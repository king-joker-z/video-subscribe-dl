#!/usr/bin/env python3
"""
subscribe.py — Model Subscription Manager for en.xchina.co

Usage:
  python subscribe.py add   <model_url_or_id>       # Subscribe + download full history
  python subscribe.py remove <model_id>              # Unsubscribe (keeps downloaded files)
  python subscribe.py list                           # Show all subscriptions
  python subscribe.py run                            # One-shot: poll all models, download new
  python subscribe.py run --watch [--interval N]     # Daemon: poll every N hours (default 6)
  python subscribe.py sync  <model_url_or_id>        # Force-sync one model right now

Requires main_v2.py in the same directory for the core download pipeline.
"""

import os
import re
import sys
import time
import json
import random
import logging
import argparse
import sqlite3
import asyncio
from pathlib import Path
from urllib.parse import urlparse
from concurrent.futures import ThreadPoolExecutor

import aiohttp
from tqdm import tqdm

from selenium import webdriver
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC
from bs4 import BeautifulSoup
import warnings
warnings.filterwarnings("ignore", category=DeprecationWarning)

# ---------------------------------------------------------------------------
# Try to import shared utilities from main_v2; inline fallback if not present
# ---------------------------------------------------------------------------
try:
    from main_v2 import (
        find_m3u8_url_with_selenium,
        process_video,
        sanitize_filename,
        ensure_dir,
        atomic_write_text,
        extract_video_id,
        DOWNLOAD_CONCURRENCY,
        PER_HOST_LIMIT,
        USER_AGENT,
        REFERER,
        LOGFILE,
    )
    _MAIN_IMPORTED = True
except ImportError:
    _MAIN_IMPORTED = False

    # ---- Inline minimal copies (used when main_v2.py is absent) ----
    DOWNLOAD_CONCURRENCY = 32
    PER_HOST_LIMIT = 32
    USER_AGENT = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
    REFERER = "https://en.xchina.co/"
    LOGFILE = "downloader.log"

    def sanitize_filename(text):
        import re as _re
        cleaned = _re.sub(r'[\\/*?:"<>|]', '', str(text)).strip()
        return cleaned[:120]

    def ensure_dir(path: Path):
        path.mkdir(parents=True, exist_ok=True)

    def atomic_write_text(path: Path, data: str):
        tmp = path.with_suffix(path.suffix + ".part")
        tmp.write_text(data, encoding="utf-8")
        tmp.replace(path)

    def extract_video_id(page_url):
        match = re.search(r'id-([a-fA-F0-9]+)\.html', page_url)
        return match.group(1) if match else None

    # These will be None — subscribe.py warns if invoked without main_v2.py
    find_m3u8_url_with_selenium = None
    process_video = None


# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------
SQLITE_DB = "jobs.sqlite"
SUBSCRIBE_LOG = "subscribe.log"
BASE_URL = "https://en.xchina.co"
MODEL_URL_PATTERN = re.compile(r'(?:https?://[^/]+)?/model/id-([a-zA-Z0-9_-]+)')
DEFAULT_POLL_INTERVAL_HOURS = 6
PAGE_DELAY_MIN = 2.0   # seconds between page loads (anti-bot)
PAGE_DELAY_MAX = 4.0
CLOUDFLARE_RETRIES = 3
SELENIUM_WAIT = 12


# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
logger = logging.getLogger("subscribe")
logger.setLevel(logging.DEBUG)

_ch = logging.StreamHandler()
_ch.setLevel(logging.INFO)
_fh = logging.FileHandler(SUBSCRIBE_LOG, encoding="utf-8")
_fh.setLevel(logging.DEBUG)
_fmt = logging.Formatter("%(asctime)s [%(levelname)s] %(message)s", "%H:%M:%S")
_ch.setFormatter(_fmt)
_fh.setFormatter(_fmt)
logger.addHandler(_ch)
logger.addHandler(_fh)


# ---------------------------------------------------------------------------
# Database
# ---------------------------------------------------------------------------

def open_db() -> sqlite3.Connection:
    conn = sqlite3.connect(SQLITE_DB, check_same_thread=False)
    conn.row_factory = sqlite3.Row
    _migrate(conn)
    return conn


def _migrate(conn: sqlite3.Connection):
    cur = conn.cursor()
    cur.executescript("""
    CREATE TABLE IF NOT EXISTS subscriptions (
        model_id    TEXT PRIMARY KEY,
        model_name  TEXT,
        model_url   TEXT,
        added_at    INTEGER DEFAULT 0,
        last_polled INTEGER DEFAULT 0,
        enabled     INTEGER DEFAULT 1
    );

    CREATE TABLE IF NOT EXISTS seen_videos (
        video_id      TEXT PRIMARY KEY,
        model_id      TEXT,
        title         TEXT,
        page_url      TEXT,
        discovered_at INTEGER DEFAULT 0,
        downloaded_at INTEGER DEFAULT 0,
        file_path     TEXT,
        status        TEXT DEFAULT 'pending'
    );
    """)
    conn.commit()


# ---- Subscription helpers ----

def db_add_subscription(conn, model_id, model_name, model_url):
    conn.execute(
        """INSERT OR REPLACE INTO subscriptions
           (model_id, model_name, model_url, added_at, last_polled, enabled)
           VALUES (?, ?, ?, ?, COALESCE((SELECT last_polled FROM subscriptions WHERE model_id=?), 0), 1)""",
        (model_id, model_name, model_url, int(time.time()), model_id)
    )
    conn.commit()


def db_remove_subscription(conn, model_id):
    conn.execute("UPDATE subscriptions SET enabled=0 WHERE model_id=?", (model_id,))
    conn.commit()


def db_list_subscriptions(conn):
    return conn.execute(
        "SELECT * FROM subscriptions ORDER BY added_at DESC"
    ).fetchall()


def db_get_subscription(conn, model_id):
    return conn.execute(
        "SELECT * FROM subscriptions WHERE model_id=?", (model_id,)
    ).fetchone()


def db_get_seen_ids(conn, model_id) -> set:
    rows = conn.execute(
        "SELECT video_id FROM seen_videos WHERE model_id=?", (model_id,)
    ).fetchall()
    return {r["video_id"] for r in rows}


def db_insert_seen_video(conn, video_id, model_id, page_url, title=""):
    conn.execute(
        """INSERT OR IGNORE INTO seen_videos
           (video_id, model_id, page_url, title, discovered_at, status)
           VALUES (?, ?, ?, ?, ?, 'pending')""",
        (video_id, model_id, page_url, title, int(time.time()))
    )
    conn.commit()


def db_mark_video_done(conn, video_id, file_path=""):
    conn.execute(
        "UPDATE seen_videos SET status='done', downloaded_at=?, file_path=? WHERE video_id=?",
        (int(time.time()), file_path, video_id)
    )
    conn.commit()


def db_mark_video_failed(conn, video_id, reason=""):
    conn.execute(
        "UPDATE seen_videos SET status='failed', file_path=? WHERE video_id=?",
        (reason, video_id)
    )
    conn.commit()


def db_update_last_polled(conn, model_id):
    conn.execute(
        "UPDATE subscriptions SET last_polled=? WHERE model_id=?",
        (int(time.time()), model_id)
    )
    conn.commit()


# ---------------------------------------------------------------------------
# URL / ID parsing
# ---------------------------------------------------------------------------

def parse_model_id(raw: str) -> tuple[str, str]:
    """
    Returns (model_id, full_url).
    Accepts:
      - Full URL: https://en.xchina.co/model/id-abc123
      - Path:     /model/id-abc123
      - Bare ID:  abc123
    """
    m = MODEL_URL_PATTERN.search(raw)
    if m:
        model_id = m.group(1)
        full_url = f"{BASE_URL}/model/id-{model_id}"
        return model_id, full_url
    # Bare ID
    if re.fullmatch(r'[a-zA-Z0-9_-]+', raw):
        return raw, f"{BASE_URL}/model/id-{raw}"
    raise ValueError(f"Cannot parse model ID from: {raw!r}")


# ---------------------------------------------------------------------------
# Selenium: scrape model page for video URLs
# ---------------------------------------------------------------------------

# CSS selectors tried in order (most → least specific)
_VIDEO_LINK_SELECTORS = [
    ".item.video a[href*='/video/id-']",
    ".video-item a[href*='/video/id-']",
    "a[href*='/video/id-']",
]

_MODEL_NAME_SELECTORS = [
    ".model-name",
    ".performer-name",
    "h1",
    "title",
]

_NEXT_PAGE_SELECTORS = [
    ".pagination a[rel='next']",
    ".pagination .next a",
    ".pagination .next",
    "a.next",
    "a[rel='next']",
]


def _make_chrome_driver():
    opts = Options()
    opts.add_argument("--headless")
    opts.add_argument("--disable-gpu")
    opts.add_argument("--no-sandbox")
    opts.add_argument("--log-level=3")
    opts.add_argument(f"user-agent={USER_AGENT}")
    opts.set_capability("goog:loggingPrefs", {"performance": "ALL"})
    service = Service(log_path=os.devnull)
    return webdriver.Chrome(service=service, options=opts)


def _is_cloudflare_challenge(driver) -> bool:
    title = driver.title.lower()
    return "just a moment" in title or "cloudflare" in title


def _extract_video_links(soup: BeautifulSoup) -> list[str]:
    """Return absolute video page URLs found on the page."""
    links = []
    for sel in _VIDEO_LINK_SELECTORS:
        els = soup.select(sel)
        if els:
            for el in els:
                href = el.get("href", "")
                if "/video/id-" in href:
                    if href.startswith("http"):
                        links.append(href)
                    else:
                        links.append(BASE_URL + href)
            break
    # Deduplicate preserving order
    seen = set()
    result = []
    for l in links:
        if l not in seen:
            seen.add(l)
            result.append(l)
    return result


def _extract_model_name(soup: BeautifulSoup, fallback="Unknown") -> str:
    for sel in _MODEL_NAME_SELECTORS:
        el = soup.select_one(sel)
        if el:
            text = el.get_text(strip=True)
            if text:
                return text
    return fallback


def _find_next_page_url(soup: BeautifulSoup, current_url: str) -> str | None:
    for sel in _NEXT_PAGE_SELECTORS:
        el = soup.select_one(sel)
        if el:
            href = el.get("href", "")
            if href and href != "#":
                if href.startswith("http"):
                    return href
                return BASE_URL + href
    # Fallback: look for page number increment in URL
    m = re.search(r'[?&]page=(\d+)', current_url)
    if m:
        page_n = int(m.group(1))
        return re.sub(r'([?&]page=)\d+', rf'\g<1>{page_n + 1}', current_url)
    return None


def scrape_model_page(
    model_url: str,
    known_ids: set | None = None,
    full_history: bool = True,
) -> tuple[str, list[str]]:
    """
    Scrape a model's page and return (model_name, list_of_video_urls).

    Args:
        model_url:    Full URL to the model's page.
        known_ids:    Set of already-known video hex IDs. If provided and
                      full_history=False, pagination stops when a page has
                      no new IDs (i.e., we've reached content we've seen before).
        full_history: If True, always paginate all pages regardless of known_ids.

    Returns:
        (model_name, video_urls)  — video_urls are absolute, newest-first.
    """
    driver = _make_chrome_driver()
    model_name = "Unknown"
    all_video_urls: list[str] = []
    current_url = model_url

    try:
        page_num = 1
        while True:
            logger.info(f"  Scraping page {page_num}: {current_url}")

            for attempt in range(CLOUDFLARE_RETRIES):
                driver.get(current_url)
                # Wait for content or CF challenge to resolve
                time.sleep(random.uniform(PAGE_DELAY_MIN, PAGE_DELAY_MAX))
                if not _is_cloudflare_challenge(driver):
                    break
                logger.warning(f"  Cloudflare challenge on attempt {attempt+1}, retrying...")
                time.sleep(3)
            else:
                logger.error("  Cloudflare challenge not resolved, aborting model scrape.")
                break

            # Wait for video items to appear
            try:
                WebDriverWait(driver, SELENIUM_WAIT).until(
                    EC.presence_of_element_located(
                        (By.CSS_SELECTOR, "a[href*='/video/id-']")
                    )
                )
            except Exception:
                pass  # Might be the last page

            soup = BeautifulSoup(driver.page_source, "html.parser")

            # Extract model name on first page
            if page_num == 1:
                model_name = _extract_model_name(soup)
                logger.info(f"  Model name: {model_name!r}")

            # Extract video links on this page
            page_links = _extract_video_links(soup)
            logger.debug(f"  Found {len(page_links)} video links on page {page_num}")

            if not page_links:
                logger.info("  No video links found, assuming last page.")
                break

            # Early-stop logic for incremental polls
            if not full_history and known_ids is not None:
                page_ids = {extract_video_id(u) for u in page_links if extract_video_id(u)}
                new_on_page = page_ids - known_ids
                all_video_urls.extend(page_links)
                if not new_on_page:
                    logger.info("  Full page of known IDs — stopping pagination early.")
                    break
            else:
                all_video_urls.extend(page_links)

            # Find next page
            next_url = _find_next_page_url(soup, current_url)
            if not next_url or next_url == current_url:
                logger.info("  No next page found, done paginating.")
                break

            current_url = next_url
            page_num += 1
            time.sleep(random.uniform(PAGE_DELAY_MIN, PAGE_DELAY_MAX))

    finally:
        driver.quit()

    # Deduplicate preserving order
    seen = set()
    unique_urls = []
    for u in all_video_urls:
        if u not in seen:
            seen.add(u)
            unique_urls.append(u)

    logger.info(f"  Total unique videos found: {len(unique_urls)}")
    return model_name, unique_urls


# ---------------------------------------------------------------------------
# Core async pipeline wrappers
# ---------------------------------------------------------------------------

async def _download_video_url(url: str, executor, session, conn, model_id: str):
    """Download a single video URL and update seen_videos status."""
    video_id = extract_video_id(url)
    if not video_id:
        logger.warning(f"Cannot extract video_id from {url}, skipping.")
        return

    if process_video is None:
        logger.error("main_v2.py not found — cannot download videos. "
                     "Please place main_v2.py in the same directory.")
        return

    logger.info(f"Downloading {video_id} ...")
    try:
        await process_video(executor, session, url, conn)
        db_mark_video_done(conn, video_id)
        logger.info(f"Done: {video_id}")
    except Exception as e:
        logger.error(f"Error downloading {video_id}: {e}")
        db_mark_video_failed(conn, video_id, str(e))


async def poll_model(
    model_id: str,
    conn: sqlite3.Connection,
    executor,
    session: aiohttp.ClientSession,
    full_history: bool = False,
):
    """
    Poll one model for new videos and download them.
    If full_history=True, downloads ALL videos (used on first subscribe).
    """
    row = db_get_subscription(conn, model_id)
    if not row:
        logger.error(f"Model {model_id} not found in subscriptions.")
        return

    model_url = row["model_url"]
    known_ids = db_get_seen_ids(conn, model_id)
    logger.info(f"Polling model {row['model_name']!r} ({model_id}), "
                f"known videos: {len(known_ids)}")

    loop = asyncio.get_event_loop()
    model_name, video_urls = await loop.run_in_executor(
        executor,
        scrape_model_page,
        model_url,
        known_ids,
        full_history,
    )

    # Update model name if it changed or was "Unknown"
    if model_name != "Unknown" and model_name != row["model_name"]:
        conn.execute(
            "UPDATE subscriptions SET model_name=? WHERE model_id=?",
            (model_name, model_id)
        )
        conn.commit()

    # Find genuinely new videos
    new_urls = [
        u for u in video_urls
        if extract_video_id(u) not in known_ids
    ]
    logger.info(f"  Found {len(new_urls)} new video(s) to download.")

    # Register all new videos in seen_videos first
    for url in new_urls:
        vid_id = extract_video_id(url)
        if vid_id:
            db_insert_seen_video(conn, vid_id, model_id, url)

    # Download sequentially (each uses full 32-connection bandwidth)
    for url in tqdm(new_urls, desc=f"  {model_name}", unit="video", leave=True):
        await _download_video_url(url, executor, session, conn, model_id)

    db_update_last_polled(conn, model_id)


# ---------------------------------------------------------------------------
# CLI Commands
# ---------------------------------------------------------------------------

async def cmd_add(args, conn):
    """Subscribe to a model and download full history."""
    model_id, model_url = parse_model_id(args.target)

    existing = db_get_subscription(conn, model_id)
    if existing and existing["enabled"]:
        logger.info(f"Already subscribed to {model_id} ({existing['model_name']}). "
                    f"Use 'sync' to re-download new videos.")
        return

    logger.info(f"Subscribing to model {model_id} ...")
    logger.info(f"Scraping full history (this may take a while)...")

    connector = aiohttp.TCPConnector(
        limit=DOWNLOAD_CONCURRENCY, limit_per_host=PER_HOST_LIMIT, ssl=False
    )
    async with aiohttp.ClientSession(connector=connector) as session:
        with ThreadPoolExecutor(max_workers=1) as executor:
            # Scrape first page just to get model name, then register subscription
            loop = asyncio.get_event_loop()
            model_name, _ = await loop.run_in_executor(
                executor,
                scrape_model_page,
                model_url,
                set(),     # no known IDs yet
                False,     # just first page for name
            )
            # Re-do with full_history=True for the actual download
            db_add_subscription(conn, model_id, model_name, model_url)
            logger.info(f"Subscribed: {model_name!r} ({model_id})")
            await poll_model(model_id, conn, executor, session, full_history=True)

    logger.info(f"Add complete for {model_name!r}.")


async def cmd_remove(args, conn):
    """Disable a subscription (files are kept)."""
    model_id, _ = parse_model_id(args.target)
    row = db_get_subscription(conn, model_id)
    if not row:
        print(f"No subscription found for {model_id!r}.")
        return
    db_remove_subscription(conn, model_id)
    print(f"Unsubscribed from {row['model_name']!r} ({model_id}). Files are kept.")


def cmd_list(conn):
    """Print all subscriptions in a table."""
    rows = db_list_subscriptions(conn)
    if not rows:
        print("No subscriptions yet. Use: python subscribe.py add <model_url>")
        return

    print(f"\n{'Model ID':<20} {'Name':<30} {'Last Polled':<22} {'Status'}")
    print("-" * 80)
    for r in rows:
        lp = time.strftime("%Y-%m-%d %H:%M", time.localtime(r["last_polled"])) \
            if r["last_polled"] else "Never"
        status = "Active" if r["enabled"] else "Disabled"

        # Count seen / done videos
        counts = conn.execute(
            "SELECT status, COUNT(*) as n FROM seen_videos WHERE model_id=? GROUP BY status",
            (r["model_id"],)
        ).fetchall()
        done = next((c["n"] for c in counts if c["status"] == "done"), 0)
        total = sum(c["n"] for c in counts)

        print(f"{r['model_id']:<20} {(r['model_name'] or '?'):<30} {lp:<22} "
              f"{status}  [{done}/{total} videos]")
    print()


async def cmd_run(args, conn):
    """Poll all enabled subscriptions, download new content."""
    rows = conn.execute(
        "SELECT model_id FROM subscriptions WHERE enabled=1"
    ).fetchall()

    if not rows:
        print("No active subscriptions. Use: python subscribe.py add <model_url>")
        return

    interval = args.interval if hasattr(args, "interval") and args.interval else DEFAULT_POLL_INTERVAL_HOURS

    connector = aiohttp.TCPConnector(
        limit=DOWNLOAD_CONCURRENCY, limit_per_host=PER_HOST_LIMIT, ssl=False
    )

    watch_mode = hasattr(args, "watch") and args.watch

    async with aiohttp.ClientSession(connector=connector) as session:
        with ThreadPoolExecutor(max_workers=1) as executor:
            while True:
                logger.info(f"Starting poll cycle for {len(rows)} subscription(s)...")
                for r in rows:
                    try:
                        await poll_model(r["model_id"], conn, executor, session)
                    except Exception as e:
                        logger.error(f"Error polling {r['model_id']}: {e}")

                if not watch_mode:
                    break

                logger.info(f"Cycle complete. Next poll in {interval}h. (Ctrl+C to stop)")
                try:
                    await asyncio.sleep(interval * 3600)
                except asyncio.CancelledError:
                    break

                # Refresh subscription list each cycle (user may have added new ones)
                rows = conn.execute(
                    "SELECT model_id FROM subscriptions WHERE enabled=1"
                ).fetchall()


async def cmd_sync(args, conn):
    """Force-sync (poll + download new) for one specific model."""
    model_id, model_url = parse_model_id(args.target)

    row = db_get_subscription(conn, model_id)
    if not row:
        # Auto-add if not subscribed yet
        logger.info(f"Not subscribed to {model_id}, adding first...")
        await cmd_add(args, conn)
        return

    connector = aiohttp.TCPConnector(
        limit=DOWNLOAD_CONCURRENCY, limit_per_host=PER_HOST_LIMIT, ssl=False
    )
    async with aiohttp.ClientSession(connector=connector) as session:
        with ThreadPoolExecutor(max_workers=1) as executor:
            await poll_model(model_id, conn, executor, session, full_history=False)

    logger.info(f"Sync complete for {row['model_name']!r}.")


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="subscribe.py",
        description="Model subscription manager for en.xchina.co",
    )
    sub = p.add_subparsers(dest="command", required=True)

    # add
    p_add = sub.add_parser("add", help="Subscribe to a model and download full history")
    p_add.add_argument("target", help="Model URL or ID")

    # remove
    p_rm = sub.add_parser("remove", help="Unsubscribe from a model")
    p_rm.add_argument("target", help="Model URL or ID")

    # list
    sub.add_parser("list", help="List all subscriptions")

    # run
    p_run = sub.add_parser("run", help="Poll all subscriptions for new videos")
    p_run.add_argument("--watch", action="store_true",
                       help="Keep running and poll repeatedly")
    p_run.add_argument("--interval", type=float, default=DEFAULT_POLL_INTERVAL_HOURS,
                       metavar="HOURS",
                       help=f"Poll interval in hours (default: {DEFAULT_POLL_INTERVAL_HOURS})")

    # sync
    p_sync = sub.add_parser("sync", help="Force-sync one model right now")
    p_sync.add_argument("target", help="Model URL or ID")

    return p


def main():
    parser = build_parser()
    args = parser.parse_args()

    conn = open_db()

    if sys.platform == "win32":
        asyncio.set_event_loop_policy(asyncio.WindowsSelectorEventLoopPolicy())

    try:
        if args.command == "add":
            asyncio.run(cmd_add(args, conn))
        elif args.command == "remove":
            asyncio.run(cmd_remove(args, conn))
        elif args.command == "list":
            cmd_list(conn)
        elif args.command == "run":
            asyncio.run(cmd_run(args, conn))
        elif args.command == "sync":
            asyncio.run(cmd_sync(args, conn))
    except KeyboardInterrupt:
        print("\nInterrupted.")
    finally:
        conn.close()


if __name__ == "__main__":
    main()
