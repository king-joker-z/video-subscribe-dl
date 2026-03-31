import React from 'react';
import { api, createLogSocket, createEventSource } from '../api.js';
import { cn, toast, Icon, Card, Button, Badge } from '../components/utils.js';
const { createElement: h, useState, useEffect, useRef, useCallback, useMemo } = React;

export function LogsPage() {
  const [logs, setLogs] = useState([]);
  const [filter, setFilter] = useState('all');
  // 反转模式：最新日志在顶部
  // autoScroll=true 时新日志自动出现在顶部（容器 scrollTop 保持 0）
  // 用户向下滚动时停止自动追随，显示"跳到最新"浮钮
  const [autoScroll, setAutoScroll] = useState(true);
  const [unreadCount, setUnreadCount] = useState(0);
  const [connType, setConnType] = useState('');
  const autoScrollRef = useRef(true);
  const bufferRef = useRef([]); // [FIXED: buffer for paused mode]
  const containerRef = useRef(null);
  const connectionRef = useRef(null);
  const alreadyFallenBack = useRef(false); // P0-5: 防止 ws.onclose 和 onerror 双连
  const [reconnectKey, setReconnectKey] = useState(0);

  // 连接管理：WebSocket 优先，SSE 降级
  const connect = useCallback(() => {
    if (connectionRef.current) {
      connectionRef.current.close();
      connectionRef.current = null;
    }

    const onLog = (entry) => {
      // [FIXED: buffer in pause mode, prepend in follow mode]
      if (autoScrollRef.current) {
        setLogs(prev => [entry, ...prev.slice(0, 999)]);
      } else {
        bufferRef.current = [entry, ...bufferRef.current];
        setUnreadCount(bufferRef.current.length);
      }
    };

    const sock = createLogSocket(onLog, (type) => {
      setConnType('ws');
    });

    if (sock.ws) {
      alreadyFallenBack.current = false;
      const origOnError = sock.ws.onerror;
      sock.ws.onerror = () => {
        if (origOnError) origOnError();
        if (!alreadyFallenBack.current) {
          alreadyFallenBack.current = true;
          fallbackToSSE();
        }
      };
      sock.ws.onclose = () => {
        setConnType('');
        // 如果已经因 onerror 降级到 SSE，不再重新发起 WS 连接
        if (!alreadyFallenBack.current) {
          setTimeout(() => connect(), 5000);
        }
      };
    }

    connectionRef.current = sock;
  }, []);

  const fallbackToSSE = useCallback(() => {
    const onLog = (entry) => {
      // [FIXED: buffer in pause mode, prepend in follow mode]
      if (autoScrollRef.current) {
        setLogs(prev => [entry, ...prev.slice(0, 999)]);
      } else {
        bufferRef.current = [entry, ...bufferRef.current];
        setUnreadCount(bufferRef.current.length);
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
      const history = res.data || [];
      // 历史日志反转，最新的在顶部
      setLogs([...history].reverse());
    }).catch(e => toast.error(e.message));

    connect();

    return () => {
      if (connectionRef.current) {
        connectionRef.current.close();
      }
    };
  }, [reconnectKey]);

  // [FIXED: 反转模式，只在追随时强制 scrollTop=0，靠 ref 判断避免 autoScroll state 时序问题]
  useEffect(() => {
    if (!autoScrollRef.current || !containerRef.current) return;
    containerRef.current.scrollTop = 0;
  }, [logs]);

  const handleScroll = () => {
    // [FIXED: 用户向下滚时暂停追随，回顶时调用 scrollToTop 合并 buffer]
    if (!containerRef.current) return;
    const { scrollTop } = containerRef.current;
    if (scrollTop > 20) {
      autoScrollRef.current = false;
      setAutoScroll(false);
    } else if (!autoScrollRef.current) {
      // 用户手动滚回顶部，恢复追随并合并 buffer
      scrollToTop();
    }
  };

  const scrollToTop = () => {
    // [FIXED: 合并 buffer 进 logs，清空 buffer，恢复追随]
    if (bufferRef.current.length > 0) {
      setLogs(prev => [...bufferRef.current, ...prev].slice(0, 1000));
      bufferRef.current = [];
    }
    autoScrollRef.current = true;
    setAutoScroll(true);
    setUnreadCount(0);
    if (containerRef.current) containerRef.current.scrollTop = 0;
  };

  const toggleAutoScroll = () => {
    // [FIXED: 恢复追随时调用 scrollToTop，暂停时只更新 ref 和 state]
    const next = !autoScroll;
    autoScrollRef.current = next;
    setAutoScroll(next);
    if (next) scrollToTop();
  };

  const handleClear = async () => {
    try {
      await api.clearLogs();
    } catch (e) {
      console.warn('清空后端日志失败:', e);
    }
    setLogs([]);
    if (connectionRef.current) {
      connectionRef.current.close();
      connectionRef.current = null;
    }
    setTimeout(() => connect(), 300);
  };

  const filteredLogs = useMemo(() => filter === 'all' ? logs : logs.filter(l => {
    const raw = l.message || l.msg || '';
    const level = (l.level || '').toLowerCase();
    if (filter === 'error') return level === 'error' || /\[error\]|\b(error|fail|failed|failure|exception|panic|fatal)\b/i.test(raw);
    if (filter === 'warn') return level === 'warn' || /\[WARN\]/i.test(raw);
    if (filter === 'info') return level === 'info' || /\[info\]/i.test(raw);
    return true;
  }), [logs, filter]);

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
          title: autoScroll ? '点击暂停自动追随最新' : '点击恢复追随最新',
          className: cn(
            'relative px-3 py-1 rounded text-xs font-medium transition-colors',
            autoScroll ? 'bg-emerald-100 text-emerald-700' : 'bg-slate-100 text-slate-500 hover:text-slate-700'
          )
        },
          autoScroll ? '⏫ 追随' : '⏸ 暂停',
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
    // 日志容器
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
              return h('div', { key: `${l.time}-${l.level}-${i}`, className: 'flex gap-2 hover:bg-slate-100 py-0.5 px-1 rounded' },
                time && h('span', { className: 'text-slate-400 flex-shrink-0' }, time),
                level && h('span', { className: cn('flex-shrink-0 w-12', levelColors[level] || 'text-slate-500') }, level),
                h('span', { className: 'text-slate-700 break-all' }, msg)
              );
            })
      ),
      // 浮动"跳到最新"按钮（非追随时显示）
      !autoScroll && h('button', {
        onClick: scrollToTop,
        title: '跳到最新日志并恢复追随',
        className: 'absolute top-4 right-6 flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-blue-600 text-white text-xs font-medium shadow-lg hover:bg-blue-700 transition-colors'
      },
        '↑ 跳到最新',
        unreadCount > 0 && h('span', {
          className: 'bg-white/20 rounded px-1 tabular-nums'
        }, '+' + (unreadCount > 99 ? '99+' : unreadCount))
      )
    )
  );
}
