import React from 'react';
import { api } from '../api.js';
import { cn, Icon } from './utils.js';
const { createElement: h, useState, useEffect, useRef, useCallback, Fragment } = React;

// 页面导航选项
const NAV_ITEMS = [
  { type: 'page', title: '仪表盘', subtitle: '总览数据', route: 'dashboard', icon: 'layout-dashboard' },
  { type: 'page', title: '订阅源', subtitle: '管理订阅', route: 'sources', icon: 'rss' },
  { type: 'page', title: '视频列表', subtitle: '所有视频', route: 'videos', icon: 'video' },
  { type: 'page', title: 'UP 主', subtitle: '按 UP 主浏览', route: 'uploaders', icon: 'users' },
  { type: 'page', title: '设置', subtitle: '系统配置', route: 'settings', icon: 'settings' },
  { type: 'page', title: '实时日志', subtitle: '查看日志', route: 'logs', icon: 'terminal' },
];

// 快捷动作
const ACTION_ITEMS = [
  { type: 'action', title: '快速下载', subtitle: 'Ctrl+D', action: 'quick-download', icon: 'download' },
  { type: 'action', title: '触发同步', subtitle: '立即检查新视频', action: 'trigger-sync', icon: 'play' },
];

// 类型分组标签
const TYPE_LABELS = {
  page: '页面',
  action: '快捷操作',
  video: '视频',
  uploader: 'UP 主',
  source: '订阅源',
};

// 类型排序优先级
const TYPE_ORDER = ['page', 'action', 'source', 'uploader', 'video'];

export function CommandPalette({ open, onClose, onNavigate, onAction }) {
  const [query, setQuery] = useState('');
  const [results, setResults] = useState([]);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [loading, setLoading] = useState(false);
  const inputRef = useRef(null);
  const listRef = useRef(null);
  const searchTimer = useRef(null);

  // 重置状态
  useEffect(() => {
    if (open) {
      setQuery('');
      setResults([]);
      setSelectedIndex(0);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [open]);

  // 计算显示项：静态导航 + 动态搜索结果
  const getDisplayItems = useCallback(() => {
    const q = query.toLowerCase().trim();

    if (!q) {
      // 无搜索词时显示导航和快捷操作
      return [...NAV_ITEMS, ...ACTION_ITEMS];
    }

    // 过滤匹配的导航项
    const matchedNav = NAV_ITEMS.filter(item =>
      item.title.toLowerCase().includes(q) ||
      item.subtitle.toLowerCase().includes(q)
    );

    // 过滤匹配的快捷操作
    const matchedActions = ACTION_ITEMS.filter(item =>
      item.title.toLowerCase().includes(q)
    );

    // 合并静态匹配 + 动态搜索结果
    return [...matchedNav, ...matchedActions, ...results];
  }, [query, results]);

  const displayItems = getDisplayItems();

  // 搜索 API 调用（防抖）
  useEffect(() => {
    if (searchTimer.current) clearTimeout(searchTimer.current);

    const q = query.trim();
    if (!q || q.length < 2) {
      setResults([]);
      setLoading(false);
      return;
    }

    setLoading(true);
    searchTimer.current = setTimeout(async () => {
      try {
        const res = await api.globalSearch(q);
        setResults(res.data || []);
      } catch {
        setResults([]);
      } finally {
        setLoading(false);
      }
    }, 250);

    return () => { if (searchTimer.current) clearTimeout(searchTimer.current); };
  }, [query]);

  // 确保选中项不越界
  useEffect(() => {
    if (selectedIndex >= displayItems.length) {
      setSelectedIndex(Math.max(0, displayItems.length - 1));
    }
  }, [displayItems.length, selectedIndex]);

  // 滚动选中项到可见区域
  useEffect(() => {
    if (listRef.current) {
      const selected = listRef.current.querySelector('[data-selected="true"]');
      if (selected) {
        selected.scrollIntoView({ block: 'nearest' });
      }
    }
  }, [selectedIndex]);

  // 执行选中项
  const executeItem = useCallback((item) => {
    if (!item) return;
    onClose();

    if (item.type === 'page') {
      onNavigate(item.route);
    } else if (item.type === 'action') {
      if (onAction) onAction(item.action);
    } else if (item.route) {
      // 搜索结果 — 导航到对应路由
      const hash = item.route;
      location.hash = hash.startsWith('#') ? hash.slice(1) : '/' + hash;
    }
  }, [onClose, onNavigate, onAction]);

  // 键盘导航
  const handleKeyDown = useCallback((e) => {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        setSelectedIndex(i => Math.min(i + 1, displayItems.length - 1));
        break;
      case 'ArrowUp':
        e.preventDefault();
        setSelectedIndex(i => Math.max(i - 1, 0));
        break;
      case 'Enter':
        e.preventDefault();
        executeItem(displayItems[selectedIndex]);
        break;
      case 'Escape':
        e.preventDefault();
        onClose();
        break;
    }
  }, [displayItems, selectedIndex, executeItem, onClose]);

  if (!open) return null;

  // 按类型分组
  const grouped = {};
  displayItems.forEach((item, idx) => {
    const t = item.type;
    if (!grouped[t]) grouped[t] = [];
    grouped[t].push({ ...item, _idx: idx });
  });

  // 排序分组
  const sortedTypes = TYPE_ORDER.filter(t => grouped[t]);

  const statusColors = {
    completed: 'text-emerald-400',
    downloading: 'text-blue-400',
    pending: 'text-amber-400',
    failed: 'text-red-400',
    charge_blocked: 'text-yellow-500',
  };

  return h('div', {
    className: 'fixed inset-0 z-[100] flex items-start justify-center pt-[15vh]',
    onClick: (e) => { if (e.target === e.currentTarget) onClose(); }
  },
    // 背景遮罩
    h('div', { className: 'fixed inset-0 bg-black/60 backdrop-blur-sm' }),

    // 面板
    h('div', {
      className: 'relative w-full max-w-lg bg-slate-900 border border-slate-700/70 rounded-xl shadow-2xl overflow-hidden',
      style: { animation: 'slideIn 0.15s ease' },
    },
      // 搜索输入
      h('div', { className: 'flex items-center gap-3 px-4 border-b border-slate-700/50' },
        h(Icon, { name: 'search', size: 18, className: 'text-slate-500 flex-shrink-0' }),
        h('input', {
          ref: inputRef,
          type: 'text',
          value: query,
          onChange: (e) => { setQuery(e.target.value); setSelectedIndex(0); },
          onKeyDown: handleKeyDown,
          placeholder: '搜索视频、UP主、订阅源，或跳转页面...',
          className: 'flex-1 bg-transparent border-0 py-3.5 text-sm text-slate-200 placeholder-slate-500 focus:outline-none',
          autoComplete: 'off',
          spellCheck: false,
        }),
        loading && h('div', { className: 'w-4 h-4 border-2 border-slate-600 border-t-blue-400 rounded-full animate-spin' }),
        h('kbd', { className: 'text-[10px] text-slate-600 bg-slate-800 px-1.5 py-0.5 rounded border border-slate-700 flex-shrink-0' }, 'ESC')
      ),

      // 结果列表
      h('div', {
        ref: listRef,
        className: 'max-h-[50vh] overflow-y-auto py-1'
      },
        displayItems.length === 0
          ? h('div', { className: 'px-4 py-8 text-center text-sm text-slate-500' },
              query.length > 0 ? '没有找到匹配结果' : '输入关键词开始搜索'
            )
          : sortedTypes.map(type =>
              h(Fragment, { key: type },
                // 分组标题
                h('div', { className: 'px-4 pt-2 pb-1 text-[11px] font-medium text-slate-600 uppercase tracking-wider' },
                  TYPE_LABELS[type] || type
                ),
                // 分组内的项
                grouped[type].map(item =>
                  h('button', {
                    key: item.type + '-' + (item.title || '') + '-' + (item.route || '') + '-' + (item.action || ''),
                    'data-selected': item._idx === selectedIndex ? 'true' : undefined,
                    onClick: () => executeItem(item),
                    onMouseEnter: () => setSelectedIndex(item._idx),
                    className: cn(
                      'w-full flex items-center gap-3 px-4 py-2.5 text-left transition-colors',
                      item._idx === selectedIndex ? 'bg-blue-500/15 text-blue-300' : 'text-slate-300 hover:bg-slate-800/50'
                    )
                  },
                    // 图标
                    h('div', { className: cn('flex-shrink-0 w-8 h-8 rounded-lg flex items-center justify-center',
                      item._idx === selectedIndex ? 'bg-blue-500/20' : 'bg-slate-800') },
                      h(Icon, { name: item.icon || 'circle', size: 16, className: item._idx === selectedIndex ? 'text-blue-400' : 'text-slate-500' })
                    ),
                    // 标题和副标题
                    h('div', { className: 'flex-1 min-w-0' },
                      h('div', { className: 'text-sm truncate' }, item.title),
                      item.subtitle && h('div', { className: 'text-xs text-slate-500 truncate' }, item.subtitle)
                    ),
                    // 状态标签（视频搜索结果）
                    item.status && h('span', {
                      className: cn('text-xs flex-shrink-0', statusColors[item.status] || 'text-slate-500')
                    }, item.status === 'completed' ? '已完成' : item.status === 'downloading' ? '下载中' : item.status === 'pending' ? '待处理' : item.status === 'failed' ? '失败' : item.status === 'charge_blocked' ? '充电' : item.status),
                    // Enter 提示
                    item._idx === selectedIndex && h('kbd', {
                      className: 'text-[10px] text-slate-600 bg-slate-800 px-1.5 py-0.5 rounded border border-slate-700 flex-shrink-0'
                    }, '↵')
                  )
                )
              )
            )
      ),

      // 底部快捷键提示
      h('div', { className: 'flex items-center gap-4 px-4 py-2 border-t border-slate-700/50 text-[11px] text-slate-600' },
        h('span', { className: 'flex items-center gap-1' },
          h('kbd', { className: 'bg-slate-800 px-1 py-0.5 rounded border border-slate-700' }, '↑↓'),
          ' 导航'
        ),
        h('span', { className: 'flex items-center gap-1' },
          h('kbd', { className: 'bg-slate-800 px-1 py-0.5 rounded border border-slate-700' }, '↵'),
          ' 选择'
        ),
        h('span', { className: 'flex items-center gap-1' },
          h('kbd', { className: 'bg-slate-800 px-1 py-0.5 rounded border border-slate-700' }, 'Esc'),
          ' 关闭'
        )
      )
    )
  );
}
