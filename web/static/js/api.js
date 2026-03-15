// API 客户端 — 统一调用新版 API

const BASE = '';

async function request(path, options = {}) {
  const url = BASE + path;
  const res = await fetch(url, {
    headers: { 'Content-Type': 'application/json', ...options.headers },
    ...options,
  });
  const data = await res.json();
  if (data.code !== 0 && data.code !== undefined) {
    throw new Error(data.message || '请求失败');
  }
  return data;
}

export const api = {
  // Dashboard
  getDashboard: () => request('/api/dashboard'),

  // Sources
  getSources: (type) => request('/api/sources' + (type ? `?type=${type}` : '')),
  createSource: (body) => request('/api/sources', { method: 'POST', body: JSON.stringify(body) }),
  parseSource: (url) => request('/api/sources/parse', { method: 'POST', body: JSON.stringify({ url }) }),
  getSource: (id) => request(`/api/sources/${id}`),
  updateSource: (id, body) => request(`/api/sources/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  deleteSource: (id) => request(`/api/sources/${id}`, { method: 'DELETE' }),
  syncSource: (id) => request(`/api/sources/${id}/sync`, { method: 'POST' }),
  fullScanSource: (id) => request(`/api/sources/${id}/fullscan`, { method: 'POST' }),

  // Videos
  getVideos: (params = {}) => {
    const qs = new URLSearchParams();
    Object.entries(params).forEach(([k, v]) => { if (v) qs.set(k, v); });
    return request('/api/videos?' + qs.toString());
  },
  getVideo: (id) => request(`/api/videos/${id}`),
  retryVideo: (id) => request(`/api/videos/${id}/retry`, { method: 'POST' }),
  redownloadVideo: (id) => request(`/api/videos/${id}/redownload`, { method: 'POST' }),
  cancelVideo: (id) => request(`/api/videos/${id}/cancel`, { method: 'POST' }),
  deleteVideo: (id) => request(`/api/videos/${id}`, { method: 'DELETE' }),
  deleteVideoFiles: (id) => request(`/api/videos/${id}/delete-files`, { method: 'POST' }),
  restoreVideo: (id) => request(`/api/videos/${id}/restore`, { method: 'POST' }),
  detectCharge: () => request('/api/videos/detect-charge', { method: 'POST' }),
  batchVideos: (action, ids) => request('/api/videos/batch', {
    method: 'POST', body: JSON.stringify({ action, ids })
  }),

  // Uploaders
  getUploaders: (params = {}) => {
    const qs = new URLSearchParams();
    Object.entries(params).forEach(([k, v]) => { if (v) qs.set(k, v); });
    return request('/api/uploaders?' + qs.toString());
  },
  getUploaderVideos: (name, params = {}) => {
    const qs = new URLSearchParams();
    Object.entries(params).forEach(([k, v]) => { if (v) qs.set(k, v); });
    return request(`/api/uploaders/${encodeURIComponent(name)}/videos?` + qs.toString());
  },

  // Task
  getTaskStatus: () => request('/api/task/status'),
  triggerTask: () => request('/api/task/trigger', { method: 'POST' }),
  pauseTask: () => request('/api/task/pause', { method: 'POST' }),
  resumeTask: () => request('/api/task/resume', { method: 'POST' }),

  // Settings
  getSettings: () => request('/api/settings'),
  updateSettings: (body) => request('/api/settings', { method: 'PUT', body: JSON.stringify(body) }),

  // Credential
  getCredential: () => request('/api/credential'),
  refreshCredential: () => request('/api/credential/refresh', { method: 'POST' }),
  generateQRCode: () => request('/api/login/qrcode/generate', { method: 'POST' }),
  pollQRCode: (key) => request(`/api/login/qrcode/poll?qrcode_key=${key}`),

  // Logs
  getLogs: (limit = 100) => request(`/api/logs?limit=${limit}`),
  clearLogs: () => request('/api/logs', { method: 'POST' }),

  // Version
  getVersion: () => request('/api/version'),

  // 处理所有 pending 下载
  processPending: () => request('/api/task/trigger', { method: 'POST' }),

  // 批量下载 pending
  downloadAllPending: () => request('/api/videos/batch', {
    method: 'POST', body: JSON.stringify({ action: 'download_all_pending' })
  }),
  downloadPendingByUploader: (uploader) => request('/api/videos/batch', {
    method: 'POST', body: JSON.stringify({ action: 'download_by_uploader', uploader })
  }),
  // UP 主下载 pending（专用 endpoint）
  uploaderDownloadPending: (name) => request(`/api/uploaders/${encodeURIComponent(name)}/download-pending`, { method: 'POST' }),
  // Me — 关注列表
  getMyUppers: (page, pageSize, search) => {
    const qs = new URLSearchParams({ page, page_size: pageSize });
    if (search) qs.set('name', search);
    return request('/api/me/uppers?' + qs.toString());
  },
  batchSubscribe: (body) => request('/api/me/subscribe', { method: 'POST', body: JSON.stringify(body) }),

   // Quick Download
  quickDownload: (url) => request('/api/download', { method: 'POST', body: JSON.stringify({ url }) }),
  previewDownload: (url) => request('/api/download/preview', { method: 'POST', body: JSON.stringify({ url }) }),

  // Template preview
  previewTemplate: (template) => request("/api/settings/preview-template", { method: "POST", body: JSON.stringify({ template }) }),

  // Global Search
  globalSearch: (q) => request(`/api/search?q=${encodeURIComponent(q)}`),

  };

// SSE 事件源
export function createEventSource(onProgress, onLog, onConnected) {
  const es = new EventSource('/api/events');
  es.addEventListener('connected', () => { if (onConnected) onConnected(); });
  es.addEventListener('progress', (e) => { if (onProgress) onProgress(JSON.parse(e.data)); });
  es.addEventListener('log', (e) => { if (onLog) onLog(JSON.parse(e.data)); });
  es.onerror = () => { /* 自动重连 */ };
  return es;
}

// WebSocket 日志连接（带 SSE 降级）
export function createLogSocket(onLog, onConnected) {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const wsUrl = `${proto}//${location.host}/api/ws/logs`;
  
  let ws;
  try {
    ws = new WebSocket(wsUrl);
    ws.onopen = () => { if (onConnected) onConnected('websocket'); };
    ws.onmessage = (e) => {
      try {
        const entry = JSON.parse(e.data);
        if (onLog) onLog(entry);
      } catch(err) {}
    };
    ws.onerror = () => {
      // WebSocket 失败，降级到 SSE
      console.log('WebSocket failed, falling back to SSE');
      ws.close();
    };
    ws.onclose = () => {};
  } catch(e) {
    // 不支持 WebSocket
  }
  
  return {
    close: () => { if (ws) ws.close(); },
    ws,
  };
}
