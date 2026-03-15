import React from 'react';
import { api } from '../api.js';
import { formatBytes, formatTime, cn, toast, Icon, Card, Button, StatusBadge, Skeleton } from '../components/utils.js';
const { createElement: h, useState, useEffect, useCallback, useRef, Fragment } = React;

export function DashboardPage({ onNavigate }) {
  const [data, setData] = useState(null);
  const [task, setTask] = useState(null);
  const [loading, setLoading] = useState(true);
  const [triggering, setTriggering] = useState(false);
  const [cooldownSec, setCooldownSec] = useState(0);
  const cooldownRef = useRef(null);
  const [refreshingCred, setRefreshingCred] = useState(false);

  const load = useCallback(async () => {
    try {
      const [dash, taskRes] = await Promise.all([api.getDashboard(), api.getTaskStatus()]);
      setData(dash.data);
      setTask(taskRes.data);
      // 同步风控冷却倒计时
      if (dash.data?.cooldown?.active) {
        setCooldownSec(dash.data.cooldown.remaining_sec || 0);
      } else {
        setCooldownSec(0);
      }
    } catch (e) { toast.error('加载失败: ' + e.message); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); const t = setInterval(load, 10000); return () => clearInterval(t); }, [load]);

  // 监听全局下载事件，立即刷新仪表盘
  useEffect(() => {
    const handler = () => { setTimeout(load, 500); };
    window.addEventListener('vsd:download-event', handler);
    return () => window.removeEventListener('vsd:download-event', handler);
  }, [load]);

  // 风控冷却倒计时
  useEffect(() => {
    if (cooldownRef.current) clearInterval(cooldownRef.current);
    if (cooldownSec > 0) {
      cooldownRef.current = setInterval(() => {
        setCooldownSec(prev => {
          if (prev <= 1) { clearInterval(cooldownRef.current); return 0; }
          return prev - 1;
        });
      }, 1000);
    }
    return () => { if (cooldownRef.current) clearInterval(cooldownRef.current); };
  }, [cooldownSec]);

  const handleTrigger = async () => {
    setTriggering(true);
    try { await api.triggerTask(); toast.success('已触发下载任务'); setTimeout(load, 2000); }
    catch (e) { toast.error(e.message); }
    finally { setTriggering(false); }
  };

  const handleRefreshCred = async () => {
    setRefreshingCred(true);
    try {
      await api.refreshCredential();
      toast.success('凭证刷新成功');
      setTimeout(load, 1000);
    } catch (e) { toast.error('刷新失败: ' + e.message); }
    finally { setRefreshingCred(false); }
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
    { label: '24h 下载', value: data?.downloads_24h || 0, color: 'text-cyan-400' },
  ];

  // 运行状态
  const getRunStatus = () => {
    if (cooldownSec > 0) return { label: '风控冷却', color: 'text-orange-400', badge: 'cancelled' };
    if (task?.status === 'running') return { label: '运行中', color: 'text-emerald-400', badge: 'downloading' };
    if (task?.status === 'paused') return { label: '已暂停', color: 'text-amber-400', badge: 'cancelled' };
    return { label: '空闲', color: 'text-slate-400', badge: 'completed' };
  };
  const runStatus = getRunStatus();

  // 状态分布数据（用于纯 CSS 柱状图）
  const total = data?.total_videos || 0;
  const statusDist = [
    { label: '已完成', value: data?.completed || 0, color: '#34d399' },
    { label: '下载中', value: data?.downloading || 0, color: '#60a5fa' },
    { label: '待处理', value: data?.pending || 0, color: '#fbbf24' },
    { label: '失败', value: data?.failed || 0, color: '#f87171' },
    { label: '充电专属', value: data?.charge_blocked || 0, color: '#eab308' },
  ].filter(s => s.value > 0);

  const formatCooldown = (sec) => {
    const m = Math.floor(sec / 60);
    const s = sec % 60;
    return m > 0 ? `${m}分${s}秒` : `${s}秒`;
  };

  // 凭证状态辅助
  const cred = data?.credential || {};
  const getCredStatusStyle = () => {
    switch (cred.status) {
      case 'ok': return { icon: '✅', color: 'text-emerald-400', bg: 'bg-emerald-500/10', border: 'border-emerald-500/30' };
      case 'expired': return { icon: '⚠️', color: 'text-amber-400', bg: 'bg-amber-500/10', border: 'border-amber-500/30' };
      case 'error': return { icon: '❌', color: 'text-red-400', bg: 'bg-red-500/10', border: 'border-red-500/30' };
      default: return { icon: '🔑', color: 'text-slate-400', bg: 'bg-slate-500/10', border: 'border-slate-500/30' };
    }
  };
  const credStyle = getCredStatusStyle();

  return h('div', { className: 'page-enter space-y-6' },
    // 风控冷却横幅
    cooldownSec > 0 && h('div', { className: 'bg-orange-500/10 border border-orange-500/30 rounded-xl p-4 flex items-center gap-3' },
      h('div', { className: 'text-orange-400 text-xl' }, '⚠️'),
      h('div', { className: 'flex-1' },
        h('div', { className: 'text-orange-300 font-medium' }, 'B站风控冷却中'),
        h('div', { className: 'text-orange-400/70 text-sm' }, '下载已自动暂停，冷却结束后恢复')
      ),
      h('div', { className: 'text-2xl font-mono font-bold text-orange-300' }, formatCooldown(cooldownSec))
    ),

    // 凭证过期/未登录警告横幅
    (cred.status === 'expired' || cred.status === 'none') && h('div', { className: cn('rounded-xl p-4 flex items-center gap-3 border', credStyle.bg, credStyle.border) },
      h('div', { className: cn('text-xl', credStyle.color) }, credStyle.icon),
      h('div', { className: 'flex-1' },
        h('div', { className: cn('font-medium', credStyle.color) }, cred.status === 'expired' ? 'B站凭证已过期' : '未登录 B 站'),
        h('div', { className: cn('text-sm opacity-70', credStyle.color) }, cred.status === 'expired' ? '下载功能受限，请刷新凭证或重新登录' : '登录后可下载更高画质视频')
      ),
      cred.status === 'expired' && cred.has_credential && h(Button, {
        onClick: handleRefreshCred, disabled: refreshingCred, size: 'sm'
      }, refreshingCred ? '刷新中...' : '刷新凭证'),
      onNavigate && h(Button, {
        onClick: () => onNavigate('settings'), size: 'sm', variant: 'secondary'
      }, cred.status === 'expired' ? '重新登录' : '去登录')
    ),

    // 统计卡片网格
    h('div', { className: 'grid grid-cols-2 sm:grid-cols-4 lg:grid-cols-4 xl:grid-cols-8 gap-4' },
      stats.map((s, i) => h(Card, { key: i },
        h('div', { className: 'text-xs text-slate-500 mb-1' }, s.label),
        h('div', { className: cn('text-2xl font-bold', s.color) }, s.value.toLocaleString())
      ))
    ),

    h('div', { className: 'grid grid-cols-1 lg:grid-cols-3 gap-4' },
      // 任务状态
      h(Card, null,
        h('div', { className: 'flex items-center justify-between mb-4' },
          h('h3', { className: 'font-medium text-slate-200' }, '任务状态'),
          h('div', { className: 'flex items-center gap-2' },
            h('span', { className: cn('text-sm font-medium', runStatus.color) }, runStatus.label),
            h(StatusBadge, { status: runStatus.badge })
          )
        ),
        h('div', { className: 'space-y-3 text-sm' },
          h('div', { className: 'flex justify-between text-slate-400' }, h('span', null, '活动下载'), h('span', { className: 'text-slate-200' }, task?.active_downloads || 0)),
          h('div', { className: 'flex justify-between text-slate-400' }, h('span', null, '队列长度'), h('span', { className: 'text-slate-200' }, task?.queue_length || 0)),
          h('div', { className: 'flex justify-between text-slate-400' }, h('span', null, '运行时间'), h('span', { className: 'text-slate-200' }, task?.uptime || '--')),
        ),
        h('div', { className: 'flex gap-2 mt-4' },
          h(Button, { onClick: handleTrigger, disabled: triggering || cooldownSec > 0, size: 'sm' },
            h(Icon, { name: 'play', size: 14 }), triggering ? '触发中...' : '立即执行'),
          task?.status === 'paused'
            ? h(Button, { onClick: () => api.resumeTask().then(() => { toast.success('已恢复'); load(); }), variant: 'secondary', size: 'sm' }, h(Icon, { name: 'play', size: 14 }), '恢复')
            : h(Button, { onClick: () => api.pauseTask().then(() => { toast.success('已暂停'); load(); }), variant: 'secondary', size: 'sm' }, h(Icon, { name: 'pause', size: 14 }), '暂停')
        )
      ),

      // B站账号状态
      h(Card, null,
        h('div', { className: 'flex items-center gap-2 mb-4' },
          h('div', { className: 'text-lg' }, credStyle.icon),
          h('h3', { className: 'font-medium text-slate-200' }, 'B站账号')
        ),
        cred.status === 'ok' ? h('div', { className: 'space-y-3 text-sm' },
          h('div', { className: 'flex justify-between text-slate-400' }, h('span', null, '用户名'), h('span', { className: 'text-slate-200 font-medium' }, cred.username || '--')),
          h('div', { className: 'flex justify-between text-slate-400' }, h('span', null, '会员'), h('span', { className: cn('font-medium', cred.vip_active ? 'text-pink-400' : 'text-slate-200') }, cred.vip_label || '普通用户')),
          h('div', { className: 'flex justify-between text-slate-400' }, h('span', null, '最高画质'), h('span', { className: 'text-slate-200' }, cred.max_quality || '--')),
          h('div', { className: 'flex justify-between text-slate-400' }, h('span', null, '状态'), h('span', { className: 'text-emerald-400' }, '正常')),
          cred.updated_at && h('div', { className: 'flex justify-between text-slate-400' },
            h('span', null, '更新时间'), h('span', { className: 'text-slate-500 text-xs' }, cred.updated_at)
          ),
          h('div', { className: 'flex gap-2 mt-3' },
            h(Button, { onClick: handleRefreshCred, disabled: refreshingCred, size: 'sm', variant: 'secondary' },
              h(Icon, { name: 'sync', size: 14 }), refreshingCred ? '刷新中...' : '刷新凭证')
          )
        ) : cred.status === 'expired' ? h('div', { className: 'space-y-3' },
          h('div', { className: 'text-amber-400 text-sm' }, '凭证已过期，请刷新或重新登录'),
          cred.username && h('div', { className: 'text-sm text-slate-400' }, '上次登录: ' + cred.username),
          h('div', { className: 'flex gap-2 mt-3' },
            h(Button, { onClick: handleRefreshCred, disabled: refreshingCred, size: 'sm' },
              refreshingCred ? '刷新中...' : '刷新凭证'),
            onNavigate && h(Button, { onClick: () => onNavigate('settings'), size: 'sm', variant: 'secondary' }, '重新登录')
          )
        ) : cred.status === 'error' ? h('div', { className: 'space-y-3' },
          h('div', { className: 'text-red-400 text-sm' }, '凭证验证失败'),
          h('div', { className: 'text-xs text-slate-500' }, '可能是网络问题，稍后会自动重试'),
          h('div', { className: 'flex gap-2 mt-3' },
            onNavigate && h(Button, { onClick: () => onNavigate('settings'), size: 'sm', variant: 'secondary' }, '前往设置')
          )
        ) : h('div', { className: 'space-y-3' },
          h('div', { className: 'text-slate-400 text-sm' }, '未登录，仅能下载 480P 视频'),
          h('div', { className: 'text-xs text-slate-500' }, '扫码登录后可下载高画质视频'),
          h('div', { className: 'flex gap-2 mt-3' },
            onNavigate && h(Button, { onClick: () => onNavigate('settings'), size: 'sm' },
              h(Icon, { name: 'log-in', size: 14 }), '前往登录')
          )
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

    // 状态分布（纯 CSS 柱状图）
    total > 0 && statusDist.length > 0 && h(Card, null,
      h('h3', { className: 'font-medium mb-4 text-slate-200' }, '视频状态分布'),
      // 堆叠条形图
      h('div', { className: 'w-full h-6 rounded-full overflow-hidden flex mb-4', style: { backgroundColor: '#334155' } },
        statusDist.map((s, i) =>
          h('div', {
            key: i,
            style: {
              width: (s.value / total * 100).toFixed(1) + '%',
              backgroundColor: s.color,
              minWidth: s.value > 0 ? '2px' : '0',
              transition: 'width 0.6s ease'
            },
            title: `${s.label}: ${s.value} (${(s.value / total * 100).toFixed(1)}%)`
          })
        )
      ),
      // 图例
      h('div', { className: 'flex flex-wrap gap-4 text-sm' },
        statusDist.map((s, i) =>
          h('div', { key: i, className: 'flex items-center gap-2' },
            h('div', { style: { width: 12, height: 12, borderRadius: 3, backgroundColor: s.color } }),
            h('span', { className: 'text-slate-400' }, `${s.label} `),
            h('span', { className: 'text-slate-200 font-medium' }, s.value.toLocaleString()),
            h('span', { className: 'text-slate-500 text-xs ml-1' }, `(${(s.value / total * 100).toFixed(1)}%)`)
          )
        )
      ),

      // 按月下载趋势（纯 CSS 柱状图）
      data?.by_month?.length > 0 && h(Fragment, null,
        h('div', { className: 'border-t border-slate-700/50 mt-4 pt-4' }),
        h('h4', { className: 'text-sm font-medium text-slate-400 mb-3' }, '月度下载趋势'),
        h('div', { className: 'flex items-end gap-1', style: { height: 100 } },
          (() => {
            const maxCount = Math.max(...data.by_month.map(m => m.count), 1);
            return data.by_month.map((m, i) =>
              h('div', { key: i, className: 'flex-1 flex flex-col items-center gap-1' },
                h('span', { className: 'text-xs text-slate-500' }, m.count),
                h('div', {
                  style: {
                    width: '100%',
                    maxWidth: 40,
                    height: Math.max(4, (m.count / maxCount) * 80),
                    backgroundColor: '#60a5fa',
                    borderRadius: '3px 3px 0 0',
                    transition: 'height 0.6s ease'
                  },
                  title: `${m.month}: ${m.count} 个视频`
                }),
                h('span', { className: 'text-xs text-slate-600 mt-1' }, m.month.slice(5))
              )
            );
          })()
        )
      )
    ),

    // 最近下载
    data?.recent_downloads?.length > 0 && h(Card, null,
      h('h3', { className: 'font-medium mb-4 text-slate-200' }, '最近下载'),
      h('div', { className: 'space-y-2' },
        data.recent_downloads.slice(0, 8).map(dl =>
          h('div', { key: dl.id, className: 'flex items-center gap-3 py-2 border-b border-slate-700/30 last:border-0' },
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
