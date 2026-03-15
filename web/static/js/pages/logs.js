import React from 'react';
import { api } from '../api.js';
import { cn, toast, Icon, Card, Button, Badge } from '../components/utils.js';
const { createElement: h, useState, useEffect, useRef, useCallback } = React;

export function LogsPage() {
  const [logs, setLogs] = useState([]);
  const [filter, setFilter] = useState('all');
  const [autoScroll, setAutoScroll] = useState(true);
  const [connType, setConnType] = useState(''); // 'ws' | 'sse'
  const containerRef = useRef(null);
  const connectionRef = useRef(null);
  const skipHistoryRef = useRef(false);
  const reconnectKey = useRef(0); // 用于强制重连

  // 连接管理：WebSocket 优先，SSE 降级
  const connect = useCallback(() => {
    // 关闭旧连接
    if (connectionRef.current) {
      connectionRef.current.close();
      connectionRef.current = null;
    }

    // 尝试 WebSocket
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${proto}//${location.host}/api/ws/logs`;
    
    try {
      const finalWsUrl = skipHistoryRef.current ? wsUrl + (wsUrl.includes('?') ? '&' : '?') + 'no_history=1' : wsUrl;
      skipHistoryRef.current = false;
      const ws = new WebSocket(finalWsUrl);
      let wsConnected = false;
      
      ws.onopen = () => {
        wsConnected = true;
        setConnType('ws');
        console.log('[logs] WebSocket 已连接');
      };
      
      ws.onmessage = (e) => {
        try {
          const entry = JSON.parse(e.data);
          setLogs(prev => [...prev.slice(-999), entry]);
        } catch {}
      };
      
      ws.onerror = () => {
        if (!wsConnected) {
          // WebSocket 连接失败，降级到 SSE
          ws.close();
          fallbackToSSE();
        }
      };
      
      ws.onclose = () => {
        if (wsConnected) {
          setConnType('');
          // 5 秒后自动重连
          setTimeout(() => connect(), 5000);
        }
      };
      
      connectionRef.current = {
        close: () => ws.close(),
        type: 'ws',
      };
    } catch {
      fallbackToSSE();
    }
  }, []);

  const fallbackToSSE = useCallback(() => {
    try {
      const es = new EventSource('/api/events');
      es.addEventListener('log', (e) => {
        try {
          const entry = JSON.parse(e.data);
          setLogs(prev => [...prev.slice(-999), entry]);
        } catch {}
      });
      es.addEventListener('connected', () => {
        setConnType('sse');
        console.log('[logs] SSE 已连接');
      });
      es.onerror = () => {
        setConnType('');
        es.close();
        setTimeout(() => connect(), 5000);
      };
      connectionRef.current = {
        close: () => es.close(),
        type: 'sse',
      };
    } catch {}
  }, []);

  // 加载历史日志 + 建立连接
  useEffect(() => {
    api.getLogs(200).then(res => {
      setLogs(res.data || []);
    }).catch(e => toast.error(e.message));
    
    connect();
    
    return () => {
      if (connectionRef.current) {
        connectionRef.current.close();
      }
    };
  }, [reconnectKey.current]);

  // 自动滚动
  useEffect(() => {
    if (autoScroll && containerRef.current) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
    }
  }, [logs, autoScroll]);

  const handleScroll = () => {
    if (!containerRef.current) return;
    const { scrollTop, scrollHeight, clientHeight } = containerRef.current;
    setAutoScroll(scrollHeight - scrollTop - clientHeight < 50);
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
    if (filter === 'error') return l.level === 'ERROR' || l.level === 'error' || msg.includes('error') || msg.includes('fail');
    if (filter === 'warn') return l.level === 'WARN' || l.level === 'warn' || msg.includes('warn');
    return true;
  });

  const levelColors = {
    'ERROR': 'text-red-400',
    'error': 'text-red-400',
    'WARN': 'text-amber-400',
    'warn': 'text-amber-400',
    'INFO': 'text-blue-400',
    'info': 'text-blue-400',
    'DEBUG': 'text-slate-500',
    'debug': 'text-slate-500',
  };

  const filters = [
    { value: 'all', label: '全部' },
    { value: 'error', label: 'ERROR' },
    { value: 'warn', label: 'WARN' },
  ];

  return h('div', { className: 'page-enter flex flex-col h-[calc(100vh-8rem)]' },
    // 顶栏
    h('div', { className: 'flex items-center justify-between mb-3' },
      h('h2', { className: 'text-lg font-semibold' }, '实时日志'),
      h('div', { className: 'flex items-center gap-3' },
        // 连接状态指示
        connType && h('span', { className: cn('px-2 py-0.5 rounded text-[10px] font-medium',
          connType === 'ws' ? 'bg-emerald-500/20 text-emerald-400' : 'bg-blue-500/20 text-blue-400')
        }, connType === 'ws' ? 'WS' : 'SSE'),
        h('div', { className: 'flex gap-1' },
          filters.map(f => h('button', {
            key: f.value,
            onClick: () => setFilter(f.value),
            className: cn('px-3 py-1 rounded text-xs font-medium', filter === f.value ? 'bg-blue-500/20 text-blue-400' : 'text-slate-500 hover:text-slate-300')
          }, f.label))
        ),
        h('button', {
          onClick: () => setAutoScroll(!autoScroll),
          className: cn('px-3 py-1 rounded text-xs font-medium', autoScroll ? 'bg-emerald-500/20 text-emerald-400' : 'text-slate-500')
        }, autoScroll ? '⏬ 自动滚动' : '⏸ 已暂停'),
        h('button', {
          onClick: handleClear,
          className: 'px-3 py-1 rounded text-xs text-slate-500 hover:text-red-400 transition-colors'
        }, '🗑 清空')
      )
    ),
    // 日志容器
    h('div', {
      ref: containerRef,
      onScroll: handleScroll,
      className: 'flex-1 bg-slate-900/80 border border-slate-700/50 rounded-xl p-4 overflow-y-auto font-mono text-xs leading-5'
    },
      filteredLogs.length === 0
        ? h('div', { className: 'text-slate-600 text-center py-8' }, '等待日志...')
        : filteredLogs.map((l, i) => {
            const time = l.time ? new Date(l.time).toLocaleTimeString('zh-CN') : '';
            const level = l.level || '';
            const msg = l.message || l.msg || JSON.stringify(l);
            return h('div', { key: i, className: 'flex gap-2 hover:bg-slate-800/50 py-0.5 px-1 rounded' },
              time && h('span', { className: 'text-slate-600 flex-shrink-0' }, time),
              level && h('span', { className: cn('flex-shrink-0 w-12', levelColors[level] || 'text-slate-400') }, level),
              h('span', { className: 'text-slate-300 break-all' }, msg)
            );
          })
    )
  );
}
