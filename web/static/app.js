// ========================================
// Video Subscribe DL - Frontend App
// ========================================

const API = '';
let sources = [], downloads = [], people = [], currentFilter = 'all', paused = false;
let collapsedGroups = {};
let activeProgress = [];
let currentView = 'list';
let searchQuery = '';

// === Auth Token ===

function getToken() {
    return localStorage.getItem('auth_token') || '';
}

function setToken(token) {
    if (token) localStorage.setItem('auth_token', token);
    else localStorage.removeItem('auth_token');
}

function authHeaders(extra) {
    const h = extra || {};
    const t = getToken();
    if (t) h['Authorization'] = 'Bearer ' + t;
    return h;
}

function authFetch(url, opts) {
    opts = opts || {};
    const t = getToken();
    if (t) {
        // Add token to URL for EventSource compatibility and as fallback
        const sep = url.includes('?') ? '&' : '?';
        if (!opts._noQueryToken) url = url + sep + 'token=' + encodeURIComponent(t);
        // Also add Authorization header
        opts.headers = authHeaders(opts.headers || {});
    }
    return fetch(url, opts).then(resp => {
        if (resp.status === 401) {
            const token = prompt('需要访问密码才能使用，请输入:');
            if (token) {
                setToken(token);
                toast('密码已保存，正在重试...', 'info');
                location.reload();
            }
            throw new Error('unauthorized');
        }
        if (resp.status === 429) {
            const retryCount = (opts._retryCount || 0) + 1;
            if (retryCount > 2) {
                console.warn('429 max retries reached:', url);
                return resp; // 不再重试，返回 429 响应让调用方处理
            }
            const delay = retryCount * 5000; // 5s, 10s
            toast('请求频繁，' + (delay/1000) + 's后重试...', 'warning', delay);
            return new Promise(resolve => setTimeout(() => resolve(authFetch(url, {...opts, _retryCount: retryCount})), delay));
        }
        return resp;
    });
}


document.addEventListener('DOMContentLoaded', () => {
    initNav();
    initButtons();
    initSearch();
    initViewToggle();
    loadData();
    loadSettings();
    initProgressSSE();
    checkCookieGuide();
});

// === Toast Notifications ===

function toast(message, type = 'info', duration = 3000) {
    const container = document.getElementById('toastContainer');
    const t = document.createElement('div');
    t.className = `toast toast-${type}`;
    t.textContent = message;
    t.onclick = () => { t.classList.add('toast-out'); setTimeout(() => t.remove(), 300); };
    container.appendChild(t);
    setTimeout(() => {
        if (t.parentNode) { t.classList.add('toast-out'); setTimeout(() => t.remove(), 300); }
    }, duration);
}

// === Confirm Dialog ===

let confirmCallback = null;

function showDialog(title, bodyHTML, onConfirm) {
    const overlay = document.createElement('div');
    overlay.className = 'modal-overlay';
    overlay.innerHTML = '<div class="modal-box"><h3>' + title + '</h3><div class="modal-body">' + bodyHTML + '</div><div class="modal-actions"><button class="btn btn-secondary" id="dialogCancel">取消</button><button class="btn btn-danger" id="dialogOK">确认</button></div></div>';
    document.body.appendChild(overlay);
    overlay.querySelector('#dialogCancel').onclick = () => overlay.remove();
    overlay.querySelector('#dialogOK').onclick = () => { overlay.remove(); if (onConfirm) onConfirm(); };
    overlay.addEventListener('click', e => { if (e.target === overlay) overlay.remove(); });
}

function showConfirm(title, message, onOk, okText = '确认', okClass = 'btn-danger') {
    document.getElementById('confirmTitle').textContent = title;
    document.getElementById('confirmMessage').textContent = message;
    const btn = document.getElementById('confirmOkBtn');
    btn.textContent = okText;
    btn.className = 'btn ' + okClass;
    confirmCallback = onOk;
    document.getElementById('confirmModal').classList.add('show');
    btn.onclick = () => { const cb = confirmCallback; closeConfirm(); if (cb) cb(); };
}

function closeConfirm() {
    document.getElementById('confirmModal').classList.remove('show');
    confirmCallback = null;
}

// === Navigation ===

function initNav() {
    document.querySelectorAll('.nav-link').forEach(link => {
        link.addEventListener('click', () => {
            document.querySelectorAll('.nav-link').forEach(x => x.classList.remove('active'));
            document.querySelectorAll('.tab-content').forEach(x => x.classList.remove('active'));
            link.classList.add('active');
            const tab = link.dataset.tab;
            document.getElementById(`tab-${tab}`).classList.add('active');

            // Close mobile menu
            document.querySelector('.nav-links').classList.remove('show');

            // Init log tab on first visit
            if (tab === 'logs') initLogTab();
            if (tab === 'analytics' && !analyticsLoaded) loadAnalytics();
        });
    });

    // Mobile menu
    document.getElementById('mobileMenuBtn').addEventListener('click', () => {
        document.querySelector('.nav-links').classList.toggle('show');
    });

    // Close mobile menu on outside click
    document.addEventListener('click', (e) => {
        const nav = document.querySelector('.nav-links');
        const btn = document.getElementById('mobileMenuBtn');
        if (!nav.contains(e.target) && !btn.contains(e.target)) {
            nav.classList.remove('show');
        }
    });

    // Filter buttons
    document.querySelectorAll('.filter-btn').forEach(b => b.addEventListener('click', () => {
        document.querySelectorAll('.filter-btn').forEach(x => x.classList.remove('active'));
        b.classList.add('active');
        currentFilter = b.dataset.filter;
        collapsedGroups = {}; // 切换筛选时重置折叠状态
        // 显示/隐藏"开始下载"按钮
        const startBtn = document.getElementById('startDownloadBtn');
        if (startBtn) startBtn.style.display = (b.dataset.filter === 'pending') ? 'inline-block' : 'none';
        renderDownloads();
    }));
}

function initSearch() {
    const searchInput = document.getElementById('downloadSearch');
    if (searchInput) {
        let debounceTimer;
        searchInput.addEventListener('input', () => {
            clearTimeout(debounceTimer);
            debounceTimer = setTimeout(() => {
                searchQuery = searchInput.value.trim().toLowerCase();
                renderDownloads();
            }, 200);
        });
    }
}

function initViewToggle() {
    document.querySelectorAll('.view-btn').forEach(b => {
        b.addEventListener('click', () => {
            document.querySelectorAll('.view-btn').forEach(x => x.classList.remove('active'));
            b.classList.add('active');
            currentView = b.dataset.view;
            renderDownloads();
        });
    });
}

function initButtons() {
    document.getElementById('addSourceBtn').addEventListener('click', showModal);
    document.getElementById('refreshBtn').addEventListener('click', () => { loadData(); toast('已刷新', 'info', 1500); });
    document.getElementById('scanBtn').addEventListener('click', triggerScan);
    document.getElementById('syncNowBtn').addEventListener('click', syncNow);
    document.getElementById('pauseBtn').addEventListener('click', togglePause);
    document.getElementById('saveSettingsBtn').addEventListener('click', saveSettings);
    document.getElementById('uploadCookieBtn').addEventListener('click', uploadCookie);
    document.getElementById('verifyCookieBtn').addEventListener('click', verifyCookie);
    document.getElementById('scanLocalBtn').addEventListener('click', triggerScan);
    document.getElementById('cleanUploaderBtn').addEventListener('click', showCleanDialog);
    document.getElementById('reconcileCheckBtn').addEventListener('click', reconcileCheck);
    document.getElementById('reconcileFixBtn').addEventListener('click', reconcileFix);
    document.getElementById('sourceForm').addEventListener('submit', addSource);
    document.getElementById('editSourceForm').addEventListener('submit', saveEditSource);

    const g = document.getElementById('groupByUploader');
    if (g) g.addEventListener('change', renderDownloads);

    // URL auto-detect platform
    const urlInput = document.getElementById('sourceUrl');
    if (urlInput) {
        urlInput.addEventListener('input', () => {
            const icon = document.getElementById('platformIcon');
            const url = urlInput.value;
            if (url.includes('bilibili.com') || url.includes('b23.tv')) {
                icon.textContent = '📺';
                // Auto-detect type
                if (url.includes('medialist') || url.includes('favlist')) {
                    document.getElementById('sourceType').value = 'favorite';
                    onSourceTypeChange();
                } else if ((url.includes('/lists/') && url.includes('type=season')) || url.includes('collectiondetail')) {
                    document.getElementById('sourceType').value = 'season';
                    onSourceTypeChange();
                } else {
                    document.getElementById('sourceType').value = 'up';
                    onSourceTypeChange();
                }
            } else if (url.includes('youtube.com') || url.includes('youtu.be')) {
                icon.textContent = '▶️';
            } else {
                icon.textContent = '🔗';
            }
        });
    }
}

// === SSE Progress ===

let progressSSE = null;
let progressSSERetries = 0;
const PROGRESS_SSE_MAX_RETRIES = 5;

function initProgressSSE() {
    if (progressSSE) progressSSE.close();
    progressSSERetries = 0;
    connectProgressSSE();
}

function connectProgressSSE() {
    progressSSE = new EventSource(`${API}/api/progress/stream${getToken() ? '?token='+encodeURIComponent(getToken()) : ''}`);
    progressSSE.onopen = () => { progressSSERetries = 0; };
    progressSSE.onmessage = (e) => {
        try {
            const newProgress = JSON.parse(e.data) || [];
            // 检测是否有任务刚完成或失败（之前在列表里，现在不在了，或状态变为 done/error）
            const prevIds = new Set(activeProgress.map(p => p.bvid));
            const curIds = new Set(newProgress.map(p => p.bvid));
            const finished = activeProgress.some(p =>
                (p.status !== 'done' && p.status !== 'error') &&
                (!curIds.has(p.bvid) || newProgress.find(n => n.bvid === p.bvid && (n.status === 'done' || n.status === 'error')))
            );
            activeProgress = newProgress;
            renderProgressOverlay();
            if (finished) { clearTimeout(window._finishTimer); window._finishTimer = setTimeout(loadData, 2000); }
        } catch (err) { console.error('SSE parse:', err); }
    };
    progressSSE.onerror = () => {
        activeProgress = []; renderProgressOverlay();
        progressSSE.close();
        if (progressSSERetries < PROGRESS_SSE_MAX_RETRIES) {
            progressSSERetries++;
            // 指数退避: 1s, 2s, 4s, 8s, 16s... 最大 30s
            const delay = Math.min(30000, 1000 * Math.pow(2, progressSSERetries - 1));
            console.log(`SSE disconnected, reconnecting in ${delay/1000}s (${progressSSERetries}/${PROGRESS_SSE_MAX_RETRIES})...`);
            setTimeout(connectProgressSSE, delay);
        } else {
            console.warn('SSE max retries reached, giving up. Refresh page to retry.');
        }
    };
}

function renderProgressOverlay() {
    const container = document.getElementById('progressOverlay');
    if (container) { container.innerHTML = ''; container.style.display = 'none'; }
    // 轻量更新：只更新已有的进度条元素，不重建整个列表
    if (!activeProgress || activeProgress.length === 0) return;
    activeProgress.forEach(p => {
        const el = document.querySelector('[data-bvid="' + p.bvid + '"] .dl-inline-progress');
        if (el && p.percent !== undefined) {
            const pct = Math.min(100, Math.max(0, p.percent || 0)).toFixed(1);
            const phase = p.status === 'downloading_video' ? '📹 视频' : p.status === 'downloading_audio' ? '🔊 音频' : '🔄 合并中';
            const sizeText = p.total > 0 ? fmtSize(p.downloaded) + '/' + fmtSize(p.total) : fmtSize(p.downloaded);
            const bar = el.querySelector('.progress-bar-fill');
            if (bar) { bar.style.width = pct + '%'; bar.classList.remove('progress-bar-indeterminate'); }
            const text = el.querySelector('.progress-mini-text');
            if (text) text.textContent = phase + ' ' + pct + '% · ' + sizeText + ' · ' + fmtSpeed(p.speed);
        }
    });
}

// === Data Loading ===

async function loadData() {
    // 先加载 downloads（其他页面依赖此数据计算视频数）
    try { const r = await authFetch(`${API}/api/downloads`); if (r.ok) { downloads = await r.json() || []; } } catch (e) { console.error('Load downloads:', e); }
    // 再加载其他数据并渲染（依赖 downloads 数组）
    try { const r = await authFetch(`${API}/api/sources`); if (r.ok) { sources = await r.json() || []; } } catch (e) { console.error('Load sources:', e); }
    try { const r = await authFetch(`${API}/api/queue`); if (r.ok) { const q = await r.json(); paused = q.paused === 1; updatePauseBtn(); renderStats(q); } } catch (e) { console.error('Load queue:', e); }

    // 统一渲染（保证所有数据已就绪）
    renderDownloads();
    renderSources();
}

// === Cookie Guide Banner ===

let cookieGuideChecked = false;

function checkCookieGuide() {
    if (cookieGuideChecked) return;
    cookieGuideChecked = true;
    authFetch(API + '/api/settings').then(r => r.json()).then(s => {
        const banner = document.getElementById('cookieGuideBanner');
        if (!banner) return;
        if (!s.cookie_path) {
            banner.style.display = 'flex';
        } else {
            banner.style.display = 'none';
        }
    }).catch(() => {});
}

function dismissCookieGuide() {
    const banner = document.getElementById('cookieGuideBanner');
    if (banner) banner.style.display = 'none';
}

function goToCookieSettings() {
    // Switch to settings tab
    document.querySelectorAll('.nav-link').forEach(x => x.classList.remove('active'));
    document.querySelectorAll('.tab-content').forEach(x => x.classList.remove('active'));
    const settingsLink = document.querySelector('[data-tab="settings"]');
    if (settingsLink) settingsLink.classList.add('active');
    document.getElementById('tab-settings').classList.add('active');
    // Sync mobile tabs
    document.querySelectorAll('.mobile-tab').forEach(t => {
        t.classList.toggle('active', t.dataset.tab === 'settings');
    });
    dismissCookieGuide();
    // Scroll to cookie section
    setTimeout(() => {
        const cookieSection = document.getElementById('cookieFile');
        if (cookieSection) cookieSection.scrollIntoView({behavior: 'smooth', block: 'center'});
    }, 100);
}

async function loadSettings() {
    try {
        const r = await authFetch(`${API}/api/settings`);
        const s = await r.json();
        const q = document.getElementById('setting-quality');
        const n = document.getElementById('setting-nfo-type');
        const i = document.getElementById('setting-interval');
        if (q) q.value = s.download_quality || 'best';
        if (n) n.value = s.nfo_type || 'movie';
        if (i) i.value = s.request_interval || 3600;
        const sl = document.getElementById('setting-speed-limit');
        const md = document.getElementById('setting-min-disk');
        if (sl) sl.value = s.max_download_speed_mb || '0';
        if (md) md.value = s.min_disk_free_gb || '1';
        if (s.cookie_path) {
            document.getElementById('cookieStatus').textContent = '已配置: ' + s.cookie_path;
            loadCookieVerifyStatus();
            // Hide cookie guide banner
            const banner = document.getElementById('cookieGuideBanner');
            if (banner) banner.style.display = 'none';
        }
        // Auth token status
        const authStatus = document.getElementById('authTokenStatus');
        if (authStatus) {
            if (s.auth_token === '***') {
                authStatus.textContent = '已设置（如需修改请输入新密码）';
                authStatus.style.color = 'var(--success)';
            } else {
                authStatus.textContent = '未设置（设置后所有 API 请求需携带密码）';
                authStatus.style.color = 'var(--text3)';
            }
        }
        // Notification settings — multi-channel
        const nt = document.getElementById('setting-notify-type');
        const wh = document.getElementById('setting-webhook-url');
        const tgToken = document.getElementById('setting-telegram-token');
        const tgChat = document.getElementById('setting-telegram-chatid');
        const barkKey = document.getElementById('setting-bark-key');
        const barkServer = document.getElementById('setting-bark-server');
        const nc = document.getElementById('setting-notify-complete');
        const ne = document.getElementById('setting-notify-error');
        const nk = document.getElementById('setting-notify-cookie');
        const ns = document.getElementById('setting-notify-sync');
        if (nt) { nt.value = s.notify_type || 'webhook'; toggleNotifyFields(); }
        if (wh) wh.value = s.webhook_url || '';
        if (tgToken) tgToken.value = s.telegram_bot_token || '';
        if (tgChat) tgChat.value = s.telegram_chat_id || '';
        if (barkKey) barkKey.value = s.bark_key || '';
        if (barkServer) barkServer.value = s.bark_server || '';
        if (nc) nc.checked = s.notify_on_complete === 'true' || s.notify_on_complete === '1';
        if (ne) ne.checked = s.notify_on_error !== 'false' && s.notify_on_error !== '0';
        if (nk) nk.checked = s.notify_on_cookie_expire !== 'false' && s.notify_on_cookie_expire !== '0';
        if (ns) ns.checked = s.notify_on_sync === 'true' || s.notify_on_sync === '1';
    } catch (e) { console.error('Settings:', e); }
}

// === Render: Sources ===

function renderSources() {
    const c = document.getElementById('sourcesList');
    if (!sources.length) {
        c.innerHTML = `<div class="empty-state" style="grid-column:1/-1;">
            <div class="empty-icon">📡</div>
            <div class="empty-text">还没有订阅源<br>点击"添加订阅源"开始吧！</div>
            <button class="btn btn-primary" onclick="showModal()">+ 添加订阅源</button>
        </div>`;
        return;
    }

    // Count videos per source
    const videoCounts = {};
    (downloads || []).forEach(d => {
        if (d.source_id) videoCounts[d.source_id] = (videoCounts[d.source_id] || 0) + 1;
    });

    c.innerHTML = sources.map(s => {
        const videoCount = videoCounts[s.id] || 0;
        return `<div class="source-card">
            <div class="source-card-header">
                <div class="source-name">
                    <span class="source-status ${s.enabled?'online':'offline'}"></span>
                    ${esc(s.name||'未命名')}
                    <span class="badge ${s.enabled?'badge-enabled':'badge-disabled'}">${s.enabled?'启用':'禁用'}</span>
                </div>
            </div>
            <div class="source-url">${esc(s.url)}</div>
            <div class="source-meta">
                <span>📺 ${fmtSourceType(s.type)}</span>
                <span>🎬 ${videoCount} 个视频</span>
                <span>🎯 ${s.download_quality||'best'}</span>
                <span>⏱️ ${fmtInterval(s.check_interval)}</span>
                ${s.download_danmaku?'<span>💬 弹幕</span>':''}
                ${s.last_check?'<span>🕐 '+fmtDate(s.last_check)+'</span>':'<span>🕐 未检查</span>'}
            </div>
            <div class="source-actions">
                <button class="btn btn-sm btn-primary" onclick="syncSource(${s.id})">🔄 同步</button>
                <button class="btn btn-sm btn-secondary" onclick="editSource(${s.id})">✏️ 编辑</button>
                <button class="btn btn-sm btn-secondary" onclick="toggleSource(${s.id},${!s.enabled})">${s.enabled?'⏸ 禁用':'▶ 启用'}</button>
                <button class="btn btn-sm btn-danger" onclick="deleteSourceConfirm(${s.id},'${esc(s.name)}')">🗑 删除</button>
            </div>
        </div>`;
    }).join('');
}

// === Render: Downloads ===

function updateFilterCounts() {
    const all = downloads || [];
    const counts = {
        all: all.length,
        pending: all.filter(d => d.status === 'pending').length,
        downloading: all.filter(d => d.status === 'downloading').length,
        completed: all.filter(d => d.status === 'completed' || d.status === 'relocated').length,
        failed: all.filter(d => d.status === 'failed' || d.status === 'permanent_failed').length,
    };
    document.querySelectorAll('.filter-btn').forEach(b => {
        const f = b.dataset.filter;
        const count = counts[f];
        if (count !== undefined) {
            const label = b.textContent.replace(/\s*\(\d+\)/, '');
            b.textContent = count > 0 ? label + ' (' + count + ')' : label;
        }
    });
}

function renderDownloads() {
    updateFilterCounts();
    const c = document.getElementById('downloadsList');
    let list = downloads || [];

    // Filter by status
    if (currentFilter === 'failed') list = list.filter(d => d.status === 'failed' || d.status === 'permanent_failed');
    else if (currentFilter === 'downloading') list = list.filter(d => d.status === 'downloading');
    else if (currentFilter !== 'all') list = list.filter(d => d.status === currentFilter);

    // Search filter
    const sourceNameMap = {};
    (sources || []).forEach(s => { sourceNameMap[s.id] = s.name || ''; });
    if (searchQuery) {
        list = list.filter(d =>
            (d.title && d.title.toLowerCase().includes(searchQuery)) ||
            (d.uploader && d.uploader.toLowerCase().includes(searchQuery)) ||
            (sourceNameMap[d.source_id] && sourceNameMap[d.source_id].toLowerCase().includes(searchQuery)) ||
            (d.video_id && d.video_id.toLowerCase().includes(searchQuery))
        );
    }

    if (!list.length) {
        const emptyMsg = searchQuery ? '没有找到匹配的视频' : (currentFilter !== 'all' ? '该筛选条件下没有视频' : '暂无下载记录');
        c.innerHTML = `<div class="empty-state"><div class="empty-icon">📭</div><div class="empty-text">${emptyMsg}</div></div>`;
        c.className = 'download-list';
        return;
    }

    c.className = 'download-list' + (currentView === 'card' ? ' card-view' : '');

    // 按订阅源分组（和订阅源列表一致）
    const sourceMap = {};
    (sources || []).forEach(s => { sourceMap[s.id] = s.name || '未命名'; });
    const groups = {};
    list.forEach(d => {
        const k = sourceMap[d.source_id] || d.uploader || '未知';
        if (!groups[k]) groups[k] = [];
        groups[k].push(d);
    });
    if (Object.keys(groups).length > 1 || currentView === 'list') {
        let html = '';
        Object.keys(groups).sort().forEach(up => {
            const items = groups[up];
            const done = items.filter(i => i.status === 'completed' || i.status === 'relocated').length;
            const shouldCollapse = currentFilter === 'downloading' || currentFilter === 'pending' ? false : true;
            const isCollapsed = collapsedGroups[up] !== undefined ? collapsedGroups[up] : shouldCollapse;
            const collapsed = isCollapsed ? 'collapsed' : '';
            html += `<div class="group-header ${collapsed}" onclick="toggleGroup('${esc(up).replace(/'/g,"\\'")}')">
                <span class="group-toggle">${isCollapsed ? '▶' : '▼'}</span>
                <span>📺 ${esc(up)}</span>
                <span class="count">${done}/${items.length} 已完成</span>
            </div>`;
            if (!isCollapsed) {
                html += items.map(dlItem).join('');
            }
        });
        c.innerHTML = html;
    } else {
        c.innerHTML = list.map(dlItem).join('');
    }
}

function dlItem(d) {
    const thumb = null; // 封面图已禁用
    const prog = activeProgress.find(p => p.bvid === d.video_id || d.video_id.startsWith(p.bvid + '_P'));
    let progressHTML = '';
    if (prog && (prog.status === 'downloading_video' || prog.status === 'downloading_audio' || prog.status === 'merging')) {
        const pct = Math.min(100, Math.max(0, prog.percent || 0)).toFixed(1);
        const phase = prog.status === 'downloading_video' ? '📹 视频' : prog.status === 'downloading_audio' ? '🔊 音频' : '🔄 合并中';
        const sizeText = prog.total > 0 ? fmtSize(prog.downloaded) + '/' + fmtSize(prog.total) : fmtSize(prog.downloaded);
        progressHTML = `<div class="dl-inline-progress">
            <div class="progress-bar-container"><div class="progress-bar-fill" style="width:${pct}%"></div></div>
            <span class="progress-mini-text">${phase} ${pct}% · ${sizeText} · ${fmtSpeed(prog.speed)}</span>
        </div>`;
    } else if (d.status === 'downloading') {
        // DB 状态是 downloading 但 SSE 还没推进度（刚开始或获取详情中）
        progressHTML = `<div class="dl-inline-progress">
            <div class="progress-bar-container"><div class="progress-bar-fill progress-bar-indeterminate" style="width:30%"></div></div>
            <span class="progress-mini-text">准备中...</span>
        </div>`;
    }

    let errorHTML = '';
    if ((d.status === 'failed' || d.status === 'permanent_failed') && (d.error_message || d.last_error || d.retry_count > 0)) {
        const errText = d.last_error || d.error_message || '';
        errorHTML = `<div class="error-detail">`;
        if (errText) {
            if (errText.length > 80) {
                errorHTML += `<span class="error-reason error-truncated" title="${esc(errText)}" onclick="this.classList.toggle('error-expanded')" style="cursor:pointer">⚠ <span class="error-short">${esc(errText.substring(0, 80))}…<span class="error-expand-hint" style="color:var(--info);font-size:0.85em"> 点击展开</span></span><span class="error-full" style="display:none">${esc(errText)}</span></span>`;
            } else {
                errorHTML += `<span class="error-reason">⚠ ${esc(errText)}</span>`;
            }
        }
        if (d.retry_count > 0) errorHTML += `<span class="retry-count">🔄 重试 ${d.retry_count} 次</span>`;
        errorHTML += `</div>`;
    }

    const statusLabels = {pending:'待下载',downloading:'下载中',completed:'已完成',failed:'失败',permanent_failed:'永久失败',relocated:'已迁移'};
    const showRetry = d.status === 'failed' || d.status === 'permanent_failed';
    const showRedownload = d.status === 'completed' || d.status === 'relocated';

    return `<div class="download-item" data-bvid="${d.video_id}">

        <div class="info">
            <div class="title" title="${esc(d.title)}">${esc(d.title || d.video_id)}</div>
            <div class="sub">${d.uploader ? esc(d.uploader) + ' · ' : ''}${fmtSize(d.file_size)}${d.duration ? ' · ' + fmtDur(d.duration) : ''} · ${fmtDate(d.downloaded_at || d.created_at)}</div>
            ${errorHTML}${progressHTML}
        </div>
        <span class="status-badge status-${d.status}">${statusLabels[d.status] || d.status}</span>
        ${showRetry ? `<button class="btn btn-sm btn-secondary" onclick="retryDownload(${d.id})" title="重试">🔄</button>` : ''}
        ${showRedownload ? `<button class="btn btn-sm btn-secondary" onclick="redownload(${d.id})" title="重新下载">⬇️</button><button class="btn btn-sm btn-danger" onclick="deleteRecord(${d.id})" title="清除记录（下次同步会重新下载）">🗑</button>` : ''}
    </div>`;
}

function toggleGroup(up) {
    const shouldCollapse = currentFilter === 'downloading' || currentFilter === 'pending' ? false : true;
    const current = collapsedGroups[up] !== undefined ? collapsedGroups[up] : shouldCollapse;
    collapsedGroups[up] = !current;
    renderDownloads();
}

// === Render: People ===

function renderPeople() {
    const c = document.getElementById('peopleList');
    const all = downloads || [];
    const srcList = sources || [];
    if (!srcList.length) {
        c.innerHTML = '<div class="empty-state" style="grid-column:1/-1;"><div class="empty-icon">👤</div><div class="empty-text">暂无订阅源</div></div>';
        return;
    }
    // 按 source 生成 UP 主卡片（和下载列表分组完全一致）
    const sourceMap = {};
    srcList.forEach(s => { sourceMap[s.id] = s; });
    
    // 统计每个 source 的视频数和完成数
    const sourceCounts = {};
    all.forEach(d => {
        if (!sourceCounts[d.source_id]) sourceCounts[d.source_id] = {total: 0, done: 0};
        sourceCounts[d.source_id].total++;
        if (d.status === 'completed' || d.status === 'relocated') sourceCounts[d.source_id].done++;
    });

    // 匹配 people 头像
    const peopleMap = {};
    (people || []).forEach(p => { peopleMap[p.name] = p; });

    c.innerHTML = srcList.map(s => {
        const counts = sourceCounts[s.id] || {total: 0, done: 0};
        const person = peopleMap[s.name] || {};
        return '<div class="person-card" onclick="showSourceVideos(' + s.id + ')" title="查看视频列表">' +
            (person.avatar ? '<img class="avatar" src="' + person.avatar + '" alt="' + esc(s.name) + '" loading="lazy" onerror="this.style.display=\'none\'">' : '') +
            '<div class="avatar-placeholder" ' + (person.avatar ? 'style="display:none"' : '') + '>👤</div>' +
            '<div class="name">' + esc(s.name || '未命名') + '</div>' +
            '<div class="video-count">' + counts.done + '/' + counts.total + ' 已完成</div>' +
        '</div>';
    }).join('');
}

function renderStats(q) {
    // 统一从 downloads 数组算，保证和下载列表一致
    const all = downloads || [];
    const setEl = (id, v) => { const el = document.getElementById(id); if (el) el.textContent = v; };
    setEl('stat-pending', all.filter(d => d.status === 'pending').length);
    setEl('stat-downloading', all.filter(d => d.status === 'downloading').length);
    setEl('stat-completed', all.filter(d => d.status === 'completed' || d.status === 'relocated').length);
    setEl('stat-failed', all.filter(d => d.status === 'failed' || d.status === 'permanent_failed').length);
    // Update disk info from stats API
    updateDiskDisplay();
}

async function updateDiskDisplay() {
    try {
        const r = await authFetch(API + '/api/stats');
        const stats = await r.json();
        const diskEl = document.getElementById('stat-disk');
        if (diskEl && stats.disk_total) {
            const used = fmtSize(stats.disk_used || 0);
            const total = fmtSize(stats.disk_total || 0);
            const free = fmtSize(stats.disk_free || 0);
            diskEl.textContent = free + ' / ' + total;
            diskEl.title = '已用: ' + used + ' | 剩余: ' + free + ' | 总计: ' + total;
        }
    } catch(e) {}
}

// === Actions ===

// 源类型切换时更新 UI
function onSourceTypeChange() {
    const type = document.getElementById('sourceType').value;
    const urlInput = document.getElementById('sourceUrl');
    const urlHint = document.getElementById('sourceUrlHint');
    const urlWrapper = urlInput.closest('.form-group');
    
    if (type === 'watchlater') {
        urlInput.removeAttribute('required');
        urlInput.value = '';
        urlInput.placeholder = '自动使用当前 Cookie 账号';
        urlInput.disabled = true;
        if (urlHint) urlHint.textContent = '稍后再看将自动同步当前登录账号的列表';
    } else if (type === 'favorite') {
        urlInput.setAttribute('required', '');
        urlInput.disabled = false;
        urlInput.placeholder = '粘贴收藏夹链接，如 space.bilibili.com/xxx/favlist?fid=xxx';
        if (urlHint) urlHint.textContent = '粘贴 B 站收藏夹页面链接';
    } else if (type === 'season') {
        urlInput.setAttribute('required', '');
        urlInput.disabled = false;
        urlInput.placeholder = '粘贴合集链接，如 space.bilibili.com/xxx/lists/xxx?type=season';
        if (urlHint) urlHint.textContent = '粘贴 B 站合集页面链接';
    } else {
        urlInput.setAttribute('required', '');
        urlInput.disabled = false;
        urlInput.placeholder = '粘贴 bilibili 链接...';
        if (urlHint) urlHint.textContent = '支持 space.bilibili.com (UP主) 或收藏夹链接';
    }
}

async function addSource(e) {
    e.preventDefault();
    const btn = document.getElementById('addSourceSubmit');
    btn.disabled = true; btn.textContent = '添加中...';
    const src = {
        type: document.getElementById('sourceType').value,
        url: document.getElementById('sourceUrl').value,
        name: document.getElementById('sourceName').value,
        check_interval: parseInt(document.getElementById('sourceInterval').value) || 3600,
        download_quality: document.getElementById('sourceQuality').value,
        download_codec: document.getElementById('sourceCodec').value,
        download_danmaku: document.getElementById('sourceDanmaku').checked,
        enabled: true
    };
    try {
        const r = await authFetch(`${API}/api/sources`, {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(src)});
        const d = await r.json();
        if (r.ok) {
            closeModal();
            toast('已添加: ' + (d.name || '新订阅源'), 'success');
            loadData();
            authFetch(`${API}/api/queue/run`, {method:'POST'});
        } else {
            toast('添加失败: ' + (d.error || ''), 'error');
        }
    } catch(e) { toast('添加失败: ' + e.message, 'error'); }
    finally { btn.disabled = false; btn.textContent = '添加'; }
}

function editSource(id) {
    const s = sources.find(x => x.id === id);
    if (!s) return;
    document.getElementById('editSourceId').value = id;
    document.getElementById('editSourceName').value = s.name || '';
    document.getElementById('editSourceInterval').value = s.check_interval || 3600;
    document.getElementById('editSourceQuality').value = s.download_quality || 'best';
    document.getElementById('editSourceCodec').value = s.download_codec || 'all';
    document.getElementById('editSourceDanmaku').checked = !!s.download_danmaku;
    document.getElementById('editSourceModal').classList.add('show');
}

async function saveEditSource(e) {
    e.preventDefault();
    const id = parseInt(document.getElementById('editSourceId').value);
    const s = sources.find(x => x.id === id);
    if (!s) return;
    s.name = document.getElementById('editSourceName').value;
    s.check_interval = parseInt(document.getElementById('editSourceInterval').value) || 3600;
    s.download_quality = document.getElementById('editSourceQuality').value;
    s.download_codec = document.getElementById('editSourceCodec').value;
    s.download_danmaku = document.getElementById('editSourceDanmaku').checked;
    try {
        await authFetch(`${API}/api/sources/${id}`, {method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(s)});
        closeEditModal();
        toast('已保存', 'success');
        loadData();
    } catch (e) { toast('保存失败: ' + e.message, 'error'); }
}

function closeEditModal() { document.getElementById('editSourceModal').classList.remove('show'); }

async function toggleSource(id, en) {
    const s = sources.find(x => x.id === id);
    if (!s) return;
    s.enabled = en;
    await authFetch(`${API}/api/sources/${id}`, {method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(s)});
    toast(en ? '已启用' : '已禁用', 'info', 1500);
    loadData();
}

function deleteSourceConfirm(id, name) {
    showConfirm('删除订阅源', `确定删除「${name || '该订阅源'}」？\n同时会删除该源的下载记录。`, () => deleteSource(id));
}

async function deleteSource(id) {
    try {
        const r = await authFetch(`${API}/api/sources/${id}`, {method:'DELETE'});
        const d = await r.json();
        if (d.ok) { toast('已删除', 'success'); loadData(); }
        else toast('删除失败: ' + (d.error || ''), 'error');
    } catch (e) { toast('删除失败: ' + e.message, 'error'); }
}

async function syncSource(id) {
    toast('已触发同步', 'info', 1500);
    await authFetch(`${API}/api/sources/${id}/sync`, {method:'POST'});
    setTimeout(loadData, 5000);
}

async function startAllPending() {
    showConfirm('开始下载', '将所有待下载的视频提交到下载队列', async () => {
        try {
            const r = await authFetch(API + '/api/downloads/batch/process-pending', {method:'POST'});
            if (r.ok) { toast('已开始下载', 'info', 2000); setTimeout(loadData, 5000); }
            else toast('操作失败', 'error');
        } catch(e) { toast('操作失败', 'error'); }
    });
}

async function deleteRecord(id) {
    showConfirm('清除记录', '删除该下载记录后，下次同步会重新下载此视频', async () => {
        try {
            const r = await authFetch(API + '/api/downloads/' + id, {method:'DELETE'});
            if (r.ok) { toast('已清除', 'success', 2000); loadData(); }
            else toast('操作失败', 'error');
        } catch(e) { toast('操作失败: ' + e.message, 'error'); }
    });
}

async function redownload(id) {
    showConfirm('确认重新下载？', '将重新下载此视频（覆盖已有文件）', async () => {
        try {
            const r = await authFetch(`${API}/api/downloads/${id}/redownload`, {method:'POST'});
            if (r.ok) { toast('已提交重新下载', 'info', 2000); setTimeout(loadData, 3000); }
            else { const e = await r.json(); toast(e.error || '操作失败', 'error'); }
        } catch(e) { toast('操作失败', 'error'); }
    });
}

async function retryDownload(id) {
    await authFetch(`${API}/api/downloads/${id}/retry`, {method:'POST'});
    toast('已重置下载', 'info', 1500);
    loadData();
}

async function syncNow() {
    const b = document.getElementById('syncNowBtn');
    b.disabled = true; b.textContent = '⏳ 同步中...';
    toast('已触发全量同步', 'info');
    await authFetch(`${API}/api/queue/run`, {method:'POST'});
    setTimeout(() => { b.disabled = false; b.textContent = '▶ 同步'; loadData(); }, 5000);
}

async function togglePause() {
    if (paused) { await authFetch(`${API}/api/queue/resume`, {method:'POST'}); paused = false; toast('已恢复', 'success', 1500); }
    else { await authFetch(`${API}/api/queue/pause`, {method:'POST'}); paused = true; toast('已暂停', 'warning', 1500); }
    updatePauseBtn();
}

function updatePauseBtn() {
    const b = document.getElementById('pauseBtn');
    b.textContent = paused ? '▶ 恢复' : '⏸ 暂停';
    b.className = paused ? 'btn btn-primary btn-nav' : 'btn btn-secondary btn-nav';
}

async function triggerScan() {
    toast('扫描已启动', 'info');
    await authFetch(`${API}/api/scan`, {method:'POST'});
    setTimeout(loadData, 5000);
}

function showCleanDialog() {
    const ups = new Set();
    (sources || []).forEach(s => { if (s.name) ups.add(s.name); });
    const sorted = Array.from(ups).sort();
    if (sorted.length === 0) { toast('没有可清理的UP主', 'warning'); return; }
    const options = sorted.map(n => '<option value="' + esc(n) + '">' + esc(n) + '</option>').join('');
    showDialog('清理UP主数据', '<p>选择要清理的UP主（将删除该UP主的所有下载记录和本地文件）</p><select id="cleanUploaderSelect" class="form-select" style="width:100%;padding:8px;margin-top:8px">' + options + '</select>', () => {
        const name = document.getElementById('cleanUploaderSelect').value;
        if (name) cleanUploader(name);
    });
}

async function cleanUploader(name) {
    try {
        const r = await authFetch(`${API}/api/clean/uploader/${encodeURIComponent(name)}`, {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({delete_files:true})});
        const d = await r.json();
        if (d.ok) { toast(`已清理 ${d.records} 条记录`, 'success'); loadData(); }
        else toast('失败: ' + (d.error || ''), 'error');
    } catch (e) { toast('失败: ' + e.message, 'error'); }
}


async function saveAuthToken() {
    const input = document.getElementById('authTokenInput');
    const token = input ? input.value.trim() : '';
    if (!token) { toast('请输入访问密码', 'warning'); return; }
    try {
        const r = await authFetch(`${API}/api/settings/auth_token`, {
            method: 'PUT',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({value: token})
        });
        const d = await r.json();
        if (d.ok) {
            setToken(token);
            toast('访问密码已设置', 'success');
            if (input) input.value = '';
            loadSettings();
            // Reconnect SSE with new token
            initProgressSSE();
            if (logInitialized) startLogSSE();
        } else {
            toast('设置失败: ' + (d.error || ''), 'error');
        }
    } catch (e) { toast('设置失败: ' + e.message, 'error'); }
}

async function removeAuthToken() {
    showConfirm('移除访问密码', '确定移除访问密码？移除后所有人都可以访问 API。', async () => {
        try {
            const r = await authFetch(`${API}/api/settings/auth_token`, {
                method: 'PUT',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({value: ''})
            });
            const d = await r.json();
            if (d.ok) {
                setToken('');
                toast('访问密码已移除', 'success');
                loadSettings();
                initProgressSSE();
                if (logInitialized) startLogSSE();
            } else {
                toast('移除失败: ' + (d.error || ''), 'error');
            }
        } catch (e) { toast('移除失败: ' + e.message, 'error'); }
    });
}

async function saveSettings() {
    const s = {
        download_quality: document.getElementById('setting-quality').value,
        request_interval: document.getElementById('setting-interval').value,
        nfo_type: document.getElementById('setting-nfo-type').value,
        max_download_speed_mb: document.getElementById('setting-speed-limit').value || '0',
        min_disk_free_gb: document.getElementById('setting-min-disk').value || '1',
        notify_type: document.getElementById('setting-notify-type').value || 'webhook',
        webhook_url: document.getElementById('setting-webhook-url').value || '',
        telegram_bot_token: document.getElementById('setting-telegram-token').value || '',
        telegram_chat_id: document.getElementById('setting-telegram-chatid').value || '',
        bark_key: document.getElementById('setting-bark-key').value || '',
        bark_server: document.getElementById('setting-bark-server').value || '',
        notify_on_complete: document.getElementById('setting-notify-complete').checked ? 'true' : 'false',
        notify_on_error: document.getElementById('setting-notify-error').checked ? 'true' : 'false',
        notify_on_cookie_expire: document.getElementById('setting-notify-cookie').checked ? 'true' : 'false',
        notify_on_sync: document.getElementById('setting-notify-sync').checked ? 'true' : 'false'
    };
    await authFetch(`${API}/api/settings`, {method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(s)});
    toast('设置已保存', 'success');
}

async function uploadCookie() {
    const f = document.getElementById('cookieFile').files[0];
    if (!f) { toast('请选择文件', 'warning'); return; }
    const fd = new FormData(); fd.append('cookie', f);
    try {
        const r = await authFetch(`${API}/api/cookie/upload`, {method:'POST',body:fd,_noQueryToken:false});
        const d = await r.json();
        if (d.ok) {
            document.getElementById('cookieStatus').textContent = '已上传: ' + d.path;
            showCookieVerifyResult(d);
            toast('Cookie 已上传', 'success');
        } else { toast('上传失败', 'error'); }
    } catch (e) { toast('上传失败: ' + e.message, 'error'); }
}

async function verifyCookie() {
    const btn = document.getElementById('verifyCookieBtn');
    btn.disabled = true; btn.textContent = '⏳ 验证中...';
    try {
        const r = await authFetch(`${API}/api/cookie/verify`);
        const d = await r.json();
        showCookieVerifyResult(d);
    } catch (e) { showCookieVerifyResult({ok: false, error: e.message}); }
    finally { btn.disabled = false; btn.textContent = '🔍 验证'; }
}

async function loadCookieVerifyStatus() {
    try { const r = await authFetch(`${API}/api/cookie/verify`); const d = await r.json(); showCookieVerifyResult(d); } catch {}
}

function showCookieVerifyResult(d) {
    const el = document.getElementById('cookieVerifyResult');
    if (!el) return;
    el.classList.add('show');
    if (!d.ok && d.error) { el.innerHTML = `<span style="color:var(--danger)">❌ 验证失败: ${esc(d.error)}</span>`; return; }
    if (!d.logged_in) { el.innerHTML = `<span style="color:var(--warning)">⚠️ Cookie 未登录或已失效${d.message ? ' - ' + esc(d.message) : ''}</span>`; return; }
    el.innerHTML = `<span style="color:var(--success)">✅ 登录有效</span>
        <div style="margin-top:6px"><b>用户名:</b> ${esc(d.username||'-')} &nbsp; <b>VIP:</b> <span style="color:${d.vip_active?'#e91e8c':'var(--warning)'}">${esc(d.vip_label||(d.vip_type>0?'大会员':'普通用户'))}</span></div>
        <div style="margin-top:4px;color:var(--text3);font-size:12px">最高画质: ${esc(d.max_quality||'-')} &nbsp; 最高音质: ${esc(d.max_audio||'-')}</div>`;
}

function showSourceVideos(sourceId) {
    // 从前端 downloads 数组过滤（同一数据源，保证一致）
    document.querySelectorAll('.nav-link').forEach(x => x.classList.remove('active'));
    document.querySelectorAll('.tab-content').forEach(x => x.classList.remove('active'));
    document.querySelector('[data-tab="downloads"]').classList.add('active');
    document.getElementById('tab-downloads').classList.add('active');
    
    const videos = (downloads || []).filter(d => d.source_id === sourceId);
    const src = (sources || []).find(s => s.id === sourceId);
    const name = src ? src.name : '未知';
    const c = document.getElementById('downloadsList');
    if (!videos.length) {
        c.innerHTML = '<div class="person-videos-header"><button class="btn btn-secondary btn-sm" onclick="renderDownloads()">← 返回</button><span>' + esc(name) + ' - 暂无视频</span></div>';
        return;
    }
    const done = videos.filter(v => v.status === 'completed' || v.status === 'relocated').length;
    let html = '<div class="person-videos-header"><button class="btn btn-secondary btn-sm" onclick="renderDownloads()">← 返回</button><span>' + esc(name) + ' — ' + done + '/' + videos.length + ' 已完成</span></div>';
    html += videos.map(dlItem).join('');
    c.innerHTML = html;
}

// 保留旧函数名兼容
async function showPersonVideos(name) {
    // 找到匹配的 source
    const src = (sources || []).find(s => s.name === name);
    if (src) { showSourceVideos(src.id); return; }
    // fallback: 从 downloads 按 uploader 过滤
    document.querySelectorAll('.nav-link').forEach(x => x.classList.remove('active'));
    document.querySelectorAll('.tab-content').forEach(x => x.classList.remove('active'));
    document.querySelector('[data-tab="downloads"]').classList.add('active');
    document.getElementById('tab-downloads').classList.add('active');
    const _sm = {}; (sources||[]).forEach(s=>{_sm[s.id]=s.name||''});
    const videos = (downloads || []).filter(d => d.uploader === name || (_sm[d.source_id] || '') === name);
    const c = document.getElementById('downloadsList');
    const done = videos.filter(v => v.status === 'completed' || v.status === 'relocated').length;
    let html = '<div class="person-videos-header"><button class="btn btn-secondary btn-sm" onclick="renderDownloads()">← 返回</button><span>' + esc(name) + ' — ' + done + '/' + videos.length + ' 已完成</span></div>';
    html += videos.map(dlItem).join('');
    c.innerHTML = html;
}

function showModal() { document.getElementById('sourceModal').classList.add('show'); }
function closeModal() { document.getElementById('sourceModal').classList.remove('show'); document.getElementById('sourceForm').reset(); document.getElementById('platformIcon').textContent = '🔗'; }

// === Reconcile ===

async function reconcileCheck() {
    const btn = document.getElementById('reconcileCheckBtn');
    const resultDiv = document.getElementById('reconcileResult');
    const fixBtn = document.getElementById('reconcileFixBtn');
    btn.disabled = true; btn.textContent = '⏳ 检查中...';
    resultDiv.classList.remove('show'); fixBtn.style.display = 'none';
    try {
        const resp = await authFetch(`${API}/api/scan/status`);
        const data = await resp.json();
        if (data.error) { resultDiv.innerHTML = `<span style="color:var(--danger)">❌ ${esc(data.error)}</span>`; resultDiv.classList.add('show'); return; }
        let html = '<div style="line-height:1.8">';
        if (data.is_consistent) {
            html += '<div style="color:var(--success);font-weight:bold">✅ 数据一致</div>';
            html += `<div>数据库: ${esc(String(data.total_db_records))} | 本地: ${esc(String(data.total_local_files))}</div>`;
        } else {
            html += '<div style="color:var(--warning);font-weight:bold">⚠️ 发现不一致</div>';
            html += `<div>数据库: ${esc(String(data.total_db_records))} | 本地: ${esc(String(data.total_local_files))} | 一致: ${esc(String(data.consistent))}</div>`;
            if (data.orphan_count > 0) html += `<div style="color:var(--info)">📁 本地有DB无: ${data.orphan_count}</div>`;
            if (data.missing_count > 0) html += `<div style="color:var(--danger)">🔍 DB有本地无: ${data.missing_count}</div>`;
            if (data.stale_count > 0) html += `<div style="color:var(--warning)">⏳ 残留downloading: ${data.stale_count}</div>`;
            fixBtn.style.display = 'inline-block';
        }
        html += `<div style="color:var(--text3);font-size:0.82em;margin-top:4px">检查时间: ${esc(data.checked_at)}</div></div>`;
        resultDiv.innerHTML = html;
        resultDiv.classList.add('show');
    } catch (e) { resultDiv.innerHTML = `<span style="color:var(--danger)">❌ ${esc(e.message)}</span>`; resultDiv.classList.add('show'); }
    finally { btn.disabled = false; btn.textContent = '🔍 检查一致性'; }
}

async function reconcileFix() {
    const btn = document.getElementById('reconcileFixBtn');
    const resultDiv = document.getElementById('reconcileResult');
    showConfirm('执行修复', '确定修复？\n补录orphan、标记missing、重置stale', async () => {
        btn.disabled = true; btn.textContent = '⏳ 修复中...';
        try {
            const resp = await authFetch(`${API}/api/scan/fix`, {method:'POST'});
            const data = await resp.json();
            if (data.error) { toast('修复失败: ' + data.error, 'error'); return; }
            toast('修复完成', 'success');
            btn.style.display = 'none';
            loadData();
        } catch (e) { toast('修复失败: ' + e.message, 'error'); }
        finally { btn.disabled = false; btn.textContent = '🔧 执行修复'; }
    }, '确认修复', 'btn-primary');
}

// === Log Tab ===

let logSSE = null, logAutoscroll = true, logInitialized = false;

function initLogTab() {
    if (logInitialized) return;
    logInitialized = true;
    authFetch(`${API}/api/logs?limit=500`).then(r => r.json()).then(entries => {
        const list = document.getElementById('logList');
        if (!list) return;
        entries.forEach(entry => appendLogEntry(entry));
        scrollLogToBottom();
    }).catch(err => console.error('Logs:', err));
    startLogSSE();
}

function startLogSSE() {
    if (logSSE) logSSE.close();
    logSSE = new EventSource(`${API}/api/logs/stream${getToken() ? '?token='+encodeURIComponent(getToken()) : ''}`);
    logSSE.onmessage = (e) => {
        try {
            const data = JSON.parse(e.data);
            if (data.type === 'connected') return;
            appendLogEntry(data);
            if (logAutoscroll) scrollLogToBottom();
        } catch {}
    };
}

function appendLogEntry(entry) {
    const list = document.getElementById('logList');
    if (!list) return;
    const div = document.createElement('div');
    div.className = `log-entry log-${entry.level || 'info'}`;
    div.innerHTML = `<span class="log-time">${esc(entry.time||'')}</span><span class="log-level log-level-${esc(entry.level||'info')}">${esc((entry.level||'info').toUpperCase())}</span><span class="log-message">${esc(entry.message||'')}</span>`;
    list.appendChild(div);
    while (list.children.length > 1000) list.removeChild(list.firstChild);
}

function scrollLogToBottom() {
    const c = document.getElementById('logContainer');
    if (c) c.scrollTop = c.scrollHeight;
}

function toggleLogPause() {
    logAutoscroll = !logAutoscroll;
    const btn = document.getElementById('logPauseBtn');
    if (btn) { btn.textContent = logAutoscroll ? '⏸ 暂停' : '▶ 恢复'; btn.className = logAutoscroll ? 'btn btn-secondary' : 'btn btn-primary'; }
    if (logAutoscroll) scrollLogToBottom();
}

function clearLogView() { const list = document.getElementById('logList'); if (list) list.innerHTML = ''; }

// === Helpers ===

function esc(s) { return s ? String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&#x27;') : ''; }
function fmtSize(b) { if(!b)return'-'; const u=['B','KB','MB','GB']; let i=0; while(b>=1024&&i<3){b/=1024;i++;} return b.toFixed(1)+' '+u[i]; }
function fmtDate(s) { if(!s)return'-'; try{const d=new Date(s);return d.toLocaleDateString('zh-CN')+' '+d.toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit'});}catch{return'-';} }
function fmtDur(s) { if(!s)return''; const m=Math.floor(s/60),r=s%60; return m>0?m+'分'+r+'秒':r+'秒'; }
function fmtInterval(s) { if(!s)return'-'; if(s>=3600)return(s/3600).toFixed(0)+'小时'; if(s>=60)return(s/60).toFixed(0)+'分钟'; return s+'秒'; }
function fmtSourceType(t) { const m = {up:'UP主',favorite:'收藏夹',watchlater:'稍后再看',season:'合集',series:'系列'}; return m[t] || t || '未知'; }
function fmtSpeed(bps) { if(!bps||bps<=0)return'-'; if(bps>=1048576)return(bps/1048576).toFixed(1)+' MB/s'; if(bps>=1024)return(bps/1024).toFixed(0)+' KB/s'; return bps.toFixed(0)+' B/s'; }


// === Analytics Tab ===

let analyticsLoaded = false;

async function loadAnalytics() {
    try {
        // 磁盘信息仍从后端获取
        let diskFree = 0;
        try {
            const r = await authFetch(API + '/api/stats');
            if (r.ok) { const d = await r.json(); diskFree = d.disk_free || 0; }
        } catch(e) {}
        renderAnalytics(diskFree);
        analyticsLoaded = true;
    } catch (e) {
        console.error('Load analytics:', e);
    }
}

function renderAnalytics(diskFree) {
    const all = downloads || [];
    const completed = all.filter(d => d.status === 'completed' || d.status === 'relocated');
    const failed = all.filter(d => d.status === 'failed' || d.status === 'permanent_failed');
    const totalSize = completed.reduce((s, d) => s + (d.file_size || 0), 0);
    const rate = all.length > 0 ? (completed.length / all.length * 100) : 0;

    const setEl = (id, v) => { const el = document.getElementById(id); if (el) el.textContent = v; };
    setEl('a-total', all.length);
    setEl('a-completed', completed.length);
    setEl('a-failed', failed.length);
    setEl('a-size', fmtSize(totalSize));
    setEl('a-rate', all.length > 0 ? rate.toFixed(1) + '%' : '-');
    setEl('a-disk', diskFree ? fmtSize(diskFree) : '-');

    // Monthly bar chart - 从 downloads 按月份统计
    const monthMap = {};
    completed.forEach(d => {
        const date = d.downloaded_at || d.created_at;
        if (date) {
            const month = date.substring(0, 7); // "2026-03"
            monthMap[month] = (monthMap[month] || 0) + 1;
        }
    });
    const monthData = Object.entries(monthMap).sort((a,b) => a[0].localeCompare(b[0])).map(([m, c]) => ({month: m, count: c}));
    const monthEl = document.getElementById('monthlyChart');
    if (monthData.length === 0) {
        monthEl.innerHTML = '<div class="empty-state"><div class="empty-text">暂无数据</div></div>';
    } else {
        const maxCount = Math.max(...monthData.map(m => m.count), 1);
        let html = '<div class="bar-chart-inner">';
        monthData.forEach(m => {
            const pct = Math.max(4, (m.count / maxCount) * 100);
            const label = m.month.substring(5);
            html += '<div class="bar-col"><div class="bar-value">' + m.count + '</div><div class="bar-fill" style="height:' + pct + '%"></div><div class="bar-label">' + label + '月</div></div>';
        });
        html += '</div>';
        monthEl.innerHTML = html;
    }

    // Uploader ranking - 从 downloads 按 source 分组（和下载列表一致）
    const sourceMap = {};
    (sources || []).forEach(s => { sourceMap[s.id] = s.name || '未命名'; });
    const upMap = {};
    completed.forEach(d => {
        const name = sourceMap[d.source_id] || d.uploader || '未知';
        if (!upMap[name]) upMap[name] = {count: 0, size: 0};
        upMap[name].count++;
        upMap[name].size += (d.file_size || 0);
    });
    const upData = Object.entries(upMap).map(([name, v]) => ({uploader: name, count: v.count, total_size: v.size})).sort((a,b) => b.count - a.count);
    const rankEl = document.getElementById('uploaderRank');
    if (upData.length === 0) {
        rankEl.innerHTML = '<div class="empty-state"><div class="empty-text">暂无数据</div></div>';
    } else {
        const maxUp = Math.max(...upData.map(u => u.count), 1);
        let html = '';
        upData.forEach((u, i) => {
            const pct = (u.count / maxUp) * 100;
            const medal = i === 0 ? '🥇' : i === 1 ? '🥈' : i === 2 ? '🥉' : (i+1)+'.';
            html += '<div class="rank-item"><span class="rank-medal">' + medal + '</span><span class="rank-name">' + esc(u.uploader) + '</span><div class="rank-bar-wrap"><div class="rank-bar" style="width:' + pct + '%"></div></div><span class="rank-count">' + u.count + ' 个</span><span class="rank-size">' + fmtSize(u.total_size) + '</span></div>';
        });
        rankEl.innerHTML = html;
    }
}

// === Batch Operations ===

function batchRetryFailed() {
    const failedCount = (downloads || []).filter(d => d.status === 'failed' || d.status === 'permanent_failed').length;
    if (failedCount === 0) { toast('没有失败的下载任务', 'info'); return; }
    showConfirm('批量重试', '确定重试所有 ' + failedCount + ' 个失败的下载任务？\n所有失败记录将被重置为待下载状态。', async () => {
        try {
            const r = await authFetch(API + '/api/downloads/batch/retry-failed', {method: 'POST'});
            const d = await r.json();
            if (d.ok) {
                toast('已重试 ' + (d.affected || 0) + ' 个失败任务', 'success');
                loadData();
            } else {
                toast('批量重试失败: ' + (d.error || ''), 'error');
            }
        } catch (e) { toast('批量重试失败: ' + e.message, 'error'); }
    }, '确认重试', 'btn-primary');
}

function batchDeleteCompleted() {
    const completedCount = (downloads || []).filter(d => d.status === 'completed' || d.status === 'relocated').length;
    if (completedCount === 0) { toast('没有已完成的下载记录', 'info'); return; }
    showConfirm('清理已完成', '确定删除所有 ' + completedCount + ' 条已完成的下载记录？\n注意：仅删除数据库记录，不会删除本地文件。', async () => {
        try {
            const r = await authFetch(API + '/api/downloads/batch/completed', {method: 'DELETE'});
            const d = await r.json();
            if (d.ok) {
                toast('已清理 ' + (d.affected || 0) + ' 条记录', 'success');
                loadData();
            } else {
                toast('清理失败: ' + (d.error || ''), 'error');
            }
        } catch (e) { toast('清理失败: ' + e.message, 'error'); }
    }, '确认清理', 'btn-danger');
}

// Auto refresh (only when page is visible)
setInterval(() => { if (!document.hidden) loadData(); }, 30000);

// Error message expand/collapse CSS injection
(function() {
    const style = document.createElement('style');
    style.textContent = '.error-expanded .error-short { display: none !important; } .error-expanded .error-full { display: inline !important; } .error-reason { word-break: break-all; }';
    document.head.appendChild(style);
})();


// 切换通知通道配置字段显示
function toggleNotifyFields() {
    const t = document.getElementById('setting-notify-type').value;
    const wf = document.getElementById('notify-webhook-fields');
    const tf = document.getElementById('notify-telegram-fields');
    const bf = document.getElementById('notify-bark-fields');
    const st = document.getElementById('notify-status');
    if (wf) wf.style.display = t === 'webhook' ? '' : 'none';
    if (tf) tf.style.display = t === 'telegram' ? '' : 'none';
    if (bf) bf.style.display = t === 'bark' ? '' : 'none';
    if (st) {
        const labels = {webhook: '通用 Webhook', telegram: 'Telegram Bot', bark: 'Bark (iOS)'};
        st.textContent = '当前通道: ' + (labels[t] || t);
    }
}

async function testNotify() {
    try {
        const r = await authFetch(`${API}/api/notify/test`, {method: 'POST'});
        const d = await r.json();
        if (d.ok) {
            toast('测试通知已发送 ✅', 'success');
        } else {
            toast('发送失败: ' + (d.error || '未知错误'), 'error');
        }
    } catch (e) {
        toast('发送失败: ' + e.message, 'error');
    }
}


// === Mobile Bottom Tab Navigation ===

function switchMobileTab(el) {
    // Sync with desktop nav
    const tab = el.dataset.tab;
    
    // Update mobile tabs
    document.querySelectorAll('.mobile-tab').forEach(t => t.classList.remove('active'));
    el.classList.add('active');
    
    // Update desktop nav links
    document.querySelectorAll('.nav-link').forEach(x => x.classList.remove('active'));
    const desktopLink = document.querySelector(`.nav-link[data-tab="${tab}"]`);
    if (desktopLink) desktopLink.classList.add('active');
    
    // Switch tab content
    document.querySelectorAll('.tab-content').forEach(x => x.classList.remove('active'));
    document.getElementById(`tab-${tab}`).classList.add('active');
    
    // Init log tab on first visit
    if (tab === 'logs') initLogTab();
    if (tab === 'analytics' && !analyticsLoaded) loadAnalytics();
}

// Sync desktop nav clicks to mobile tabs
const origInitNav = initNav;
document.addEventListener('DOMContentLoaded', () => {
    document.querySelectorAll('.nav-link').forEach(link => {
        link.addEventListener('click', () => {
            const tab = link.dataset.tab;
            document.querySelectorAll('.mobile-tab').forEach(t => {
                t.classList.toggle('active', t.dataset.tab === tab);
            });
        });
    });
});

// === Mobile Action Sheet for Source Cards ===

let actionSheetSourceId = null;

function showActionSheet(id) {
    const s = sources.find(x => x.id === id);
    if (!s) return;
    actionSheetSourceId = id;
    
    document.getElementById('actionSheetTitle').textContent = s.name || '操作';
    const items = document.getElementById('actionSheetItems');
    items.innerHTML = `
        <button class="action-sheet-item" onclick="syncSource(${id}); closeActionSheet();">
            🔄 <span>立即同步</span>
        </button>
        <button class="action-sheet-item" onclick="editSource(${id}); closeActionSheet();">
            ✏️ <span>编辑设置</span>
        </button>
        <button class="action-sheet-item" onclick="toggleSource(${id}, ${!s.enabled}); closeActionSheet();">
            ${s.enabled ? '⏸' : '▶'} <span>${s.enabled ? '禁用' : '启用'}订阅</span>
        </button>
        <button class="action-sheet-item danger" onclick="deleteSourceConfirm(${id}, '${esc(s.name)}'); closeActionSheet();">
            🗑 <span>删除订阅源</span>
        </button>
    `;
    
    document.getElementById('actionSheetBackdrop').classList.add('show');
    document.getElementById('actionSheet').style.display = 'block';
}

function closeActionSheet() {
    document.getElementById('actionSheetBackdrop').classList.remove('show');
    document.getElementById('actionSheet').style.display = 'none';
    actionSheetSourceId = null;
}

// Override renderSources to add mobile touch support
const origRenderSources = renderSources;
renderSources = function() {
    origRenderSources();
    // Add click handler for mobile action sheet on source cards
    if (window.innerWidth <= 768) {
        document.querySelectorAll('.source-card').forEach(card => {
            // Find the source id from the card's sync button
            const syncBtn = card.querySelector('button[onclick*="syncSource"]');
            if (syncBtn) {
                const match = syncBtn.getAttribute('onclick').match(/syncSource\((\d+)\)/);
                if (match) {
                    card.addEventListener('click', (e) => {
                        // Don't trigger on button clicks
                        if (e.target.closest('button')) return;
                        showActionSheet(parseInt(match[1]));
                    });
                }
            }
        });
    }
};

// Sync mobile pause button state
const origUpdatePauseBtn = updatePauseBtn;
updatePauseBtn = function() {
    origUpdatePauseBtn();
    const mobileBtn = document.getElementById('pauseBtnMobile');
    if (mobileBtn) {
        mobileBtn.textContent = paused ? '▶ 恢复' : '⏸ 暂停';
        mobileBtn.className = paused ? 'btn btn-primary btn-sm' : 'btn btn-secondary btn-sm';
    }
};
