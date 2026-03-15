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

  // Version
  getVersion: () => request('/api/version'),

  // 处理所有 pending 下载
  processPending: () => request('/api/task/trigger', { method: 'POST' }),

  // Old APIs (deprecated)
  processAllPending: () => fetch('/api/downloads/batch/process-pending', { method: 'POST' }).then(r => r.json()),
  retryAllFailed: () => fetch('/api/downloads/batch/retry-failed', { method: 'POST' }).then(r => r.json()),
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
