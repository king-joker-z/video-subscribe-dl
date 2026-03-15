import React from 'react';
import { api } from '../api.js';
import { formatBytes, formatTime, cn, toast, Icon, Card, Button, StatusBadge, Skeleton } from '../components/utils.js';
const { createElement: h, useState, useEffect, useCallback, Fragment } = React;

export function DashboardPage() {
  const [data, setData] = useState(null);
  const [task, setTask] = useState(null);
  const [loading, setLoading] = useState(true);
  const [triggering, setTriggering] = useState(false);

  const load = useCallback(async () => {
    try {
      const [dash, taskRes] = await Promise.all([api.getDashboard(), api.getTaskStatus()]);
      setData(dash.data);
      setTask(taskRes.data);
    } catch (e) { toast.error('加载失败: ' + e.message); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); const t = setInterval(load, 10000); return () => clearInterval(t); }, [load]);

  const handleTrigger = async () => {
    setTriggering(true);
    try { await api.triggerTask(); toast.success('已触发下载任务'); setTimeout(load, 2000); }
    catch (e) { toast.error(e.message); }
    finally { setTriggering(false); }
  };

  if (loading) return h('div', { className: 'page-enter space-y-4' },
    h('div', { className: 'grid grid-cols-2 lg:grid-cols-3 gap-4' },
      Array.from({ length: 6 }, (_, i) => h(Card, { key: i }, h(Skeleton, { className: 'h-16' })))
    )
  );

  const stats = [
    { label: '订阅源', value: data?.sources || 0, color: 'text-blue-400' },
    { label: '视频总数', value: data?.total_videos || 0, color: 'text-slate-200' },
    { label: '已完成', value: data?.completed || 0, color: 'text-emerald-400' },
    { label: '下载中', value: data?.downloading || 0, color: 'text-blue-400' },
    { label: '失败', value: data?.failed || 0, color: 'text-red-400' },
    { label: '待处理', value: data?.pending || 0, color: 'text-amber-400' },
    { label: '充电专属', value: data?.charge_blocked || 0, color: 'text-yellow-500' },
  ];

  return h('div', { className: 'page-enter space-y-6' },
    h('div', { className: 'grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-7 gap-4' },
      stats.map((s, i) => h(Card, { key: i },
        h('div', { className: 'text-xs text-slate-500 mb-1' }, s.label),
        h('div', { className: cn('text-2xl font-bold', s.color) }, s.value.toLocaleString())
      ))
    ),
    h('div', { className: 'grid grid-cols-1 lg:grid-cols-2 gap-4' },
      // 任务状态
      h(Card, null,
        h('div', { className: 'flex items-center justify-between mb-4' },
          h('h3', { className: 'font-medium text-slate-200' }, '任务状态'),
          h(StatusBadge, { status: task?.status === 'running' ? 'downloading' : task?.status === 'paused' ? 'cancelled' : 'completed' })
        ),
        h('div', { className: 'space-y-3 text-sm' },
          h('div', { className: 'flex justify-between text-slate-400' }, h('span', null, '活动下载'), h('span', { className: 'text-slate-200' }, task?.active_downloads || 0)),
          h('div', { className: 'flex justify-between text-slate-400' }, h('span', null, '队列长度'), h('span', { className: 'text-slate-200' }, task?.queue_length || 0)),
          h('div', { className: 'flex justify-between text-slate-400' }, h('span', null, '运行时间'), h('span', { className: 'text-slate-200' }, task?.uptime || '--')),
        ),
        h('div', { className: 'flex gap-2 mt-4' },
          h(Button, { onClick: handleTrigger, disabled: triggering, size: 'sm' },
            h(Icon, { name: 'play', size: 14 }), triggering ? '触发中...' : '立即执行'),
          task?.status === 'paused'
            ? h(Button, { onClick: () => api.resumeTask().then(() => { toast.success('已恢复'); load(); }), variant: 'secondary', size: 'sm' }, h(Icon, { name: 'play', size: 14 }), '恢复')
            : h(Button, { onClick: () => api.pauseTask().then(() => { toast.success('已暂停'); load(); }), variant: 'secondary', size: 'sm' }, h(Icon, { name: 'pause', size: 14 }), '暂停')
        )
      ),
      // 磁盘
      h(Card, null,
        h('div', { className: 'flex items-center gap-2 mb-4' },
          h(Icon, { name: 'hard-drive', size: 18, className: 'text-slate-500' }),
          h('h3', { className: 'font-medium text-slate-200' }, '存储空间')
        ),
        data?.disk ? h(Fragment, null,
          h('div', { className: 'flex items-center justify-between mb-2' },
            h('span', { className: 'text-2xl font-bold' }, formatBytes(data.disk.available)),
            h('span', { className: 'text-sm text-slate-500' }, '共 ' + formatBytes(data.disk.total))
          ),
          h('div', { className: 'w-full bg-slate-700 rounded-full h-2.5 mb-2' },
            h('div', { className: 'bg-blue-500 h-2.5 rounded-full progress-bar', style: { width: ((data.disk.used / data.disk.total) * 100).toFixed(1) + '%' } })
          ),
          h('div', { className: 'text-xs text-slate-500' }, `已用 ${formatBytes(data.disk.used)} (${((data.disk.used / data.disk.total) * 100).toFixed(1)}%)`),
          h('div', { className: 'text-xs text-slate-500 mt-1' }, `已下载文件: ${formatBytes(data.total_size || 0)}`)
        ) : h('div', { className: 'text-slate-500' }, '加载中...')
      )
    ),
    // 最近下载
    data?.recent_downloads?.length > 0 && h(Card, null,
      h('h3', { className: 'font-medium mb-4 text-slate-200' }, '最近下载'),
      h('div', { className: 'space-y-2' },
        data.recent_downloads.slice(0, 8).map(dl =>
          h('div', { key: dl.id, className: 'flex items-center gap-3 py-2 border-b border-slate-700/30 last:border-0' },
            h('div', { className: 'w-16 h-10 rounded bg-slate-700 flex-shrink-0 flex items-center justify-center' },
              h(Icon, { name: 'video', size: 16, className: 'text-slate-600' })
            ),
            h('div', { className: 'flex-1 min-w-0' },
              h('div', { className: 'text-sm truncate' }, dl.title || dl.video_id),
              h('div', { className: 'text-xs text-slate-500' }, dl.uploader || '--')
            ),
            h(StatusBadge, { status: dl.status }),
            h('span', { className: 'text-xs text-slate-500 flex-shrink-0 hidden sm:block' }, formatTime(dl.created_at))
          )
        )
      )
    )
  );
}
