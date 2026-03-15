import React from 'react';
import { createRoot } from 'react-dom/client';
import { cn, toast, toastListeners, Icon } from './components/utils.js';
import { DashboardPage } from './pages/dashboard.js';
import { SourcesPage } from './pages/sources.js';
import { VideosPage } from './pages/videos.js';
import { UploadersPage } from './pages/uploaders.js';
import { SettingsPage } from './pages/settings.js';
import { LogsPage } from './pages/logs.js';

const { createElement: h, useState, useEffect, useCallback } = React;

// ==================== Toast 容器 ====================
function ToastContainer() {
  const [toasts, setToasts] = useState([]);
  useEffect(() => {
    const handler = (t) => {
      if (t.remove) setToasts(prev => prev.filter(x => x.id !== t.id));
      else setToasts(prev => [...prev.slice(-4), t]);
    };
    toastListeners.push(handler);
    return () => { const idx = toastListeners.indexOf(handler); if (idx >= 0) toastListeners.splice(idx, 1); };
  }, []);

  const colors = { success: 'bg-emerald-600', error: 'bg-red-600', info: 'bg-blue-500' };
  return h('div', { className: 'fixed top-4 right-4 z-50 space-y-2' },
    toasts.map(t => h('div', {
      key: t.id,
      className: cn('px-4 py-3 rounded-lg shadow-lg text-white text-sm max-w-sm', colors[t.type] || colors.info),
      style: { animation: 'slideIn 0.3s ease' }
    }, t.message))
  );
}

// ==================== 侧边栏 ====================
function Sidebar({ currentPage, onNavigate, collapsed, onToggle }) {
  const nav = [
    { id: 'dashboard', icon: 'layout-dashboard', label: '仪表盘' },
    { id: 'sources', icon: 'rss', label: '订阅源' },
    { id: 'videos', icon: 'video', label: '视频列表' },
    { id: 'uploaders', icon: 'users', label: 'UP 主' },
    { id: 'settings', icon: 'settings', label: '设置' },
    { id: 'logs', icon: 'terminal', label: '实时日志' },
  ];

  return h('aside', {
    className: cn(
      'sidebar fixed top-0 left-0 h-full bg-slate-900/95 backdrop-blur border-r border-slate-700/50 z-40 flex flex-col',
      collapsed ? 'w-16' : 'w-56'
    )
  },
    h('div', { className: cn('flex items-center gap-3 px-4 h-14 border-b border-slate-700/50', collapsed && 'justify-center') },
      h('div', { className: 'w-8 h-8 rounded-lg bg-blue-500 flex items-center justify-center text-white font-bold text-sm flex-shrink-0' }, 'V'),
      !collapsed && h('div', null,
        h('div', { className: 'font-semibold text-sm text-slate-200' }, 'Video DL'),
        h('div', { className: 'text-[10px] text-slate-500' }, '订阅下载管理')
      )
    ),
    h('nav', { className: 'flex-1 p-2 space-y-0.5' },
      nav.map(item => h('button', {
        key: item.id,
        onClick: () => onNavigate(item.id),
        title: collapsed ? item.label : undefined,
        className: cn(
          'w-full flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-all',
          collapsed && 'justify-center',
          currentPage === item.id ? 'bg-blue-500/15 text-blue-400 font-medium' : 'text-slate-400 hover:bg-slate-800/80 hover:text-slate-200'
        )
      },
        h(Icon, { name: item.icon, size: 18 }),
        !collapsed && h('span', null, item.label)
      ))
    ),
    h('div', { className: 'p-2 border-t border-slate-700/50' },
      h('button', {
        onClick: onToggle,
        className: cn('w-full flex items-center gap-3 px-3 py-2 rounded-lg text-sm text-slate-500 hover:bg-slate-800/80 hover:text-slate-300 transition-colors', collapsed && 'justify-center')
      },
        h(Icon, { name: collapsed ? 'chevron-right' : 'chevron-left', size: 18 }),
        !collapsed && h('span', null, '收起')
      )
    )
  );
}

// ==================== 移动端头部 ====================
function MobileHeader({ currentPage, onToggleSidebar }) {
  const labels = {
    dashboard: '仪表盘', sources: '订阅源', videos: '视频列表',
    uploaders: 'UP 主', settings: '设置', logs: '实时日志',
  };
  return h('header', { className: 'lg:hidden fixed top-0 left-0 right-0 h-14 bg-slate-900/95 backdrop-blur border-b border-slate-700/50 z-30 flex items-center px-4 gap-3' },
    h('button', { onClick: onToggleSidebar, className: 'p-2 -ml-2 rounded-lg hover:bg-slate-800 text-slate-400' },
      h(Icon, { name: 'menu', size: 20 })
    ),
    h('span', { className: 'font-medium text-sm' }, labels[currentPage] || '')
  );
}

// ==================== 主应用 ====================
function App() {
  // Auth removed

  const [page, setPage] = useState(() => {
    const hash = location.hash.slice(2) || 'dashboard';
    const qIdx = hash.indexOf('?');
    return ((qIdx === -1 ? hash : hash.slice(0, qIdx)).split('/')[0]) || 'dashboard';
  });
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [mobileSidebar, setMobileSidebar] = useState(false);

  // Hash 路由
  useEffect(() => {
    const handler = () => {
      const hash = location.hash.slice(2) || 'dashboard';
      const qIdx = hash.indexOf('?');
      setPage((qIdx === -1 ? hash : hash.slice(0, qIdx)).split('/')[0] || 'dashboard');
      setHashParams(qIdx === -1 ? {} : Object.fromEntries(new URLSearchParams(hash.slice(qIdx + 1))));
    };
    window.addEventListener('hashchange', handler);
    return () => window.removeEventListener('hashchange', handler);
  }, []);

  // URL 参数解析
  const getHashParams = () => {
    const hash = location.hash.slice(2) || '';
    const qIdx = hash.indexOf('?');
    if (qIdx === -1) return {};
    return Object.fromEntries(new URLSearchParams(hash.slice(qIdx + 1)));
  };

  const [hashParams, setHashParams] = useState(getHashParams);

  const navigate = useCallback((target, params) => {
    let hash = '#/' + target;
    if (params) hash += '?' + new URLSearchParams(params).toString();
    location.hash = hash;
    setPage(target);
    setHashParams(params || {});
    setMobileSidebar(false);
  }, []);

  const renderPage = () => {
    switch (page) {
      case 'dashboard': return h(DashboardPage);
      case 'sources': return h(SourcesPage, { onNavigate: navigate });
      case 'videos': return h(VideosPage, { params: hashParams });
      case 'uploaders': return h(UploadersPage, { onNavigate: navigate });
      case 'settings': return h(SettingsPage);
      case 'logs': return h(LogsPage);
      default: return h(DashboardPage);
    }
  };

  return h('div', { className: 'min-h-screen bg-slate-950 text-slate-100' },
    h(ToastContainer),
    // 侧边栏（PC）
    h('div', { className: 'hidden lg:block' },
      h(Sidebar, { currentPage: page, onNavigate: navigate, collapsed: sidebarCollapsed, onToggle: () => setSidebarCollapsed(c => !c) })
    ),
    // 侧边栏（移动端遮罩）
    mobileSidebar && h('div', { className: 'lg:hidden fixed inset-0 bg-black/50 z-30', onClick: () => setMobileSidebar(false) }),
    mobileSidebar && h('div', { className: 'lg:hidden' },
      h(Sidebar, { currentPage: page, onNavigate: navigate, collapsed: false, onToggle: () => setMobileSidebar(false) })
    ),
    // 移动端头部
    h(MobileHeader, { currentPage: page, onToggleSidebar: () => setMobileSidebar(s => !s) }),
    // 主内容
    h('main', {
      className: cn(
        'transition-all duration-200 pt-14 lg:pt-0',
        sidebarCollapsed ? 'lg:ml-16' : 'lg:ml-56'
      )
    },
      h('div', { className: 'p-4 lg:p-6 max-w-7xl' }, renderPage())
    )
  );
}

// 挂载
const root = createRoot(document.getElementById('root'));
root.render(h(App));
