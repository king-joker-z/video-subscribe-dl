import React from 'react';
import { api, createLogSocket, createEventSource } from '../api.js';
import { cn, toast, Icon, Card, Button, Badge } from '../components/utils.js';
const { createElement: h, useState, useEffect, useRef, useCallback } = React;

export function LogsPage() {
  const [logs, setLogs] = useState([]);
  const [filter, setFilter] = useState('all');
  const [autoScroll, setAutoScroll] = useState(true);
  const [unreadCount, setUnreadCount] = useState(0); // 非追随状态下累积的未读新日志数
  const [connType, setConnType] = useState(''); // 'ws' | 'sse'
  const autoScrollRef = useRef(true); // ref 副本，供 onLog 闭包读取
  const containerRef = useRef(null);
  const connectionRef = useRef(null);
  const skipHistoryRef = useRef(false);
  const reconnectKey = useRef(0); // 用于强制重连

  // 连接管理：WebSocket 优先，SSE 降级（复用 api.js 导出的工厂函数）
  const connect = useCallback(() => {
    // 关闭旧连接
    if (connectionRef.current) {
      connectionRef.current.close();
      connectionRef.current = null;
    }

    const onLog = (entry) => {
      setLogs(prev => [...prev.slice(-999), entry]);
      if (!autoScrollRef.current) {
        setUnreadCount(prev => prev + 1);
      }
    };

    // 使用 api.js 导出的 createLogSocket（WebSocket 优先）
    const sock = createLogSocket(onLog, (type) => {
      setConnType('ws');
      console.log('[logs] WebSocket 已连接');
    });

    if (sock.ws) {
      const origOnError = sock.ws.onerror;
      sock.ws.onerror = () => {
        if (origOnError) origOnError();
        // WebSocket 失败，降级到 SSE
        fallbackToSSE();
      };
      sock.ws.onclose = () => {
        setConnType('');
        setTimeout(() => connect(), 5000);
      };
    }

    connectionRef.current = sock;
  }, []);

  const fallbackToSSE = useCallback(() => {
    // 使用全局 SSE 单例的 vsd:log 事件，不新建 EventSource
    const onLog = (entry) => {
      setLogs(prev => [...prev.slice(-999), entry]);
      if (!autoScrollRef.current) {
        setUnreadCount(prev => prev + 1);
      }
    };
    const handler = (e) => { onLog(e.detail); };
    window.addEventListener('vsd:log', handler);
    setConnType('sse');
    connectionRef.current = { close: () => window.removeEventListener('vsd:log', handler), type: 'sse' };
  }, []);

  // 加载历史日志 + 建立连接
  useEffect(() => {
    api.getLogs(200).then(res => {
      setLogs(res.data || []);
      // 历史日志加载完后滚到底部
      requestAnimationFrame(() => {
        if (containerRef.current) {
          containerRef.current.scrollTop = containerRef.current.scrollHeight;
        }
      });
    }).catch(e => toast.error(e.message));
    
    connect();
    
    return () => {
      if (connectionRef.current) {
        connectionRef.current.close();
      }
    };
  }, [reconnectKey.current]);

  // 自动滚动：延迟一帧确保 DOM/scrollHeight 已更新
  useEffect(() => {
    if (!autoScroll || !containerRef.current) return;
    const el = containerRef.current;
    // requestAnimationFrame 确保浏览器 layout 完成后再滚动
    const raf = requestAnimationFrame(() => {
      el.scrollTop = el.scrollHeight;
    });
    return () => cancelAnimationFrame(raf);
  }, [logs, autoScroll]);

  const handleScroll = () => {
    if (!containerRef.current) return;
    const { scrollTop, scrollHeight, clientHeight } = containerRef.current;
    const atBottom = scrollTop + clientHeight >= scrollHeight - 20;
    if (!atBottom) {
      autoScrollRef.current = false;
      setAutoScroll(false);
    } else {
      autoScrollRef.current = true;
      setAutoScroll(true);
      setUnreadCount(0);
    }
  };

  const scrollToBottom = () => {
    if (containerRef.current) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
    }
    autoScrollRef.current = true;
    setAutoScroll(true);
    setUnreadCount(0);
  };

  const toggleAutoScroll = () => {
    const next = !autoScroll;
    autoScrollRef.current = next;
    setAutoScroll(next);
    if (next) {
      scrollToBottom();
    }
  };

  // 清空日志：调 API 清后端 buffer → 清前端数组 → 断开重连
  const handleClear = async () => {
    try {
      await api.clearLogs();
    } catch (e) {
      // 即使后端 API 失败也清前端
      console.warn('清空后端日志失败:', e);
    }
    setLogs([]);
    // 断开当前连接并重新建立（重连时后端 buffer 已空，只推新日志）
    if (connectionRef.current) {
      connectionRef.current.close();
      connectionRef.current = null;
    }
    skipHistoryRef.current = true;
    setTimeout(() => connect(), 300);
  };

  const filteredLogs = filter === 'all' ? logs : logs.filter(l => {
    const msg = (l.message || l.msg || '').toLowerCase();
    const level = (l.level || '').toLowerCase();
    if (filter === 'error') return level === 'error' || msg.includes('[error]') || msg.includes('error') || msg.includes('fail');
    if (filter === 'warn') return level === 'warn' || msg.includes('[warn]');
    if (filter === 'info') return level === 'info' || msg.includes('[info]');
    return true;
  });

  const levelColors = {
    'ERROR': 'text-red-500',
    'error': 'text-red-500',
    'WARN': 'text-amber-500',
    'warn': 'text-amber-500',
    'INFO': 'text-blue-500',
    'info': 'text-blue-500',
    'DEBUG': 'text-slate-400',
    'debug': 'text-slate-400',
  };

  const filters = [
    { value: 'all', label: 'ALL' },
    { value: 'info', label: 'INFO' },
    { value: 'warn', label: 'WARN' },
    { value: 'error', label: 'ERROR' },
  ];

  return h('div', { className: 'page-enter flex flex-col h-[calc(100vh-8rem)]' },
    // 顶栏
    h('div', { className: 'flex flex-wrap items-center gap-2 mb-3' },
      h('h2', { className: 'text-lg font-semibold' }, '实时日志'),
      h('div', { className: 'flex flex-wrap items-center gap-2 ml-auto' },
        // 连接状态指示
        connType && h('span', { className: cn('px-2 py-0.5 rounded text-[10px] font-medium',
          connType === 'ws' ? 'bg-emerald-100 text-emerald-700' : 'bg-blue-100 text-blue-600')
        }, connType === 'ws' ? 'WS' : 'SSE'),
        h('div', { className: 'flex gap-1' },
          filters.map(f => h('button', {
            key: f.value,
            onClick: () => setFilter(f.value),
            className: cn('px-3 py-1 rounded text-xs font-medium', filter === f.value ? 'bg-blue-100 text-blue-600' : 'text-slate-500 hover:text-slate-700')
          }, f.label))
        ),
        h('button', {
          onClick: toggleAutoScroll,
          title: autoScroll ? '点击暂停自动追随' : '点击追随最新日志',
          className: cn(
            'relative px-3 py-1 rounded text-xs font-medium transition-colors',
            autoScroll ? 'bg-emerald-100 text-emerald-700' : 'bg-slate-100 text-slate-500 hover:text-slate-700'
          )
        },
          autoScroll ? '⏬ 追随' : '⏸ 暂停',
          // 未读计数气泡
          !autoScroll && unreadCount > 0 && h('span', {
            className: 'absolute -top-1.5 -right-1.5 min-w-[18px] h-[18px] flex items-center justify-center rounded-full bg-red-500 text-white text-[10px] font-bold px-1'
          }, unreadCount > 99 ? '99+' : String(unreadCount))
        ),
        h('button', {
          onClick: handleClear,
          className: 'px-3 py-1 rounded text-xs text-slate-500 hover:text-red-500 transition-colors'
        }, '🗑 清空')
      )
    ),
    // 日志容器（relative wrapper for floating button）
    h('div', { className: 'flex-1 relative' },
      h('div', {
        ref: containerRef,
        onScroll: handleScroll,
        className: 'h-full bg-slate-50 border border-slate-200 rounded-xl p-4 overflow-y-auto font-mono text-xs leading-5'
      },
        filteredLogs.length === 0
          ? h('div', { className: 'text-slate-400 text-center py-8' }, '等待日志...')
          : filteredLogs.map((l, i) => {
              const time = l.time ? new Date(l.time).toLocaleTimeString('zh-CN') : '';
              const level = l.level || '';
              const msg = l.message || l.msg || JSON.stringify(l);
              return h('div', { key: i, className: 'flex gap-2 hover:bg-slate-100 py-0.5 px-1 rounded' },
                time && h('span', { className: 'text-slate-400 flex-shrink-0' }, time),
                level && h('span', { className: cn('flex-shrink-0 w-12', levelColors[level] || 'text-slate-500') }, level),
                h('span', { className: 'text-slate-700 break-all' }, msg)
              );
            })
      ),
      // 浮动"回到底部 + 恢复追随"按钮（仅非追随状态时显示）
      !autoScroll && h('button', {
        onClick: scrollToBottom,
        title: '滚到底部并恢复追随最新日志',
        className: 'absolute bottom-4 right-6 flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-blue-600 text-white text-xs font-medium shadow-lg hover:bg-blue-700 transition-colors'
      },
        '↓ 跳到最新',
        unreadCount > 0 && h('span', {
          className: 'bg-white/20 rounded px-1 tabular-nums'
        }, '+' + (unreadCount > 99 ? '99+' : unreadCount))
      )
    )
  );
}
