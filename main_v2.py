#!/usr/bin/env python3
"""
main_v2.py - Optimized HLS downloader
Improvements:
- Uses Chrome Network Logs (CDP) to find m3u8 (bypasses obfuscation)
- Shared aiohttp session for better performance
- Smart Resume: Detects expired tokens (older than 2h) and refreshes URL
- Safe FFmpeg cleanup (verifies output before deleting source)
- Correct async concurrency limits
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
import shutil
import subprocess
import asyncio
from pathlib import Path
from urllib.parse import urljoin, urlparse

import aiohttp
import aiofiles
from concurrent.futures import ThreadPoolExecutor
from tqdm.asyncio import tqdm_asyncio
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


# -------------------------
# Configuration
# -------------------------
MAX_SELENIUM_WORKERS = 1            # Chrome instances concurrently
SELENIUM_WAIT_SECONDS = 10
DOWNLOAD_CONCURRENCY = 32           # Total concurrent TS downloads
PER_HOST_LIMIT = 32                 # IMPROVED: Match concurrency to prevent bottlenecks
TS_RETRIES = 5
TS_TIMEOUT = 30
DOWNLOAD_BACKOFF_BASE = 0.5
REFERER = "https://en.xchina.co/"
USER_AGENT = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
SQLITE_DB = "jobs.sqlite"
USE_SQLITE = False
LOGFILE = "downloader.log"

# Fallback domain only if relative paths cannot be resolved via m3u8 URL
CDN_FALLBACK = "https://video.xchina.download"

# -------------------------
# Logging setup
# -------------------------
logger = logging.getLogger("hls_downloader")
logger.setLevel(logging.DEBUG)
ch = logging.StreamHandler()
ch.setLevel(logging.INFO)
fh = logging.FileHandler(LOGFILE, encoding='utf-8')
fh.setLevel(logging.DEBUG)
formatter = logging.Formatter("%(asctime)s [%(levelname)s] %(message)s", "%H:%M:%S")
ch.setFormatter(formatter)
fh.setFormatter(formatter)
logger.addHandler(ch)
logger.addHandler(fh)

# -------------------------
# Utilities
# -------------------------
def sanitize_filename(text):
    # Remove invalid chars and limit length
    cleaned = re.sub(r'[\\/*?:"<>|]', '', str(text)).strip()
    return cleaned[:120]

def extract_video_id(page_url):
    match = re.search(r'id-([a-fA-F0-9]+)\.html', page_url)
    return match.group(1) if match else "unknown_" + str(int(time.time()))

def atomic_write_text(path: Path, data: str):
    tmp = path.with_suffix(path.suffix + ".part")
    tmp.write_text(data, encoding="utf-8")
    tmp.replace(path)

def ensure_dir(path: Path):
    path.mkdir(parents=True, exist_ok=True)

# -------------------------
# Database
# -------------------------
def init_db():
    if not USE_SQLITE:
        return None
    # IMPROVED: check_same_thread=False needed for async/threaded access
    conn = sqlite3.connect(SQLITE_DB, check_same_thread=False)
    cur = conn.cursor()
    cur.execute("""
    CREATE TABLE IF NOT EXISTS jobs (
      id INTEGER PRIMARY KEY,
      url TEXT UNIQUE,
      video_id TEXT,
      status TEXT,
      attempts INTEGER DEFAULT 0,
      last_error TEXT,
      updated_at INTEGER
    )""")
    conn.commit()
    return conn

def add_job_db(conn, url):
    if conn is None: return
    vid = extract_video_id(url)
    try:
        cur = conn.cursor()
        cur.execute("INSERT OR IGNORE INTO jobs (url, video_id, status, updated_at) VALUES (?, ?, ?, ?)",
                    (url, vid, "pending", int(time.time())))
        conn.commit()
    except Exception as e:
        logger.error(f"DB Error adding job: {e}")

def pop_pending_job(conn):
    if conn is None: return None
    try:
        cur = conn.cursor()
        cur.execute("SELECT url FROM jobs WHERE status='pending' ORDER BY id LIMIT 1")
        row = cur.fetchone()
        if row:
            url = row[0]
            cur.execute("UPDATE jobs SET status='in_progress', updated_at=? WHERE url=?", (int(time.time()), url))
            conn.commit()
            return url
    except Exception as e:
        logger.error(f"DB Error popping job: {e}")
    return None

def mark_job_done(conn, url, ok=True, error=None):
    if conn is None: return
    status = "done" if ok else "failed"
    try:
        cur = conn.cursor()
        cur.execute("UPDATE jobs SET status=?, last_error=?, updated_at=? WHERE url=?",
                    (status, error, int(time.time()), url))
        conn.commit()
    except Exception as e:
        logger.error(f"DB Error marking done: {e}")

# -------------------------
# Selenium / Network Capture
# -------------------------
def find_m3u8_url_with_selenium(page_url, debug=False):
    """
    Uses Chrome Performance Logs to capture the actual M3U8 request.
    This is much more robust than regex on page source.
    """
    chrome_options = Options()
    chrome_options.add_argument("--headless")
    chrome_options.add_argument("--disable-gpu")
    chrome_options.add_argument("--no-sandbox")
    chrome_options.add_argument("--log-level=3")
    chrome_options.add_argument(f"user-agent={USER_AGENT}")

    # IMPROVED: Enable Performance Logging
    chrome_options.set_capability('goog:loggingPrefs', {'performance': 'ALL'})

    service = Service(log_path=os.devnull)
    driver = webdriver.Chrome(service=service, options=chrome_options)

    title = "Unknown"
    model = "UnknownModel"
    poster_url = None
    m3u8_url = None

    try:
        driver.get(page_url)

        # Wait for player or details
        try:
            WebDriverWait(driver, SELENIUM_WAIT_SECONDS).until(
                EC.presence_of_element_located((By.CSS_SELECTOR, "video, .video-detail"))
            )
        except Exception:
            pass # Continue anyway to scrape metadata

        # 1. Scrape Metadata (Soup)
        soup = BeautifulSoup(driver.page_source, "html.parser")

        # Title
        title_el = soup.select_one(".video-detail .item .text") or soup.select_one("h1")
        if title_el:
            title = title_el.get_text(strip=True)

        # Poster
        poster_el = soup.select_one("video[poster]") or soup.select_one(".screenshot-container img")
        if poster_el:
            poster_url = poster_el.get("poster") or poster_el.get("src")

        # Model Name (Heuristic)
        model_els = soup.select(".model-item")
        if model_els:
            model = model_els[0].get_text(strip=True)

        # 2. Extract M3U8 from Network Logs (The robust way)
        logs = driver.get_log('performance')
        for entry in logs:
            try:
                message = json.loads(entry['message'])['message']
                if message['method'] == 'Network.requestWillBeSent':
                    req_url = message['params']['request']['url']
                    # Look for m3u8. We prioritize the last one found as it's often the highest quality/playlist
                    if '.m3u8' in req_url and 'favicon' not in req_url:
                        m3u8_url = req_url
            except Exception:
                continue

        # Fallback to Source Regex if Network capture failed
        if not m3u8_url:
            if debug: logger.warning("Network log empty, falling back to Regex")
            match = re.search(r'https?://[^\s"\'<>]+\.m3u8[^\s"\'<>]*', driver.page_source)
            if match:
                m3u8_url = match.group(0)

        if debug:
            logger.info(f"Found: {title} | {model} | {m3u8_url}")

        return title, m3u8_url, poster_url, model

    except Exception as e:
        logger.error(f"Selenium Error: {e}")
        return title, None, None, model
    finally:
        driver.quit()

# -------------------------
# M3U8 Parsing
# -------------------------
def parse_m3u8(m3u8_text, m3u8_url):
    """
    Returns: (rewritten_m3u8_content, list_of_absolute_ts_urls)
    """
    lines = m3u8_text.splitlines()
    base_url = m3u8_url.rsplit('/', 1)[0] + '/'
    parsed_url = urlparse(m3u8_url)
    root_domain = f"{parsed_url.scheme}://{parsed_url.netloc}"

    new_lines = []
    ts_urls = []

    for line in lines:
        line = line.strip()
        if not line:
            continue
        if line.startswith("#"):
            # Handle key URI if present
            if 'URI="' in line:
                def replace_uri(match):
                    uri = match.group(1)
                    if uri.startswith("http"): return f'URI="{uri}"'
                    if uri.startswith("/"): return f'URI="{root_domain}{uri}"'
                    return f'URI="{base_url}{uri}"'
                line = re.sub(r'URI="([^"]+)"', replace_uri, line)
            new_lines.append(line)
            continue

        # It's a segment
        if line.startswith("http"):
            abs_url = line
        elif line.startswith("/"):
            # If line is root relative, prioritize M3U8 domain, fallback to config
            abs_url = root_domain + line
        else:
            abs_url = base_url + line

        ts_urls.append(abs_url)
        # In the local m3u8, we want relative paths to the 'ts_stream' folder logic
        # But convert_m3u8_to_mp4 handles the final rewriting.
        # Here we just keep the original structure or filename.
        new_lines.append(line)

    return "\n".join(new_lines), ts_urls

# -------------------------
# Async Download
# -------------------------
async def download_ts_segments(session, ts_urls, video_id, retries=TS_RETRIES):
    ts_dir = Path("ts_stream") / video_id
    ensure_dir(ts_dir)
    progress_file = ts_dir / ".progress.json"

    # Load progress
    done = set()
    if progress_file.exists():
        try:
            done = set(json.loads(progress_file.read_text()))
        except: pass

    # Prepare Tasks
    # Filter out already downloaded files (check existence and size > 0)
    to_download = []
    for i, url in enumerate(ts_urls):
        filename = f"{i:05d}.ts" # Normalize filenames to keep order 00000.ts
        file_path = ts_dir / filename
        if filename in done and file_path.exists() and file_path.stat().st_size > 0:
            continue
        to_download.append((url, file_path, filename))

    if not to_download:
        return [] # All done

    # Semaphore is handled by the TCPConnector limit in the session,
    # but we can add an extra layer if strictly needed.

    headers = {"User-Agent": USER_AGENT, "Referer": REFERER}
    failed = []

    async def fetch(url, path, name):
        for attempt in range(1, retries + 1):
            try:
                async with session.get(url, headers=headers, timeout=TS_TIMEOUT) as resp:
                    if resp.status == 200:
                        data = await resp.read()
                        if len(data) == 0:
                            raise Exception("Empty bytes received")
                        async with aiofiles.open(path, "wb") as f:
                            await f.write(data)
                        return (name, True, None)
                    elif resp.status in [403, 410]:
                        # Token expired or forbidden
                        return (name, False, "403_FORBIDDEN")
                    else:
                        # Other server error, retry
                        if attempt == retries:
                            return (name, False, f"HTTP_{resp.status}")
            except Exception as e:
                if attempt == retries:
                    return (name, False, str(e))
                await asyncio.sleep(DOWNLOAD_BACKOFF_BASE * attempt)
        return (name, False, "MAX_RETRIES")

    # Execute
    tasks = [fetch(u, p, n) for u, p, n in to_download]

    # Use tqdm
    pbar = tqdm_asyncio(total=len(tasks), desc=f"DL {video_id}", leave=False)

    for coro in asyncio.as_completed(tasks):
        name, success, err = await coro
        pbar.update(1)
        if success:
            done.add(name)
        else:
            if err == "403_FORBIDDEN":
                # If we hit a 403, it's likely the whole batch is invalid.
                # Stop early to trigger refresh.
                failed.append("403_FORBIDDEN")
                break
            failed.append(err)

    pbar.close()

    # Save progress
    atomic_write_text(progress_file, json.dumps(list(done)))

    return failed

# -------------------------
# FFmpeg
# -------------------------
def run_ffmpeg(video_id, model, title):
    base_dir = Path.cwd()
    ffmpeg_exe = base_dir / "ffmpeg.exe"
    ffmpeg_cmd = str(ffmpeg_exe) if ffmpeg_exe.exists() else "ffmpeg"

    safe_model = sanitize_filename(model)
    safe_title = sanitize_filename(title)

    # Paths
    ts_dir = (base_dir / "ts_stream" / video_id).resolve()
    input_m3u8 = ts_dir / "local.m3u8"
    output_dir = (base_dir / "Videos" / safe_model / safe_title).resolve()
    output_file = output_dir / f"{safe_title}.mp4"

    ensure_dir(output_dir)

    if not ts_dir.exists() or not input_m3u8.exists():
        logger.error(f"Missing TS data or playlist for {video_id}")
        return False

    # Check if we have the key if needed
    if "enc.key" in input_m3u8.read_text(encoding="utf-8") and not (ts_dir / "enc.key").exists():
        logger.error("Encryption key missing from download folder.")
        return False

    cmd = [
        ffmpeg_cmd, "-y", "-nostdin",
        "-allowed_extensions", "ALL",
        "-protocol_whitelist", "file,crypto,tcp",
        "-i", "local.m3u8",
        "-c", "copy",
        str(output_file)
    ]

    try:
        logger.info(f"Decrypting & Merging {video_id} -> {output_file.name}")

        # Run INSIDE the ts_dir so it finds 'enc.key' and the .ts files locally
        subprocess.run(
            cmd,
            check=True,
            capture_output=True,
            text=True,
            cwd=ts_dir
        )

        if output_file.exists() and output_file.stat().st_size > 1024:
            logger.info("Success! Cleaning up...")
            shutil.rmtree(ts_dir, ignore_errors=True)
            return True
        else:
            logger.error("FFmpeg ran but output file is empty.")
            return False

    except subprocess.CalledProcessError as e:
        logger.error(f"FFmpeg failed with exit code {e.returncode}")
        err_msg = "\n".join(e.stderr.splitlines()[-15:])
        logger.error(f"FFMPEG ERROR:\n{err_msg}")
        return False

# -------------------------
# Logic Controller
# -------------------------
async def process_video(executor, session, url, db_conn=None):
    video_id = extract_video_id(url)
    m3u8_path = Path("m3u8") / f"{video_id}.m3u8"
    meta_path = Path("m3u8") / f"{video_id}.json"
    ensure_dir(Path("m3u8"))

    m3u8_url = None
    title = video_id
    model = "Unknown"
    poster = None

    # Define headers (CRITICAL for bypassing 403)
    headers = {
        "User-Agent": USER_AGENT,
        "Referer": REFERER
    }

    # Check cache
    need_scrape = True
    if meta_path.exists():
        try:
            meta = json.loads(meta_path.read_text())
            if time.time() - meta.get('timestamp', 0) < 7200:
                m3u8_url = meta.get('m3u8_url')
                title = meta.get('title')
                model = meta.get('model')
                poster = meta.get('poster')
                if m3u8_url:
                    need_scrape = False
                    logger.info(f"Resuming {video_id} from cache")
        except: pass

    # 1. Scrape (if needed)
    if need_scrape:
        loop = asyncio.get_event_loop()
        title, m3u8_url, poster, model = await loop.run_in_executor(
            executor, find_m3u8_url_with_selenium, url
        )
        if not m3u8_url:
            logger.error(f"Failed to find m3u8 for {url}")
            if db_conn: mark_job_done(db_conn, url, False, "No M3U8 found")
            return

        meta = {
            "url": url,
            "m3u8_url": m3u8_url,
            "title": title,
            "model": model,
            "poster": poster,
            "timestamp": int(time.time())
        }
        atomic_write_text(meta_path, json.dumps(meta, indent=2))

    # ---------------------------------------------------------
    # FIXED: Poster Download with Headers
    # ---------------------------------------------------------
    if poster:
        try:
            p_dir = Path("Videos") / sanitize_filename(model) / sanitize_filename(title)
            ensure_dir(p_dir)
            p_file = p_dir / "poster.jpg"

            if not p_file.exists() or p_file.stat().st_size == 0:
                if not poster.startswith("http"):
                    poster = urljoin(url, poster)

                # FIX: Passed 'headers' here
                async with session.get(poster, headers=headers) as r:
                    if r.status == 200:
                        p_file.write_bytes(await r.read())
                        logger.info(f"Saved poster: {p_file.name}")
                    else:
                        logger.warning(f"Poster download failed: {r.status}")
        except Exception as e:
            logger.warning(f"Poster error: {e}")
    # ---------------------------------------------------------

    # 2. Get M3U8 Content
    try:
        # FIX: Passed 'headers' here
        async with session.get(m3u8_url, headers=headers) as resp:
            if resp.status != 200:
                logger.error(f"M3U8 link dead: {resp.status}")
                return
            m3u8_content = await resp.text()
    except Exception as e:
        logger.error(f"Error fetching m3u8: {e}")
        return

    # 3. Parse and Queue TS
    rewritten_m3u8, ts_urls = parse_m3u8(m3u8_content, m3u8_url)
    ts_dir = Path("ts_stream") / video_id
    ensure_dir(ts_dir)

    # --- ENCRYPTION KEY HANDLING ---
    key_match = re.search(r'#EXT-X-KEY:METHOD=AES-128,URI="([^"]+)"', m3u8_content)
    if key_match:
        key_uri = key_match.group(1)
        if not key_uri.startswith("http"):
            parsed = urlparse(m3u8_url)
            base = f"{parsed.scheme}://{parsed.netloc}"
            if key_uri.startswith("/"):
                key_url = base + key_uri
            else:
                key_url = urljoin(m3u8_url, key_uri)
        else:
            key_url = key_uri

        logger.info(f"Downloading key: {key_url}")
        try:
            # FIX: Passed 'headers' here
            async with session.get(key_url, headers=headers) as kresp:
                if kresp.status == 200:
                    (ts_dir / "enc.key").write_bytes(await kresp.read())
                else:
                    logger.error(f"Failed to download key: {kresp.status}")
        except Exception as e:
            logger.error(f"Key download error: {e}")
    # -------------------------------

    if not ts_urls:
        logger.error("No segments found in M3U8")
        return

    # 4. Download Segments
    errors = await download_ts_segments(session, ts_urls, video_id)
    if errors and "403_FORBIDDEN" in errors:
        logger.warning(f"Token expired for {video_id}. Clearing cache...")
        meta_path.unlink(missing_ok=True)
        return

    if errors:
        logger.error(f"Incomplete download for {video_id}: {len(errors)} errors")
        if db_conn: mark_job_done(db_conn, url, False, "Missing Segments")
        return

    # -----------------------------------------------------------------------
    # Generate a strictly LOCAL playlist for FFmpeg
    # -----------------------------------------------------------------------
    try:
        lines = m3u8_content.splitlines()
        new_lines = []
        seg_count = 0
        for line in lines:
            line = line.strip()
            if not line: continue

            if line.startswith("#EXT-X-KEY"):
                line = re.sub(r'URI="[^"]+"', 'URI="enc.key"', line)
                new_lines.append(line)
            elif line.startswith("#"):
                new_lines.append(line)
            else:
                new_lines.append(f"{seg_count:05d}.ts")
                seg_count += 1
        (ts_dir / "local.m3u8").write_text("\n".join(new_lines), encoding="utf-8")
    except Exception as e:
        logger.error(f"Failed to write local m3u8: {e}")
        return

    # 5. Merge
    success = run_ffmpeg(video_id, model, title)
    if db_conn: mark_job_done(db_conn, url, success, "FFmpeg failed" if not success else None)

# -------------------------
# Main
# -------------------------
async def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", help="Single URL to process")
    parser.add_argument("--file", help="File with list of URLs")
    args = parser.parse_args()

    # Load URLs
    urls = []
    conn = init_db()

# 1. Check Arguments
    if args.url:
        urls.append(args.url)
    elif args.file:
        if os.path.exists(args.file):
            with open(args.file, 'r') as f:
                urls = [l.strip() for l in f if l.strip()]

    # 2. Fallback: Check for url.txt automatically
    elif os.path.exists("url.txt"):
        print("Found url.txt, loading...")
        with open("url.txt", 'r') as f:
            urls = [l.strip() for l in f if l.strip()]

    # 3. Fallback: Ask user input interactively
    if not urls and not (conn and USE_SQLITE):
        print("No arguments provided.")
        u = input("Enter a Video URL: ").strip()
        if u:
            urls.append(u)

    # If DB enabled, prioritize pending jobs
    if conn and USE_SQLITE:
        urls = []
        while True:
            u = pop_pending_job(conn)
            if not u: break
            urls.append(u)

    if not urls:
        print("No URLs found. Usage: main.py --url <link> OR --file <path>")
        return

    logger.info(f"Loaded {len(urls)} jobs.")

    # IMPROVED: Shared Session with optimized connector
    connector = aiohttp.TCPConnector(limit=DOWNLOAD_CONCURRENCY, limit_per_host=PER_HOST_LIMIT, ssl=False)
    async with aiohttp.ClientSession(connector=connector) as session:

        # Executor for Selenium (Blocking)
        with ThreadPoolExecutor(max_workers=MAX_SELENIUM_WORKERS) as executor:

            # Process one video at a time completely (Scrape -> Download -> Merge)
            # You can parallelize this loop if you want multiple VIDEOS at once,
            # but usually it's better to focus bandwidth on one video (32 segments) at a time.
            for url in tqdm(urls, desc="Total Progress"):
                try:
                    await process_video(executor, session, url, conn)
                except Exception as e:
                    logger.error(f"Critical error on {url}: {e}")

    if conn: conn.close()
    logger.info("All jobs finished.")

if __name__ == "__main__":
    if sys.platform == 'win32':
        asyncio.set_event_loop_policy(asyncio.WindowsSelectorEventLoopPolicy())
    asyncio.run(main())
