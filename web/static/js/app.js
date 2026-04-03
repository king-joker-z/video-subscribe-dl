import React from 'react';
import { createRoot } from 'react-dom/client';
import { cn, toast, toastListeners, Icon } from './components/utils.js';
import { api } from './api.js';
import { DashboardPage } from './pages/dashboard.js';
import { SourcesPage } from './pages/sources.js';
import { VideosPage } from './pages/videos.js';
import { UploadersPage } from './pages/uploaders.js';
import { SettingsPage } from './pages/settings.js';
import { LogsPage } from './pages/logs.js';
import { QuickDownloadDialog, QuickDownloadFAB, DropZoneOverlay, extractBiliUrl, extractVideoUrl } from './components/quick-download.js';
import { CommandPalette } from './components/command-palette.js';

const { createElement: h, useState, useEffect, useCallback, useRef, useMemo } = React;

// ==================== 全局 SSE 单例 ====================
// 所有组件共享同一个 EventSource，通过自定义 CustomEvent 分发
let globalSSE = null;

function ensureGlobalSSE() {
  if (globalSSE && globalSSE.readyState !== EventSource.CLOSED) return;
  globalSSE = new EventSource('/api/events');

  globalSSE.addEventListener('progress', (e) => {
    try {
      window.dispatchEvent(new CustomEvent('vsd:progress', { detail: JSON.parse(e.data) }));
    } catch {}
  });

  globalSSE.addEventListener('download_event', (e) => {
    try {
      window.dispatchEvent(new CustomEvent('vsd:download-event', { detail: JSON.parse(e.data) }));
    } catch {}
  });

  globalSSE.addEventListener('log', (e) => {
    try {
      window.dispatchEvent(new CustomEvent('vsd:log', { detail: JSON.parse(e.data) }));
    } catch {}
  });

  // [FIXED: P1-2] onerror 里先 close 旧实例再置 null，避免极短窗口内旧实例 readyState 未变 CLOSED
  globalSSE.onerror = () => {
    if (globalSSE) { globalSSE.close(); globalSSE = null; }
    setTimeout(ensureGlobalSSE, 5000);
  };
}

// ==================== Toast 容器 ====================
function ToastContainer() {
  const [toasts, setToasts] = useState([]);
  useEffect(() => {
    const handler = (t) => {
      if (t.remove) {
        setToasts(prev => prev.filter(x => x.id !== t.id));
      } else if (t.exiting) {
        setToasts(prev => prev.map(x => x.id === t.id ? { ...x, exiting: true } : x));
      } else {
        setToasts(prev => [...prev.slice(-4), t]);
      }
    };
    toastListeners.push(handler);
    return () => { const idx = toastListeners.indexOf(handler); if (idx >= 0) toastListeners.splice(idx, 1); };
  }, []);

  const colors = { success: 'bg-emerald-500', error: 'bg-red-500', info: 'bg-blue-500' };
  return h('div', { className: 'fixed top-4 right-4 z-50 space-y-2 pointer-events-none' },
    toasts.map(t => h('div', {
      key: t.id,
      className: cn(
        'px-4 py-3 rounded-lg shadow-lg text-white text-sm max-w-sm pointer-events-auto',
        colors[t.type] || colors.info,
        t.exiting ? 'toast-exit' : 'toast'
      ),
    }, t.message))
  );
}

// ==================== 侧边栏 ====================
function Sidebar({ currentPage, onNavigate, collapsed, onToggle, onSearchClick }) {
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
      'sidebar fixed top-0 left-0 h-full bg-white backdrop-blur border-r border-slate-200 z-40 flex flex-col',
      collapsed ? 'w-16' : 'w-56'
    )
  },
    h('div', { className: cn('flex items-center gap-3 px-4 h-14 border-b border-slate-200', collapsed && 'justify-center') },
      h('div', {
        className: 'w-8 h-8 rounded-lg flex-shrink-0 overflow-hidden',
        style: { background: 'linear-gradient(135deg, #3b82f6 0%, #6366f1 100%)' }
      },
        h('svg', {
          viewBox: '0 0 32 32',
          fill: 'none',
          xmlns: 'http://www.w3.org/2000/svg',
          className: 'w-full h-full'
        },
          h('rect', { x: '4', y: '6', width: '24', height: '15', rx: '2', fill: 'rgba(255,255,255,0.13)' }),
          h('rect', { x: '5', y: '7', width: '22', height: '13', rx: '1.5', fill: 'rgba(0,0,0,0.28)' }),
          h('path', { d: 'M13 10.5 L21.5 13.5 L13 16.5 Z', fill: 'white', opacity: '0.92' }),
          h('rect', { x: '5', y: '22.5', width: '22', height: '2', rx: '1', fill: 'rgba(255,255,255,0.1)' }),
          h('rect', { x: '5', y: '22.5', width: '12', height: '2', rx: '1', fill: 'rgba(255,255,255,0.5)' }),
          h('path', { d: 'M11 27 Q16 25.5 21 27', stroke: 'rgba(255,255,255,0.35)', strokeWidth: '1.2', fill: 'none', strokeLinecap: 'round' }),
          h('circle', { cx: '16', cy: '27.2', r: '0.9', fill: 'rgba(255,255,255,0.5)' })
        )
      ),
      !collapsed && h('div', null,
        h('div', { className: 'font-semibold text-sm text-slate-900 tracking-wide' }, 'Video DL'),
        h('div', { className: 'text-[10px] text-slate-400' }, '订阅下载管理')
      )
    ),
    // Search button
    h('div', { className: 'px-2 pt-2' },
      h('button', {
        onClick: onSearchClick,
        className: cn(
          'w-full flex items-center gap-3 px-3 py-2 rounded-lg text-sm text-slate-400 hover:bg-slate-100 hover:text-slate-600 transition-colors border border-slate-200',
          collapsed && 'justify-center'
        )
      },
        h(Icon, { name: 'search', size: 16 }),
        !collapsed && h('div', { className: 'flex-1 flex items-center justify-between' },
          h('span', { className: 'text-slate-400' }, '搜索...'),
          h('kbd', { className: 'text-[10px] text-slate-500 bg-slate-100 px-1.5 py-0.5 rounded border border-slate-200' }, '⌘K')
        )
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
          currentPage === item.id ? 'bg-blue-500/15 text-blue-600 font-medium' : 'text-slate-600 hover:bg-slate-100 hover:text-slate-900'
        )
      },
        h(Icon, { name: item.icon, size: 18 }),
        !collapsed && h('span', null, item.label)
      ))
    ),
    h('div', { className: 'p-2 border-t border-slate-200' },
      h('button', {
        onClick: onToggle,
        className: cn('w-full flex items-center gap-3 px-3 py-2 rounded-lg text-sm text-slate-400 hover:bg-slate-100 hover:text-slate-600 transition-colors', collapsed && 'justify-center')
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
  // 底部 tab 页面不需要顶部汉堡菜单（tab bar 直接导航）
  const tabPages = ['dashboard', 'sources', 'videos', 'uploaders', 'logs'];
  const isTabPage = tabPages.includes(currentPage);
  return h('header', { className: 'lg:hidden fixed top-0 left-0 right-0 h-14 bg-white backdrop-blur border-b border-slate-200 z-30 flex items-center px-4 gap-3' },
    // 非 tab 页面（logs）显示汉堡菜单，tab 页面显示占位
    !isTabPage
      ? h('button', { onClick: onToggleSidebar, className: 'p-2 -ml-2 rounded-lg hover:bg-slate-100 text-slate-600' },
          h(Icon, { name: 'menu', size: 20 })
        )
      : h('div', { className: 'w-8' }),
    h('span', { className: 'font-medium text-sm' }, labels[currentPage] || '')
  );
}

// ==================== 移动端底部 Tab 导航栏 ====================
function MobileTabBar({ currentPage, onNavigate }) {
  const tabs = [
    { id: 'dashboard', icon: 'layout-dashboard', label: '仪表盘' },
    { id: 'sources',   icon: 'rss',              label: '订阅源' },
    { id: 'videos',    icon: 'video',             label: '视频'   },
    { id: 'uploaders', icon: 'users',             label: 'UP主'   },
    { id: 'logs',      icon: 'terminal',          label: '日志'   },
  ];

  return h('nav', {
    className: 'lg:hidden fixed bottom-0 left-0 right-0 h-14 bg-white backdrop-blur border-t border-slate-200 z-30 flex items-stretch'
  },
    tabs.map(tab =>
      h('button', {
        key: tab.id,
        onClick: () => onNavigate(tab.id),
        className: cn(
          'flex-1 flex flex-col items-center justify-center gap-0.5 text-[10px] transition-colors',
          currentPage === tab.id
            ? 'text-blue-600'
            : 'text-slate-400 hover:text-slate-700'
        )
      },
        h('div', {
          className: cn(
            'flex items-center justify-center w-8 h-6 rounded-lg transition-colors',
            currentPage === tab.id ? 'bg-blue-100' : ''
          )
        },
          h(Icon, { name: tab.icon, size: 18 })
        ),
        h('span', null, tab.label)
      )
    )
  );
}

// ==================== 工具函数 ====================
function formatBytesCompact(bytes) {
  if (!bytes || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i];
}

function formatSpeed(bytesPerSec) {
  if (!bytesPerSec || bytesPerSec <= 0) return '';
  return formatBytesCompact(bytesPerSec) + '/s';
}

function truncate(str, max) {
  if (!str) return '';
  return str.length > max ? str.slice(0, max) + '…' : str;
}

// ==================== 全局下载进度浮动条 ====================
function GlobalDownloadBar({ sidebarCollapsed }) {
  const [progressList, setProgressList] = useState([]); // ProgressInfo[]
  const [visible, setVisible] = useState(false);
  const hideTimer = useRef(null);

  useEffect(() => {
    const handler = (e) => {
      try {
        const list = e.detail || [];
        const active = list.filter(p => p.status !== 'done' && p.status !== 'error');
        setProgressList(active);
        if (active.length > 0) {
          setVisible(true);
          if (hideTimer.current) { clearTimeout(hideTimer.current); hideTimer.current = null; }
        } else {
          if (!hideTimer.current) {
            hideTimer.current = setTimeout(() => {
              setVisible(false);
              setProgressList([]);
              hideTimer.current = null;
            }, 2500);
          }
        }
      } catch {}
    };
    window.addEventListener('vsd:progress', handler);
    return () => {
      window.removeEventListener('vsd:progress', handler);
      if (hideTimer.current) clearTimeout(hideTimer.current);
    };
  }, []);

  if (!visible || progressList.length === 0) return null;

  const primary = progressList[0];
  const count = progressList.length;
  const avgPercent = progressList.reduce((s, p) => s + (p.percent || 0), 0) / count;
  const totalSpeed = progressList.reduce((s, p) => s + (p.speed || 0), 0);

  const phaseLabel = ({
    downloading_video: '视频流',
    downloading_audio: '音频流',
    merging: '合并中',
    video: '视频流',
    audio: '音频流',
    merge: '合并中',
  })[primary.phase || primary.status] || '下载中';

  // 侧边栏宽度偏移（仅桌面端，直接用 CSS class 控制，不注入 <style>）
  const mlClass = sidebarCollapsed ? 'lg:ml-16' : 'lg:ml-56';

  // 手机端 top: 3.5rem（header 下方），桌面端 top: 0（无 header，侧边栏偏移）
  return h('div', {
    className: 'fixed z-20 left-0 right-0 pointer-events-none lg:top-0 top-14'
  },
    h('div', {
      className: cn('pointer-events-auto transition-all duration-200', mlClass),
    },
      h('div', {
        className: 'relative bg-white backdrop-blur border-b border-blue-200 px-4 py-1.5 flex items-center gap-3 text-xs shadow-lg'
      },
        // 旋转动画图标
        h('div', { className: 'flex-shrink-0 w-3.5 h-3.5 text-blue-500 animate-spin' },
          h('svg', { viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: '2.5' },
            h('path', { d: 'M21 12a9 9 0 11-6.219-8.56', strokeLinecap: 'round' })
          )
        ),
        // 计数 badge
        count > 1 && h('span', {
          className: 'flex-shrink-0 bg-blue-100 text-blue-600 px-1.5 py-0.5 rounded text-[10px] font-medium'
        }, `${count} 个`),
        // 标题
        h('span', { className: 'flex-1 min-w-0 text-slate-700 truncate' },
          count > 1 ? `${count} 个视频下载中 · ${truncate(primary.title, 24)}` : truncate(primary.title, 40)
        ),
        // 进度 + 速度
        h('div', { className: 'flex-shrink-0 flex items-center gap-2' },
          totalSpeed > 0 && h('span', { className: 'text-slate-400 hidden sm:inline' }, formatSpeed(totalSpeed)),
          h('span', { className: 'text-slate-500 tabular-nums w-10 text-right' }, `${avgPercent.toFixed(1)}%`),
          h('span', { className: 'text-slate-400 hidden sm:inline text-[10px]' }, phaseLabel)
        ),
        // 关闭按钮
        h('button', {
          onClick: () => { setVisible(false); },
          className: 'flex-shrink-0 ml-1 p-0.5 rounded hover:bg-slate-100 text-slate-400 hover:text-slate-600 transition-colors'
        }, h(Icon, { name: 'x', size: 12 })),
        // 进度条（绝对定位在底部）
        h('div', {
          className: 'absolute bottom-0 left-0 right-0 h-0.5 bg-slate-200'
        },
          h('div', {
            className: 'h-full bg-blue-500 transition-all duration-700',
            style: { width: `${Math.min(100, Math.max(0, avgPercent))}%` }
          })
        )
      )
    )
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
  const [quickDlOpen, setQuickDlOpen] = useState(false);
  const [quickDlUrl, setQuickDlUrl] = useState('');
  const [dropZoneActive, setDropZoneActive] = useState(false);
  const [cmdPaletteOpen, setCmdPaletteOpen] = useState(false);
  const dragCounter = useRef(0);

  // 初始化全局 SSE 单例
  useEffect(() => {
    ensureGlobalSSE();
  }, []);

  // 监听全局下载事件：下载完成/失败时弹 toast 通知
  useEffect(() => {
    const handler = (e) => {
      try {
        const evt = e.detail;
        if (!evt) return;
        if (evt.type === 'completed') {
          const sizeStr = evt.file_size > 0 ? ` (${formatBytesCompact(evt.file_size)})` : '';
          toast.success(`✅ 下载完成: ${truncate(evt.title, 40)}${sizeStr}`);
        } else if (evt.type === 'failed') {
          const errStr = evt.error ? `: ${truncate(evt.error, 60)}` : '';
          toast.error(`❌ 下载失败: ${truncate(evt.title, 40)}${errStr}`);
        }
      } catch {}
    };
    window.addEventListener('vsd:download-event', handler);
    return () => window.removeEventListener('vsd:download-event', handler);
  }, []);

  // 全局快捷键 Ctrl+D / Ctrl+K（合并到单个 useEffect，减少事件监听注册数）
  useEffect(() => {
    const handler = (e) => {
      if (e.ctrlKey || e.metaKey) {
        if (e.key === 'd') {
          e.preventDefault();
          setQuickDlUrl('');
          setQuickDlOpen(o => !o);
        } else if (e.key === 'k') {
          e.preventDefault();
          setCmdPaletteOpen(o => !o);
        }
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, []);

  // 全局粘贴监听：检测到 bilibili 链接自动打开快速下载
  useEffect(() => {
    const handler = (e) => {
      // 忽略输入框内的粘贴
      const tag = e.target.tagName?.toLowerCase();
      if (tag === 'input' || tag === 'textarea' || e.target.isContentEditable) return;
      const text = e.clipboardData?.getData('text/plain') || '';
      const biliUrl = extractVideoUrl(text);
      if (biliUrl) {
        e.preventDefault();
        setQuickDlUrl(biliUrl);
        setQuickDlOpen(true);
      }
    };
    window.addEventListener('paste', handler);
    return () => window.removeEventListener('paste', handler);
  }, []);

  // 全局拖拽监听：拖入 bilibili 链接自动打开快速下载
  useEffect(() => {
    const handleDragEnter = (e) => {
      e.preventDefault();
      dragCounter.current++;
      if (dragCounter.current === 1) setDropZoneActive(true);
    };
    const handleDragOver = (e) => { e.preventDefault(); };
    const handleDragLeave = (e) => {
      e.preventDefault();
      dragCounter.current--;
      if (dragCounter.current <= 0) {
        dragCounter.current = 0;
        setDropZoneActive(false);
      }
    };
    const handleDrop = (e) => {
      e.preventDefault();
      dragCounter.current = 0;
      setDropZoneActive(false);
      const text = e.dataTransfer?.getData('text/plain') || e.dataTransfer?.getData('text/uri-list') || '';
      const biliUrl = extractVideoUrl(text);
      if (biliUrl) {
        setQuickDlUrl(biliUrl);
        setQuickDlOpen(true);
      }
    };
    window.addEventListener('dragenter', handleDragEnter);
    window.addEventListener('dragover', handleDragOver);
    window.addEventListener('dragleave', handleDragLeave);
    window.addEventListener('drop', handleDrop);
    return () => {
      window.removeEventListener('dragenter', handleDragEnter);
      window.removeEventListener('dragover', handleDragOver);
      window.removeEventListener('dragleave', handleDragLeave);
      window.removeEventListener('drop', handleDrop);
    };
  }, []);

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

  // [FIXED: P1-1 round3] 用 useMemo 缓存页面 VNode，避免 App re-render 时因 renderPage() 重新
  // 返回新对象引用导致子页面不必要的 unmount/remount（React diff 依赖引用稳定性判断组件类型）
  const pageNode = useMemo(() => {
    switch (page) {
      case 'dashboard': return h(DashboardPage, { onNavigate: navigate });
      case 'sources': return h(SourcesPage, { onNavigate: navigate });
      case 'videos': return h(VideosPage, { params: hashParams });
      case 'uploaders': return h(UploadersPage, { onNavigate: navigate });
      case 'settings': return h(SettingsPage);
      case 'logs': return h(LogsPage);
      default: return h(DashboardPage, { onNavigate: navigate });
    }
  }, [page, hashParams, navigate]);

  return h('div', { className: 'min-h-screen bg-slate-50 text-slate-900' },
    h(ToastContainer),
    h(QuickDownloadDialog, { open: quickDlOpen, onClose: () => { setQuickDlOpen(false); setQuickDlUrl(''); }, initialUrl: quickDlUrl }),
    h(QuickDownloadFAB, { onClick: () => { setQuickDlUrl(''); setQuickDlOpen(true); } }),
    h(DropZoneOverlay, { active: dropZoneActive }),
    h(CommandPalette, {
      open: cmdPaletteOpen,
      onClose: () => setCmdPaletteOpen(false),
      onNavigate: navigate,
      onAction: (action) => {
        if (action === 'quick-download') { setQuickDlUrl(''); setQuickDlOpen(true); }
        else if (action === 'trigger-sync') { api.triggerTask().then(() => toast.success('同步已触发')).catch(e => toast.error(e.message)); }
      }
    }),
    // 侧边栏（PC）
    h('div', { className: 'hidden lg:block' },
      h(Sidebar, { currentPage: page, onNavigate: navigate, collapsed: sidebarCollapsed, onToggle: () => setSidebarCollapsed(c => !c), onSearchClick: () => setCmdPaletteOpen(true) })
    ),
    // 侧边栏（移动端遮罩）
    mobileSidebar && h('div', { className: 'lg:hidden fixed inset-0 bg-black/50 z-30', onClick: () => setMobileSidebar(false) }),
    mobileSidebar && h('div', { className: 'lg:hidden' },
      h(Sidebar, { currentPage: page, onNavigate: navigate, collapsed: false, onToggle: () => setMobileSidebar(false), onSearchClick: () => setCmdPaletteOpen(true) })
    ),
    // 全局下载进度浮动条（有活跃下载时显示在顶部）
    h(GlobalDownloadBar, { sidebarCollapsed }),
    // 移动端头部（仅日志页等非 tab 页面使用汉堡菜单）
    h(MobileHeader, { currentPage: page, onToggleSidebar: () => setMobileSidebar(s => !s) }),
    // 移动端底部 tab 导航栏
    h(MobileTabBar, { currentPage: page, onNavigate: navigate }),
    // 主内容
    h('main', {
      className: cn(
        'transition-all duration-200 pt-14 lg:pt-0 bg-slate-50',
        sidebarCollapsed ? 'lg:ml-16' : 'lg:ml-56'
      )
    },
      h('div', { className: 'p-4 lg:p-6 max-w-7xl pb-16 lg:pb-0' }, pageNode)
    )
  );
}

// 挂载
const root = createRoot(document.getElementById('root'));
root.render(h(App));
