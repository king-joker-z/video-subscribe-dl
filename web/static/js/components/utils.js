import React from 'react';
const { createElement: h } = React;


// 格式化下载速度 (bytes/sec -> "1.5 MB/s")
export function formatSpeed(bytesPerSec) {
  if (!bytesPerSec || bytesPerSec <= 0) return '';
  const k = 1024;
  const sizes = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
  const i = Math.floor(Math.log(bytesPerSec) / Math.log(k));
  const idx = Math.min(i, sizes.length - 1);
  return parseFloat((bytesPerSec / Math.pow(k, idx)).toFixed(1)) + ' ' + sizes[idx];
}

// 计算并格式化 ETA ("3分12秒", "< 1秒")
export function formatETA(downloaded, total, speed) {
  if (!speed || speed <= 0 || !total || total <= 0) return '';
  const remaining = total - downloaded;
  if (remaining <= 0) return '即将完成';
  const sec = Math.ceil(remaining / speed);
  if (sec < 1) return '< 1秒';
  if (sec < 60) return sec + '秒';
  const min = Math.floor(sec / 60);
  const s = sec % 60;
  if (min < 60) return s > 0 ? min + '分' + s + '秒' : min + '分';
  const hr = Math.floor(min / 60);
  const m = min % 60;
  return hr + '时' + (m > 0 ? m + '分' : '');
}
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

export function formatTimeAgo(t) {
  if (!t) return '';
  const d = new Date(t);
  if (isNaN(d.getTime())) return '';
  const now = Date.now();
  const diff = now - d.getTime();
  if (diff < 0) return '刚刚';
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return '刚刚';
  const min = Math.floor(sec / 60);
  if (min < 60) return min + '分钟前';
  const hr = Math.floor(min / 60);
  if (hr < 24) return hr + '小时前';
  const day = Math.floor(hr / 24);
  if (day < 30) return day + '天前';
  return d.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' });
}

export function formatNextCheck(lastCheck, interval) {
  if (!lastCheck || !interval) return '';
  const last = new Date(lastCheck);
  if (isNaN(last.getTime())) return '';
  const next = last.getTime() + interval * 1000;
  const now = Date.now();
  const diff = next - now;
  if (diff <= 0) return '即将检查';
  const min = Math.floor(diff / 60000);
  if (min < 60) return min + '分钟后';
  const hr = Math.floor(min / 60);
  return hr + '小时' + (min % 60) + '分后';
}


// Toast 系统
let toastId = 0;
export const toastListeners = [];
export function toast(message, type = 'info') {
  const id = ++toastId;
  const t = { id, message, type };
  toastListeners.forEach(fn => fn(t));
  setTimeout(() => {
    // 先触发退出动画
    toastListeners.forEach(fn => fn({ ...t, exiting: true }));
    setTimeout(() => {
      toastListeners.forEach(fn => fn({ ...t, remove: true }));
    }, 300); // 动画时长
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
  'chevron-up': 'M18 15l-6-6-6 6',
  'chevron-down': 'M6 9l6 6 6-6',
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
  'alert-triangle': 'M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0zM12 9v4M12 17h.01',
  'clock': 'M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20zM12 6v6l4 2',
  'upload': 'M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M17 8l-5-5-5 5M12 3v12',
  'x-circle': 'M22 12a10 10 0 1 0-20 0 10 10 0 0 0 20 0zM15 9l-6 6M9 9l6 6',
  'undo': 'M3 7v6h6M3 13a9 9 0 1 0 2.5-6.3L3 7',
  'square': 'M3 3h18v18H3V3z',
  'check-square': 'M9 11l3 3L22 4M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11',
};

export function Icon({ name, size = 18, className = '' }) {
  const d = ICON_PATHS[name] || '';
  return h('svg', { width: size, height: size, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: 2, strokeLinecap: 'round', strokeLinejoin: 'round', className }, h('path', { d }));
}

// UI 组件
export function Badge({ children, variant = 'default', className = '' }) {
  const variants = {
    default: 'bg-blue-100 text-blue-700',
    success: 'bg-emerald-100 text-emerald-700',
    error: 'bg-red-100 text-red-700',
    warning: 'bg-amber-100 text-amber-700',
    outline: 'border border-slate-300 text-slate-600',
  };
  return h('span', { className: cn('inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium', variants[variant] || variants.default, className) }, children);
}

export function StatusBadge({ status }) {
  const map = {
    completed:        { label: '已完成',   variant: 'success',  tip: '下载完成' },
    relocated:        { label: '已迁移',   variant: 'success',  tip: '文件已迁移' },
    downloading:      { label: '下载中',   variant: 'default',  tip: '下载中' },
    pending:          { label: '待处理',   variant: 'warning',  tip: '等待下载' },
    failed:           { label: '失败',     variant: 'error',    tip: '下载失败，点击重试' },
    permanent_failed: { label: '永久失败', variant: 'error',    tip: '下载失败，点击重试' },
    cancelled:        { label: '已取消',   variant: 'outline',  tip: '已取消下载' },
    skipped:          { label: '已跳过',   variant: 'outline',  tip: '已跳过' },
    charge_blocked:   { label: '充电专属', variant: 'warning',  tip: '充电专属视频，暂不支持下载' },
    deleted:          { label: '已删除',   variant: 'outline',  tip: '已从列表删除' },
  };
  const s = map[status] || { label: status || '未知', variant: 'outline', tip: '' };
  // 纯 CSS tooltip：桌面端 hover 显示，移动端（sm 以下）隐藏
  return h('span', { className: 'relative group/badge inline-flex' },
    h(Badge, { variant: s.variant }, s.label),
    s.tip && h('span', {
      className: 'pointer-events-none absolute bottom-full left-1/2 -translate-x-1/2 mb-1.5 whitespace-nowrap rounded-md bg-white border border-slate-200 text-slate-700 text-[11px] px-2 py-1 shadow-lg opacity-0 group-hover/badge:opacity-100 transition-opacity duration-150 z-50 hidden sm:block'
    }, s.tip)
  );
}

export function Card({ children, className = '', hover = false, onClick }) {
  return h('div', {
    onClick,
    className: cn('bg-white border border-slate-200 rounded-xl p-5 shadow-sm', hover && 'card-hover cursor-pointer', className)
  }, children);
}

export function Button({ children, onClick, variant = 'primary', size = 'md', disabled = false, className = '' }) {
  const variants = {
    primary: 'bg-blue-600 hover:bg-blue-700 text-white',
    secondary: 'bg-slate-100 hover:bg-slate-200 text-slate-700 border border-slate-300',
    danger: 'bg-red-600 hover:bg-red-700 text-white',
    ghost: 'hover:bg-slate-100 text-slate-600',
    outline: 'border border-slate-300 hover:bg-slate-100 text-slate-600',
  };
  const sizes = { sm: 'px-3 py-1.5 text-xs', md: 'px-4 py-2 text-sm', lg: 'px-6 py-3' };
  return h('button', {
    onClick, disabled,
    className: cn('inline-flex items-center justify-center gap-2 rounded-lg font-medium transition-colors whitespace-nowrap', variants[variant], sizes[size], disabled && 'opacity-50 cursor-not-allowed', className),
  }, children);
}

export function Skeleton({ className = '' }) {
  return h('div', { className: cn('rounded-lg h-4 bg-slate-200 animate-pulse', className) });
}

// 精细化 Skeleton：视频卡片（带封面图占位 + 标题 + 状态行）
export function VideoCardSkeleton() {
  return h('div', { className: 'bg-white border border-slate-200 rounded-xl overflow-hidden' },
    // 封面图占位（aspect-video）
    h('div', { className: 'bg-slate-200 animate-pulse rounded w-full aspect-video' }),
    h('div', { className: 'p-4 space-y-2.5' },
      // 标题行
      h('div', { className: 'bg-slate-200 animate-pulse rounded h-4 w-full' }),
      h('div', { className: 'bg-slate-200 animate-pulse rounded h-3 w-2/3' }),
      // 状态 badge + 大小
      h('div', { className: 'flex items-center gap-2 mt-1' },
        h('div', { className: 'bg-slate-200 animate-pulse rounded-full h-5 w-14' }),
        h('div', { className: 'bg-slate-200 animate-pulse rounded h-3 w-12' }),
      )
    )
  );
}

// 精细化 Skeleton：订阅源卡片（标题 + badge + 4列统计 + 底部信息）
export function SourceCardSkeleton() {
  return h('div', { className: 'bg-white border border-slate-200 rounded-xl p-5 space-y-3' },
    // 顶部：标题 + 操作按钮
    h('div', { className: 'flex items-start justify-between' },
      h('div', { className: 'flex-1 space-y-2 mr-3' },
        h('div', { className: 'bg-slate-200 animate-pulse rounded h-4 w-3/4' }),
        h('div', { className: 'bg-slate-200 animate-pulse rounded-full h-5 w-16' }),
      ),
      h('div', { className: 'flex gap-1' },
        h('div', { className: 'bg-slate-200 animate-pulse rounded w-7 h-7' }),
        h('div', { className: 'bg-slate-200 animate-pulse rounded w-7 h-7' }),
      )
    ),
    // 4列统计
    h('div', { className: 'grid grid-cols-4 gap-2' },
      ...[0,1,2,3].map(i => h('div', { key: i, className: 'text-center space-y-1' },
        h('div', { className: 'bg-slate-200 animate-pulse rounded h-6 w-8 mx-auto' }),
        h('div', { className: 'bg-slate-200 animate-pulse rounded h-3 w-8 mx-auto' }),
      ))
    ),
    // 底部信息行
    h('div', { className: 'pt-2 border-t border-slate-200 flex items-center gap-2' },
      h('div', { className: 'bg-slate-200 animate-pulse rounded h-3 w-20' }),
      h('div', { className: 'bg-slate-200 animate-pulse rounded h-3 w-16' }),
    )
  );
}

// 精细化 Skeleton：UP主卡片（头像 + 名字 + 3列统计）
export function UploaderCardSkeleton() {
  return h('div', { className: 'bg-white border border-slate-200 rounded-xl p-5 space-y-3' },
    // 头像 + 名字
    h('div', { className: 'flex items-center gap-3' },
      h('div', { className: 'bg-slate-200 animate-pulse rounded-full w-10 h-10 flex-shrink-0' }),
      h('div', { className: 'flex-1 space-y-1.5' },
        h('div', { className: 'bg-slate-200 animate-pulse rounded h-4 w-3/4' }),
        h('div', { className: 'bg-slate-200 animate-pulse rounded h-3 w-1/2' }),
      )
    ),
    // 3列统计
    h('div', { className: 'grid grid-cols-3 gap-2' },
      ...[0,1,2].map(i => h('div', { key: i, className: 'text-center space-y-1' },
        h('div', { className: 'bg-slate-200 animate-pulse rounded h-6 w-8 mx-auto' }),
        h('div', { className: 'bg-slate-200 animate-pulse rounded h-3 w-8 mx-auto' }),
      ))
    )
  );
}

// 精细化 Skeleton：仪表盘统计卡片（标签 + 大数字）
export function DashboardStatSkeleton() {
  return h('div', { className: 'bg-white border border-slate-200 rounded-xl p-5 space-y-3' },
    h('div', { className: 'bg-slate-200 animate-pulse rounded h-3 w-12' }),
    h('div', { className: 'bg-slate-200 animate-pulse rounded h-8 w-16' }),
  );
}

// 精细化 Skeleton：设置分区卡片（标题 + 多个表单行）
export function SettingsSectionSkeleton() {
  return h('div', { className: 'bg-white border border-slate-200 rounded-xl p-5 space-y-4' },
    h('div', { className: 'bg-slate-200 animate-pulse rounded h-4 w-24 mb-2' }),
    ...[0,1,2].map(i => h('div', { key: i, className: 'flex items-center justify-between py-2 border-b border-slate-200 last:border-0' },
      h('div', { className: 'space-y-1.5 flex-1 mr-4' },
        h('div', { className: 'bg-slate-200 animate-pulse rounded h-3.5 w-28' }),
        h('div', { className: 'bg-slate-200 animate-pulse rounded h-3 w-48' }),
      ),
      h('div', { className: 'bg-slate-200 animate-pulse rounded-lg h-8 w-40' }),
    ))
  );
}

export function EmptyState({ icon = 'video', message = '暂无数据', action }) {
  return h('div', { className: 'flex flex-col items-center justify-center py-16 text-slate-400' },
    h(Icon, { name: icon, size: 48, className: 'mb-4 opacity-30' }),
    h('p', { className: 'text-lg mb-4' }, message),
    action && action.label && h('button', {
      onClick: action.onClick,
      className: 'mt-2 px-4 py-1.5 rounded-lg text-sm bg-slate-100 hover:bg-slate-200 text-slate-600 transition-colors',
    }, action.label)
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
      h('button', { onClick: () => onChange(page - 1), disabled: page <= 1, className: 'p-2 rounded-lg hover:bg-slate-100 disabled:opacity-30 text-slate-500' }, h(Icon, { name: 'chevron-left', size: 16 })),
      pages.map(p => h('button', { key: p, onClick: () => onChange(p), className: cn('w-8 h-8 rounded-lg text-sm', p === page ? 'bg-blue-600 text-white' : 'hover:bg-slate-100 text-slate-500') }, p)),
      h('button', { onClick: () => onChange(page + 1), disabled: page >= totalPages, className: 'p-2 rounded-lg hover:bg-slate-100 disabled:opacity-30 text-slate-500' }, h(Icon, { name: 'chevron-right', size: 16 }))
    )
  );
}
