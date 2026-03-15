import React from 'react';
const { createElement: h } = React;

export function formatBytes(bytes) {
  if (!bytes || bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

export function formatTime(t) {
  if (!t) return '--';
  const d = new Date(t);
  if (isNaN(d.getTime())) return String(t).slice(0, 16);
  return d.toLocaleString('zh-CN', { month:'2-digit', day:'2-digit', hour:'2-digit', minute:'2-digit' });
}

export function cn(...classes) { return classes.filter(Boolean).join(' '); }

// Toast 系统
let toastId = 0;
export const toastListeners = [];
export function toast(message, type = 'info') {
  const id = ++toastId;
  const t = { id, message, type };
  toastListeners.forEach(fn => fn(t));
  setTimeout(() => {
    toastListeners.forEach(fn => fn({ ...t, remove: true }));
  }, 3000);
}
toast.success = (msg) => toast(msg, 'success');
toast.error = (msg) => toast(msg, 'error');
toast.info = (msg) => toast(msg, 'info');

// SVG 图标
const ICON_PATHS = {
  'layout-dashboard': 'M4 4h6v6H4V4zm10 0h6v6h-6V4zM4 14h6v6H4v-6zm10 0h6v6h-6v-6z',
  'rss': 'M4 11a9 9 0 0 1 9 9M4 4a16 16 0 0 1 16 16M5 20a1 1 0 1 0 0-2 1 1 0 0 0 0 2z',
  'video': 'M15 10l4.553-2.276A1 1 0 0 1 21 8.618v6.764a1 1 0 0 1-1.447.894L15 14M3 8a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8z',
  'users': 'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM22 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75',
  'settings': 'M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z',
  'terminal': 'M4 17l6-6-6-6M12 19h8',
  'play': 'M6 4l12 8-12 8V4z',
  'pause': 'M6 4h4v16H6V4zM14 4h4v16h-4V4z',
  'refresh': 'M1 4v6h6M23 20v-6h-6M20.49 9A9 9 0 0 0 5.64 5.64L1 10M22.95 14l-4.64 4.36A9 9 0 0 1 3.51 15',
  'check': 'M20 6L9 17l-5-5',
  'x': 'M18 6L6 18M6 6l12 12',
  'download': 'M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3',
  'hard-drive': 'M22 12H2M5.45 5.11L2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z',
  'plus': 'M12 5v14M5 12h14',
  'trash': 'M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2',
  'search': 'M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM21 21l-4.35-4.35',
  'chevron-left': 'M15 18l-6-6 6-6',
  'chevron-right': 'M9 18l6-6-6-6',
  'menu': 'M3 12h18M3 6h18M3 18h18',
  'external-link': 'M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6M15 3h6v6M10 14L21 3',
  'filter': 'M22 3H2l8 9.46V19l4 2v-8.54L22 3z',
  'grid': 'M3 3h7v7H3V3zM14 3h7v7h-7V3zM3 14h7v7H3v-7zM14 14h7v7h-7v-7z',
  'list': 'M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01',
  'sync': 'M21.5 2v6h-6M2.5 22v-6h6M2 11.5a10 10 0 0 1 18.8-4.3M22 12.5a10 10 0 0 1-18.8 4.2',
  'edit': 'M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z',
  'qr-code': 'M3 3h7v7H3V3zM14 3h7v7h-7V3zM3 14h7v7H3v-7zM17 14h1v3h-1v-3zM14 17h3v4h-3v-4zM20 14h1v7h-4v-1h3v-6z',
  'file-x': 'M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6M9.5 12.5l5 5M14.5 12.5l-5 5',
  'alert-circle': 'M12 22c5.523 0 10-4.477 10-10S17.523 2 12 2 2 6.477 2 12s4.477 10 10 10zM12 8v4M12 16h.01',
};

export function Icon({ name, size = 18, className = '' }) {
  const d = ICON_PATHS[name] || '';
  return h('svg', { width: size, height: size, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: 2, strokeLinecap: 'round', strokeLinejoin: 'round', className }, h('path', { d }));
}

// UI 组件
export function Badge({ children, variant = 'default', className = '' }) {
  const variants = {
    default: 'bg-blue-500/20 text-blue-400',
    success: 'bg-emerald-500/20 text-emerald-400',
    error: 'bg-red-500/20 text-red-400',
    warning: 'bg-amber-500/20 text-amber-400',
    outline: 'border border-slate-600 text-slate-400',
  };
  return h('span', { className: cn('inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium', variants[variant] || variants.default, className) }, children);
}

export function StatusBadge({ status }) {
  const map = {
    completed: { label: '已完成', variant: 'success' },
    relocated: { label: '已迁移', variant: 'success' },
    downloading: { label: '下载中', variant: 'default' },
    pending: { label: '待处理', variant: 'warning' },
    failed: { label: '失败', variant: 'error' },
    permanent_failed: { label: '永久失败', variant: 'error' },
    cancelled: { label: '已取消', variant: 'outline' },
    skipped: { label: '已跳过', variant: 'outline' },
    charge_blocked: { label: '充电专属', variant: 'warning' },
  };
  const s = map[status] || { label: status || '未知', variant: 'outline' };
  return h(Badge, { variant: s.variant }, s.label);
}

export function Card({ children, className = '', hover = false, onClick }) {
  return h('div', {
    onClick,
    className: cn('bg-slate-800/60 border border-slate-700/50 rounded-xl p-5', hover && 'card-hover cursor-pointer', className)
  }, children);
}

export function Button({ children, onClick, variant = 'primary', size = 'md', disabled = false, className = '' }) {
  const variants = {
    primary: 'bg-blue-500 hover:bg-blue-600 text-white',
    secondary: 'bg-slate-700 hover:bg-slate-600 text-slate-200',
    danger: 'bg-red-600 hover:bg-red-700 text-white',
    ghost: 'hover:bg-slate-700/50 text-slate-300',
    outline: 'border border-slate-600 hover:bg-slate-700/50 text-slate-300',
  };
  const sizes = { sm: 'px-3 py-1.5 text-xs', md: 'px-4 py-2 text-sm', lg: 'px-6 py-3' };
  return h('button', {
    onClick, disabled,
    className: cn('inline-flex items-center justify-center gap-2 rounded-lg font-medium transition-colors', variants[variant], sizes[size], disabled && 'opacity-50 cursor-not-allowed', className),
  }, children);
}

export function Skeleton({ className = '' }) {
  return h('div', { className: cn('skeleton rounded-lg h-4', className) });
}

export function EmptyState({ icon = 'video', message = '暂无数据', action }) {
  return h('div', { className: 'flex flex-col items-center justify-center py-16 text-slate-500' },
    h(Icon, { name: icon, size: 48, className: 'mb-4 opacity-30' }),
    h('p', { className: 'text-lg mb-4' }, message),
    action
  );
}

export function Pagination({ page, pageSize, total, onChange }) {
  const totalPages = Math.ceil(total / pageSize);
  if (totalPages <= 1) return null;
  const pages = [];
  const start = Math.max(1, page - 2);
  const end = Math.min(totalPages, page + 2);
  for (let i = start; i <= end; i++) pages.push(i);
  return h('div', { className: 'flex items-center justify-between mt-6' },
    h('span', { className: 'text-sm text-slate-500' }, `共 ${total} 条`),
    h('div', { className: 'flex items-center gap-1' },
      h('button', { onClick: () => onChange(page - 1), disabled: page <= 1, className: 'p-2 rounded-lg hover:bg-slate-700/50 disabled:opacity-30 text-slate-400' }, h(Icon, { name: 'chevron-left', size: 16 })),
      pages.map(p => h('button', { key: p, onClick: () => onChange(p), className: cn('w-8 h-8 rounded-lg text-sm', p === page ? 'bg-blue-500 text-white' : 'hover:bg-slate-700/50 text-slate-400') }, p)),
      h('button', { onClick: () => onChange(page + 1), disabled: page >= totalPages, className: 'p-2 rounded-lg hover:bg-slate-700/50 disabled:opacity-30 text-slate-400' }, h(Icon, { name: 'chevron-right', size: 16 }))
    )
  );
}
