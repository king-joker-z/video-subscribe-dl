import React from 'react';
import { api } from '../api.js';
import { cn, formatBytes, formatSpeed, formatETA, formatTime, toast, Icon, Card, Button, StatusBadge, Pagination, EmptyState, VideoCardSkeleton, ConfirmDialog } from '../components/utils.js';
import { VideoDetailModal } from '../components/video-detail.js';
const { createElement: h, useState, useEffect, useCallback, useRef } = React;

// 检测是否手机端（<= 768px）
function isMobileViewport() {
  return typeof window !== 'undefined' && window.innerWidth <= 768;
}

export function VideosPage({ params = {} } = {}) {
  const [videos, setVideos] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize] = useState(20);
  const [status, setStatus] = useState('');
  const [search, setSearch] = useState('');
  const [uploader, setUploader] = useState(params.uploader || '');
  const [sourceId, setSourceId] = useState(params.source_id || '');
  const [sourceName, setSourceName] = useState(params.source_name || '');
  const [sort, setSort] = useState('created_desc');
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState(new Set());
  // 手机端默认卡片视图，桌面端默认表格视图
  const [viewMode, setViewMode] = useState(() => isMobileViewport() ? 'card' : 'table');
  const [isMobile, setIsMobile] = useState(() => isMobileViewport());
  const [filterExpanded, setFilterExpanded] = useState(() => !isMobileViewport());
  const [detailVideo, setDetailVideo] = useState(null);
  const [searchInput, setSearchInput] = useState(''); // 受控搜索框值
  const [confirmAction, setConfirmAction] = useState(null); // { action, title, message }
  const searchTimer = useRef(null);
  // [FIXED: P2-11] 用 ref 保存最新 load 引用，visibilitychange handler 通过 ref 调用，避免 stale closure
  const loadRef = useRef(null);
  const [progress, setProgress] = useState([]);
  const [batchLoading, setBatchLoading] = useState(false);

  // 监听窗口 resize，更新 isMobile 状态
  useEffect(() => {
    const handleResize = () => {
      const mobile = isMobileViewport();
      setIsMobile(mobile);
      // 切到移动端时强制卡片视图，切回桌面不强制改变
      if (mobile) setViewMode('card');
    };
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api.getVideos({ page, page_size: pageSize, status, search, sort, uploader, source_id: sourceId });
      const d = res.data || {};
      setVideos(d.items || []);
      setTotal(d.total || 0);
    } catch (e) { toast.error(e.message); }
    finally { setLoading(false); }
  }, [page, pageSize, status, search, sort, uploader, sourceId]);

  // [FIXED: P2-11] 同步 load 最新引用到 ref，供 visibilitychange handler 使用
  useEffect(() => { loadRef.current = load; }, [load]);

  useEffect(() => { load(); }, [load]);

  // [FIXED: P1-6] 合并 uploader/source_id 同步为单个 useEffect，避免同时变化时双重 setPage(1)
  useEffect(() => {
    let changed = false;
    if (params.uploader !== undefined) {
      setUploader(params.uploader || '');
      changed = true;
    }
    if (params.source_id !== undefined) {
      setSourceId(params.source_id || '');
      setSourceName(params.source_name || '');
      changed = true;
    }
    if (changed) setPage(1);
  }, [params.uploader, params.source_id, params.source_name]);

  // SSE 进度（通过全局单例）
  useEffect(() => {
    const handler = (e) => { try { setProgress(e.detail || []); } catch {} };
    window.addEventListener('vsd:progress', handler);
    return () => window.removeEventListener('vsd:progress', handler);
  }, []);

  // 监听全局下载事件：started/completed/failed 局部更新状态，其他事件触发完整刷新
  useEffect(() => {
    const handler = (e) => {
      const evt = e.detail;
      if (!evt || !evt.bvid) { setTimeout(load, 500); return; }
      if (evt.type === 'started') {
        setVideos(prev => prev.map(v => v.video_id === evt.bvid ? { ...v, status: 'downloading' } : v));
      } else if (evt.type === 'completed' || evt.type === 'failed') {
        const newStatus = evt.type === 'completed' ? 'completed' : 'failed';
        setVideos(prev => prev.map(v => {
          if (v.video_id !== evt.bvid) return v;
          const patch = { status: newStatus, error_message: evt.error || v.error_message };
          if (evt.type === 'completed') {
            if (evt.file_size) patch.file_size = evt.file_size;
            if (evt.downloaded_at) patch.downloaded_at = evt.downloaded_at;
          }
          return { ...v, ...patch };
        }));
        // [FIXED: P1-5] 完成/失败后延迟刷新，同步 file_path、detail_status 等后端字段
        setTimeout(load, 1000);
      } else {
        setTimeout(load, 500);
      }
    };
    window.addEventListener('vsd:download-event', handler);
    return () => window.removeEventListener('vsd:download-event', handler);
  }, [load]);

  // 定时自动刷新（30s），页面不可见时暂停，切回来立即刷一次
  useEffect(() => {
    const INTERVAL = 30000; // 从 15000 改为 30000
    let timer = null;

    const schedule = () => {
      timer = setTimeout(() => {
        if (!document.hidden) {
          load();
        }
        schedule();
      }, INTERVAL);
    };

    // [FIXED: P2-11] 通过 loadRef 调用最新 load，避免 stale closure 问题
    const handleVisibility = () => {
      if (!document.hidden) {
        if (loadRef.current) loadRef.current();
      }
    };

    schedule();
    document.addEventListener('visibilitychange', handleVisibility);
    return () => {
      if (timer) clearTimeout(timer);
      document.removeEventListener('visibilitychange', handleVisibility);
    };
  }, [load]);

  const handleSearch = (value) => {
    setSearchInput(value);
    if (searchTimer.current) clearTimeout(searchTimer.current);
    searchTimer.current = setTimeout(() => { setSearch(value); setPage(1); }, 300);
  };

  const batchConfirmMessages = {
    redownload: { title: '重新下载', message: '将删除旧文件并重新下载，确认？' },
    delete:     { title: '批量删除', message: '确定批量删除选中的视频？' },
    restore:    { title: '批量恢复', message: '恢复选中视频并重新下载？' },
    delete_files: { title: '删除文件', message: '将删除选中视频的本地文件（不删数据库记录），确认？' },
  };

  const handleBatch = (action) => {
    if (selected.size === 0) return;
    const needConfirm = batchConfirmMessages[action];
    if (needConfirm) {
      setConfirmAction({ action, ...needConfirm });
      return;
    }
    executeBatch(action);
  };

  const executeBatch = async (action) => {
    setConfirmAction(null);
    setBatchLoading(true);
    const labels = { retry: '重试', cancel: '取消', delete: '删除', redownload: '重新下载', delete_files: '删除文件', restore: '恢复' };
    try {
      const res = await api.batchVideos(action, Array.from(selected));
      const count = res?.data?.affected ?? selected.size;
      toast.success(`批量${labels[action] || action}成功，共 ${count} 项`);
      setSelected(new Set()); load();
    } catch (err) { toast.error(err.message); }
    finally { setBatchLoading(false); }
  };

  const toggleSelect = (id) => {
    const s = new Set(selected);
    s.has(id) ? s.delete(id) : s.add(id);
    setSelected(s);
  };

  const toggleAll = () => {
    if (selected.size === videos.length) setSelected(new Set());
    else setSelected(new Set(videos.map(v => v.id)));
  };

  const statusFilters = [
    { value: '', label: '全部' },
    { value: 'downloading', label: '下载中' },
    { value: 'completed', label: '已完成' },
    { value: 'failed', label: '失败' },
    { value: 'charge_blocked', label: '充电专属' },
    { value: 'pending', label: '待处理' },
    { value: 'skipped', label: '已跳过' },
    { value: 'cancelled', label: '已取消' },
    { value: 'relocated', label: '已迁移' },
    { value: 'deleted', label: '已删除' },
  ];

  const getProgress = (videoId, videoVid) => progress.find(p =>
    (p.download_id && String(p.download_id) === String(videoId)) ||
    (videoVid && p.bvid && p.bvid === videoVid) ||
    (videoVid && p.video_id && p.video_id === videoVid)
  );

  const handleDetectCharge = async () => {
    try {
      const res = await api.detectCharge();
      toast.success(res.data.message || '已启动充电检测');
    } catch (e) { toast.error(e.message); }
  };

  const [repairLoading, setRepairLoading] = useState(false);
  // [FIXED: P2-8] 替换 confirm() 为 ConfirmDialog
  const handleRepairThumbs = () => {
    setConfirmAction({
      title: '补全封面',
      message: '将对所有已完成但缺少封面的视频截帧补全，可能需要较长时间，确认？',
      action: '__repair_thumbs__',
    });
  };
  const executeRepairThumbs = async () => {
    setRepairLoading(true);
    try {
      const res = await api.repairThumbs();
      const d = res.data || {};
      toast.success(`补全完成：成功 ${d.success ?? 0}，跳过 ${d.skipped ?? 0}，失败 ${d.failed ?? 0}，共 ${d.total ?? 0} 条`);
      load();
    } catch (e) { toast.error(e.message); }
    finally { setRepairLoading(false); }
  };

  const handleDownloadAllPending = async () => {
    try {
      if (uploader) {
        const res = await api.downloadPendingByUploader(uploader);
        toast.success(res.data.message || '已提交');
      } else {
        const res = await api.downloadAllPending();
        toast.success(res.data.message || '已触发全部待处理下载');
      }
      setTimeout(load, 1000);
    } catch (e) { toast.error(e.message); }
  };

  // 点击行打开详情（避免与 checkbox/按钮冲突）
  const openDetail = (v, e) => {
    // 如果点击的是 checkbox、button、a 标签则不打开详情
    const tag = e.target.tagName.toLowerCase();
    if (tag === 'input' || tag === 'button' || tag === 'a') return;
    if (e.target.closest('button') || e.target.closest('a') || e.target.closest('input')) return;
    setDetailVideo(v);
  };

  return h('div', { className: 'page-enter space-y-4' },
    // 自定义确认弹窗（批量操作 + 单视频操作 + repair thumbs）
    confirmAction && h(ConfirmDialog, {
      title: confirmAction.title,
      message: confirmAction.message,
      onConfirm: async () => {
        const act = confirmAction.action;
        const vid = confirmAction._videoId;
        setConfirmAction(null);
        if (act === '__repair_thumbs__') { executeRepairThumbs(); return; }
        if (act === '__redownload__') { try { await api.redownloadVideo(vid); toast.success('已提交重新下载'); load(); } catch (e) { toast.error(e.message); } return; }
        if (act === '__delete_files__') { try { await api.deleteVideoFiles(vid); toast.success('文件已删除'); load(); } catch (e) { toast.error(e.message); } return; }
        if (act === '__restore__') { try { await api.restoreVideo(vid); toast.success('已恢复'); load(); } catch (e) { toast.error(e.message); } return; }
        if (act === '__delete__') { try { await api.deleteVideo(vid); toast.success('已删除'); load(); } catch (e) { toast.error(e.message); } return; }
        executeBatch(act);
      },
      onCancel: () => setConfirmAction(null),
    }),
    // 视频详情弹窗
    detailVideo && h(VideoDetailModal, {
      video: detailVideo,
      onClose: () => setDetailVideo(null),
      onAction: () => { load(); }
    }),
    // 顶栏
    h('div', { className: 'flex items-center justify-between flex-wrap gap-3' },
      h('h2', { className: 'text-lg font-semibold' }, '视频列表'),
      h('div', { className: 'flex items-center gap-2' },
        h(Button, {
          onClick: handleDownloadAllPending, variant: 'secondary', size: 'sm',
          title: uploader ? `下载 ${uploader} 的全部待处理视频` : '下载全部待处理视频'
        }, uploader ? '下载该UP主Pending' : '下载全部Pending'),
        h(Button, { onClick: handleDetectCharge, variant: 'secondary', size: 'sm' }, '检测充电'),
        h(Button, { onClick: handleRepairThumbs, variant: 'secondary', size: 'sm', disabled: repairLoading }, repairLoading ? '补全中...' : '补全封面'),
        // 手机端隐藏视图切换（强制卡片视图）
        !isMobile && h('button', { onClick: () => setViewMode('table'), className: cn('p-2 rounded-lg', viewMode === 'table' ? 'bg-slate-200 text-slate-800' : 'text-slate-500') }, h(Icon, { name: 'list', size: 16 })),
        !isMobile && h('button', { onClick: () => setViewMode('card'), className: cn('p-2 rounded-lg', viewMode === 'card' ? 'bg-slate-200 text-slate-800' : 'text-slate-500') }, h(Icon, { name: 'grid', size: 16 })),
      )
    ),
    // 筛选栏（手机端可折叠）
    h('div', { className: 'space-y-2' },
      // 手机端折叠控制行
      isMobile && h('button', {
        onClick: () => setFilterExpanded(v => !v),
        className: 'flex items-center gap-2 text-sm text-slate-500 hover:text-slate-700 transition-colors w-full'
      },
        h(Icon, { name: filterExpanded ? 'chevron-up' : 'chevron-down', size: 14 }),
        h('span', null, filterExpanded ? '收起筛选' : '展开筛选'),
        // 有激活筛选时显示小徽章
        (status || search || uploader || sourceId) && h('span', { className: 'ml-auto text-xs bg-blue-100 text-blue-600 px-1.5 py-0.5 rounded-full' }, '已筛选')
      ),
    (filterExpanded || !isMobile) && h('div', { className: 'flex items-center gap-3 flex-wrap' },
      h('div', { className: 'relative' },
        h(Icon, { name: 'search', size: 16, className: 'absolute left-3 top-1/2 -translate-y-1/2 text-slate-400' }),
        h('input', {
          type: 'text', placeholder: '搜索标题/UP主...',
          value: searchInput,
          onChange: (e) => handleSearch(e.target.value),
          className: 'bg-white border border-slate-300 rounded-lg pl-9 pr-3 py-2 text-sm text-slate-800 placeholder-slate-400 focus:outline-none focus:border-blue-500 w-64'
        })
      ),
      h('div', { className: 'flex gap-1 flex-wrap' },
        statusFilters.map(f =>
          h('button', {
            key: f.value,
            onClick: () => { setStatus(f.value); setPage(1); },
            className: cn('px-2 py-1 sm:px-3 sm:py-1.5 rounded-lg text-xs font-medium transition-colors', status === f.value ? 'bg-blue-500/20 text-blue-600' : 'text-slate-500 hover:text-slate-700')
          }, f.label)
        ),
        uploader && h('button', {
          onClick: () => { setUploader(''); setPage(1); location.hash = '#/videos'; },
          className: 'px-2 py-1 sm:px-3 sm:py-1.5 rounded-lg text-xs font-medium bg-purple-100 text-purple-700 flex items-center gap-1'
        }, 'UP主: ' + uploader, ' ', h(Icon, { name: 'x', size: 12 })),
        sourceId && h('button', {
          onClick: () => { setSourceId(''); setSourceName(''); setPage(1); location.hash = '#/videos'; },
          className: 'px-2 py-1 sm:px-3 sm:py-1.5 rounded-lg text-xs font-medium bg-cyan-100 text-cyan-700 flex items-center gap-1'
        }, '订阅源: ' + (sourceName || '#' + sourceId), ' ', h(Icon, { name: 'x', size: 12 }))
      ),
      h('select', {
        value: sort,
        onChange: (e) => { setSort(e.target.value); setPage(1); },
        className: 'bg-white border border-slate-300 rounded-lg px-3 py-2 text-xs text-slate-700'
      },
        h('option', { value: 'created_desc' }, '最新'),
        h('option', { value: 'created_asc' }, '最早'),
        h('option', { value: 'title_asc' }, '标题 A-Z'),
        h('option', { value: 'size_desc' }, '文件最大'),
        h('option', { value: 'downloaded_desc' }, '最近下载'),
      ),
    )), // 结束 filterExpanded 条件 + 外层 space-y-2 div
    // 批量操作栏
    selected.size > 0 && h('div', { className: 'flex items-center gap-2 flex-wrap bg-blue-50 border border-blue-200 rounded-lg px-3 py-2' },
      h('span', { className: 'text-sm text-blue-600 mr-1' }, batchLoading ? '处理中...' : `已选 ${selected.size} 项`),
      h(Button, { onClick: () => handleBatch('retry'), variant: 'secondary', size: 'sm', disabled: batchLoading, title: '重试', className: 'shrink-0' },
        h(Icon, { name: 'refresh', size: 13 }), h('span', { className: 'hidden sm:inline ml-1' }, '重试')
      ),
      h(Button, { onClick: () => handleBatch('redownload'), variant: 'secondary', size: 'sm', disabled: batchLoading, title: '重新下载', className: 'shrink-0' },
        h(Icon, { name: 'download', size: 13 }), h('span', { className: 'hidden sm:inline ml-1' }, '重下')
      ),
      h(Button, { onClick: () => handleBatch('cancel'), variant: 'secondary', size: 'sm', disabled: batchLoading, title: '取消下载', className: 'shrink-0' },
        h(Icon, { name: 'x', size: 13 }), h('span', { className: 'hidden sm:inline ml-1' }, '取消')
      ),
      h(Button, { onClick: () => handleBatch('delete_files'), variant: 'secondary', size: 'sm', disabled: batchLoading, title: '删除文件', className: 'shrink-0' },
        h(Icon, { name: 'file-x', size: 13 }), h('span', { className: 'hidden sm:inline ml-1' }, '删文件')
      ),
      h(Button, { onClick: () => handleBatch('restore'), variant: 'secondary', size: 'sm', disabled: batchLoading, title: '恢复', className: 'shrink-0' },
        h(Icon, { name: 'undo', size: 13 }), h('span', { className: 'hidden sm:inline ml-1' }, '恢复')
      ),
      h(Button, { onClick: () => handleBatch('delete'), variant: 'danger', size: 'sm', disabled: batchLoading, title: '删除', className: 'shrink-0' },
        h(Icon, { name: 'trash', size: 13 }), h('span', { className: 'hidden sm:inline ml-1' }, '删除')
      ),
      h('button', {
        onClick: toggleAll,
        className: 'text-xs text-blue-600/70 hover:text-blue-700 ml-auto mr-2',
        disabled: batchLoading,
        title: selected.size === videos.length ? '取消全选' : '全选当页'
      },
        selected.size === videos.length ? h(Icon, { name: 'check-square', size: 14 }) : h(Icon, { name: 'square', size: 14 })
      ),
      h('button', { onClick: () => setSelected(new Set()), className: 'text-xs text-slate-500 hover:text-slate-700', disabled: batchLoading }, isMobile ? h(Icon, { name: 'x-circle', size: 14 }) : '清除选择')
    ),
    // 内容
    loading
      ? h('div', { className: 'grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-2 xl:grid-cols-3 gap-4' }, Array.from({ length: 6 }, (_, i) => h(VideoCardSkeleton, { key: i })))
      : videos.length === 0
        ? h(EmptyState, {
            icon: 'video',
            message: (status || search) ? '没有符合筛选条件的视频' : '暂无视频',
            action: (status || search) ? {
              label: '清除筛选',
              onClick: () => {
                setStatus('');
                setSearch('');
                setSearchInput('');
                if (searchTimer.current) clearTimeout(searchTimer.current);
                setPage(1);
              }
            } : undefined
          })
        : viewMode === 'table'
          ? h('div', { className: 'overflow-x-auto' },
              h('table', { className: 'w-full' },
                h('thead', null,
                  h('tr', { className: 'text-left text-xs text-slate-500 border-b border-slate-200' },
                    h('th', { className: 'pb-3 pr-3 w-8' },
                      h('input', { type: 'checkbox', checked: selected.size === videos.length && videos.length > 0, onChange: toggleAll, className: 'rounded' })
                    ),
                    h('th', { className: 'pb-3 pr-3' }, '视频'),
                    h('th', { className: 'pb-3 pr-3 hidden md:table-cell' }, 'UP 主'),
                    h('th', { className: 'pb-3 pr-3' }, '状态'),
                    h('th', { className: 'pb-3 pr-3 hidden lg:table-cell' }, '大小'),
                    h('th', { className: 'pb-3 pr-3 hidden lg:table-cell' }, '时间'),
                    h('th', { className: 'pb-3 w-24' }, '操作'),
                  )
                ),
                h('tbody', null,
                  videos.map(v => {
                    const prog = getProgress(v.id, v.video_id);
                    return h('tr', {
                      key: v.id,
                      className: 'border-b border-slate-200 hover:bg-slate-50 cursor-pointer',
                      onClick: (e) => openDetail(v, e)
                    },
                      h('td', { className: 'py-3 pr-3' },
                        h('input', { type: 'checkbox', checked: selected.has(v.id), onChange: () => toggleSelect(v.id), className: 'rounded' })
                      ),
                      h('td', { className: 'py-3 pr-3' },
                        h('div', { className: 'min-w-0' },
                          h('div', { className: 'text-sm truncate max-w-md' }, v.title || v.video_id),
                          prog && h('div', { className: 'mt-1 space-y-0.5' },
                            h('div', { className: 'flex items-center gap-2' },
                              h('div', { className: 'w-24 bg-slate-200 rounded-full h-1' },
                                h('div', { className: 'bg-blue-500 h-1 rounded-full progress-bar', style: { width: (prog.percent || 0) + '%' } })
                              ),
                              h('span', { className: 'text-[10px] text-slate-500 tabular-nums' }, (prog.percent || 0).toFixed(1) + '%')
                            ),
                            (prog.speed > 0 || prog.total > 0) && h('div', { className: 'flex items-center gap-2 text-[10px] text-slate-500' },
                              prog.speed > 0 && h('span', { className: 'text-blue-500 font-medium' }, formatSpeed(prog.speed)),
                              formatETA(prog.downloaded, prog.total, prog.speed) && h('span', null, 'ETA ' + formatETA(prog.downloaded, prog.total, prog.speed)),
                              prog.total > 0 && h('span', null, formatBytes(prog.downloaded) + '/' + formatBytes(prog.total))
                            )
                          )
                        )
                      ),
                      h('td', { className: 'py-3 pr-3 text-sm text-slate-500 hidden md:table-cell' }, v.uploader || '--'),
                      h('td', { className: 'py-3 pr-3' }, h(StatusBadge, { status: v.status })),
                      h('td', { className: 'py-3 pr-3 text-xs text-slate-500 hidden lg:table-cell' }, v.file_size ? formatBytes(v.file_size) : '--'),
                      h('td', { className: 'py-3 pr-3 text-xs text-slate-500 hidden lg:table-cell' }, formatTime(v.created_at)),
                      h('td', { className: 'py-3' },
                        h('div', { className: 'flex items-center gap-1' },
                          v.status === 'downloading' && h('button', {
                            onClick: async (e) => {
                              e.stopPropagation();
                              try { await api.cancelVideo(v.id); toast.success('已取消'); load(); }
                              catch (err) { toast.error(err.message); }
                            },
                            className: 'p-1.5 rounded hover:bg-amber-50 text-slate-400 hover:text-amber-600', title: '取消下载'
                          }, h(Icon, { name: 'x', size: 14 })),
                          v.status === 'pending' && h('button', {
                            onClick: async (e) => { e.stopPropagation(); try { await api.redownloadVideo(v.id); toast.success('已触发下载'); load(); } catch (err) { toast.error(err.message); } },
                            className: 'p-1.5 rounded hover:bg-green-50 text-slate-400 hover:text-green-600', title: '开始下载'
                          }, h(Icon, { name: 'download', size: 14 })),
                          ((v.status === 'failed' || v.status === 'permanent_failed') && v.status !== 'charge_blocked') && h('button', {
                            onClick: async (e) => { e.stopPropagation(); try { await api.retryVideo(v.id); toast.success('已重试'); load(); } catch (err) { toast.error(err.message); } },
                            className: 'p-1.5 rounded hover:bg-slate-100 text-slate-400', title: '重试'
                          }, h(Icon, { name: 'refresh', size: 14 })),
                          (v.status === 'completed' || v.status === 'relocated') && h('button', {
                            onClick: (e) => { e.stopPropagation(); setConfirmAction({ title: '重新下载', message: '将删除旧文件并重新下载，确认？', action: '__redownload__', _videoId: v.id }); },
                            className: 'p-1.5 rounded hover:bg-blue-50 text-slate-400 hover:text-blue-600', title: '重新下载'
                          }, h(Icon, { name: 'refresh', size: 14 })),
                          (v.status === 'completed' || v.status === 'relocated') && v.file_size > 0 && h('button', {
                            onClick: (e) => { e.stopPropagation(); setConfirmAction({ title: '删除文件', message: '删除本地文件（保留记录）？', action: '__delete_files__', _videoId: v.id }); },
                            className: 'p-1.5 rounded hover:bg-orange-50 text-slate-400 hover:text-orange-600', title: '删除文件'
                          }, h(Icon, { name: 'file-x', size: 14 })),
                          v.status === 'cancelled' && h('button', {
                            onClick: async (e) => { e.stopPropagation(); try { await api.redownloadVideo(v.id); toast.success('已恢复下载'); load(); } catch (err) { toast.error(err.message); } },
                            className: 'p-1.5 rounded hover:bg-green-50 text-slate-400 hover:text-green-600', title: '恢复下载'
                          }, h(Icon, { name: 'download', size: 14 })),
                          v.status === 'deleted' && h('button', {
                            onClick: (e) => { e.stopPropagation(); setConfirmAction({ title: '恢复视频', message: '恢复并重新下载？', action: '__restore__', _videoId: v.id }); },
                            className: 'p-1.5 rounded hover:bg-emerald-50 text-slate-400 hover:text-emerald-600', title: '恢复'
                          }, h(Icon, { name: 'undo', size: 14 })),
                          h('button', {
                            onClick: (e) => { e.stopPropagation(); setConfirmAction({ title: '删除视频', message: '确定删除？', action: '__delete__', _videoId: v.id }); },
                            className: 'p-1.5 rounded hover:bg-red-50 text-slate-400 hover:text-red-500', title: '删除'
                          }, h(Icon, { name: 'trash', size: 14 }))
                        )
                      )
                    );
                  })
                )
              )
            )
          : h('div', { className: 'grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4' },
              videos.map(v => h(VideoCard, {
                key: v.id, video: v,
                progress: getProgress(v.id, v.video_id),
                isMobile,
                selected: selected.has(v.id),
                onSelect: toggleSelect,
                onClick: () => setDetailVideo(v),
                onAction: load,
                onConfirm: setConfirmAction
              }))
            ),
    h(Pagination, { page, pageSize, total, onChange: setPage })
  );
}

// 判断视频平台（根据 video_id 特征）
// [FIXED: P1-6] 移除过宽的第二个正则 /^[a-z0-9]{8,20}$/i，仅保留明确的 ph[0-9a-f]+ 前缀匹配
function detectPlatform(videoId) {
  if (!videoId) return 'unknown';
  if (/^BV[0-9A-Za-z]{10}$/.test(videoId) || /^av\d+$/i.test(videoId)) return 'bilibili';
  if (/^\d{15,20}$/.test(videoId)) return 'douyin';
  if (/^ph[0-9a-f]{6,}$/i.test(videoId)) return 'pornhub';
  return 'unknown';
}

// B 站 logo SVG（内联）
function BilibiliLogo({ size = 40 }) {
  return h('svg', { width: size, height: size, viewBox: '0 0 24 24', fill: 'white', xmlns: 'http://www.w3.org/2000/svg' },
    h('path', { d: 'M17.813 4.653h.854c1.51.054 2.769.578 3.773 1.574 1.004.995 1.524 2.249 1.56 3.76v7.36c-.036 1.51-.556 2.769-1.56 3.773s-2.262 1.524-3.773 1.56H5.333c-1.51-.036-2.769-.556-3.773-1.56S.036 18.858 0 17.347v-7.36c.036-1.511.556-2.765 1.56-3.76 1.004-.996 2.262-1.52 3.773-1.574h.774l-1.174-1.12a1.234 1.234 0 0 1 0-1.733 1.234 1.234 0 0 1 1.706 0l2.134 2.107 2.08-2.08a1.234 1.234 0 0 1 1.706 0 1.234 1.234 0 0 1 0 1.733L11.4 4.707h6.413zm.613 3.199H5.574a.96.96 0 0 0-.96.96v7.893a.96.96 0 0 0 .96.96h12.853a.96.96 0 0 0 .96-.96V8.812a.96.96 0 0 0-.96-.96zm-9.6 1.92a.96.96 0 0 1 .96.96v3.84a.96.96 0 0 1-1.92 0v-3.84a.96.96 0 0 1 .96-.96zm6.4 0a.96.96 0 0 1 .96.96v3.84a.96.96 0 0 1-1.92 0v-3.84a.96.96 0 0 1 .96-.96z' })
  );
}

// 抖音 logo SVG（内联）
function DouyinLogo({ size = 40 }) {
  return h('svg', { width: size, height: size, viewBox: '0 0 24 24', fill: 'white', xmlns: 'http://www.w3.org/2000/svg' },
    h('path', { d: 'M19.59 6.69a4.83 4.83 0 0 1-3.77-4.25V2h-3.45v13.67a2.89 2.89 0 0 1-2.88 2.5 2.89 2.89 0 0 1-2.89-2.89 2.89 2.89 0 0 1 2.89-2.89c.28 0 .54.04.79.1V9.01a6.33 6.33 0 0 0-.79-.05 6.34 6.34 0 0 0-6.34 6.34 6.34 6.34 0 0 0 6.34 6.34 6.34 6.34 0 0 0 6.33-6.34V8.69a8.27 8.27 0 0 0 4.83 1.56V6.79a4.85 4.85 0 0 1-1.06-.1z' })
  );
}

// 视频卡片组件（带封面图）
// [FIXED: P0-2] 卡片视图快捷按钮也使用 ConfirmDialog（通过回调传入父组件）
function VideoCard({ video: v, progress: prog, onClick, isMobile = false, onAction, selected = false, onSelect, onConfirm }) {
  const [imgError, setImgError] = React.useState(false);
  const thumbSrc = `/api/thumb/${v.id}`;
  const isDownloading = v.status === 'downloading';
  const platform = detectPlatform(v.video_id);

  // 缩略图加载失败时显示对应平台 logo
  const renderThumbFallback = () => {
    if (platform === 'bilibili') {
      return h('div', { className: 'w-full h-full flex items-center justify-center', style: { background: '#00AEEC' } },
        h(BilibiliLogo, { size: 48 })
      );
    }
    if (platform === 'douyin') {
      return h('div', { className: 'w-full h-full flex items-center justify-center', style: { background: '#010101' } },
        h(DouyinLogo, { size: 48 })
      );
    }
    if (platform === 'pornhub') {
      return h('div', { className: 'w-full h-full flex items-center justify-center', style: { background: '#1b1b1b' } },
        h('span', { style: { fontSize: 28, lineHeight: 1 } }, '🔞')
      );
    }
    return h('div', { className: 'w-full h-full flex items-center justify-center text-slate-400' },
      h(Icon, { name: 'video', size: 32 })
    );
  };

  return h(Card, {
    hover: true,
    className: cn('group overflow-hidden', selected ? 'ring-2 ring-blue-500 ring-inset' : (isDownloading ? 'border-l-4 border-blue-500' : '')),
    onClick
  },
    // 封面图区域
    h('div', { className: 'relative -mx-5 -mt-5 mb-3 aspect-video bg-slate-100 overflow-hidden' },
      // 选择 checkbox（左上角覆盖层）
      onSelect && h('div', {
        className: 'absolute top-2 left-2 z-10',
        onClick: (e) => { e.stopPropagation(); onSelect(v.id); }
      },
        h('div', {
          className: 'w-6 h-6 rounded-md flex items-center justify-center transition-all ' + (selected ? 'bg-blue-500 shadow-md' : 'bg-black/40 border-2 border-white/60 hover:border-white'),
        },
          selected && h(Icon, { name: 'check', size: 12, className: 'text-white' })
        )
      ),
      !imgError
        ? h('img', {
            src: thumbSrc,
            className: 'w-full h-full object-cover',
            referrerPolicy: 'no-referrer',
            loading: 'lazy',
            onError: () => setImgError(true)
          })
        : renderThumbFallback(),
      // 时长标签
      v.duration > 0 && h('span', { className: 'absolute bottom-2 right-2 bg-black/60 text-white text-xs px-1.5 py-0.5 rounded' },
        formatDurationShort(v.duration)
      ),
      // 进度条 + 速度信息（下载中状态加强视觉）
      prog && h('div', { className: 'absolute bottom-0 left-0 right-0' },
        prog.speed > 0 && h('div', { className: 'flex items-center justify-between px-2 py-0.5 bg-black/70 text-[10px]' },
          h('span', { className: 'text-blue-300 font-semibold' }, formatSpeed(prog.speed)),
          h('span', { className: 'text-white font-medium' }, (prog.percent || 0).toFixed(1) + '%'),
          formatETA(prog.downloaded, prog.total, prog.speed) && h('span', { className: 'text-slate-300' }, formatETA(prog.downloaded, prog.total, prog.speed))
        ),
        h('div', { className: 'h-1.5 bg-black/40' },
          h('div', {
            className: 'h-1.5 progress-bar',
            style: {
              width: (prog.percent || 0) + '%',
              background: prog.total > 0
                ? 'linear-gradient(90deg, #3b82f6 0%, #60a5fa 50%, #93c5fd 100%)'
                : '#3b82f6'
            }
          })
        )
      )
    ),
    // 信息
    h('div', { className: 'min-w-0 overflow-hidden' }, // [FIXED: overflow-hidden 防手机端溢出]
      h('div', { className: 'text-sm font-medium truncate leading-snug' }, v.title || v.video_id),
      h('div', { className: 'text-xs text-slate-500 mt-1 truncate' }, v.uploader || '--'),
      h('div', { className: 'flex items-center gap-2 mt-2' },
        h(StatusBadge, { status: v.status }),
        v.file_size > 0 && h('span', { className: 'text-xs text-slate-500' }, formatBytes(v.file_size))
      ),
      // 手机端快捷操作按钮（触摸区域加大，min-h-9）
      isMobile && h('div', { className: 'flex items-center gap-2 mt-3 pt-2 border-t border-slate-200' },
        v.status === 'downloading' && h('button', {
          onClick: async (e) => { e.stopPropagation(); try { await api.cancelVideo(v.id); if (onAction) onAction(); } catch (err) { toast.error(err.message || '操作失败'); } },
          className: 'flex-1 flex items-center justify-center gap-1.5 min-h-[36px] rounded-lg bg-amber-50 text-amber-700 text-xs font-medium active:bg-amber-100'
        }, h(Icon, { name: 'x', size: 14 }), '取消'),
        v.status === 'pending' && h('button', {
          onClick: async (e) => { e.stopPropagation(); try { await api.redownloadVideo(v.id); if (onAction) onAction(); } catch (err) { toast.error(err.message || '操作失败'); } },
          className: 'flex-1 flex items-center justify-center gap-1.5 min-h-[36px] rounded-lg bg-green-50 text-green-700 text-xs font-medium active:bg-green-100'
        }, h(Icon, { name: 'download', size: 14 }), '下载'),
        (v.status === 'failed' || v.status === 'permanent_failed') && h('button', {
          onClick: async (e) => { e.stopPropagation(); try { await api.retryVideo(v.id); if (onAction) onAction(); } catch (err) { toast.error(err.message || '操作失败'); } },
          className: 'flex-1 flex items-center justify-center gap-1.5 min-h-[36px] rounded-lg bg-slate-100 text-slate-600 text-xs font-medium active:bg-slate-200'
        }, h(Icon, { name: 'refresh', size: 14 }), '重试'),
        (v.status === 'completed' || v.status === 'relocated') && h('button', {
          onClick: (e) => { e.stopPropagation(); if (onConfirm) onConfirm({ title: '删除文件', message: '删除本地文件（保留记录）？', action: '__delete_files__', _videoId: v.id }); },
          className: 'flex-1 flex items-center justify-center gap-1.5 min-h-[36px] rounded-lg bg-orange-50 text-orange-700 text-xs font-medium active:bg-orange-100'
        }, h(Icon, { name: 'file-x', size: 14 }), '删文件'),
        v.status === 'deleted' && h('button', {
          onClick: (e) => { e.stopPropagation(); if (onConfirm) onConfirm({ title: '恢复视频', message: '恢复并重新下载？', action: '__restore__', _videoId: v.id }); },
          className: 'flex-1 flex items-center justify-center gap-1.5 min-h-[36px] rounded-lg bg-emerald-50 text-emerald-700 text-xs font-medium active:bg-emerald-100'
        }, h(Icon, { name: 'undo', size: 14 }), '恢复')
      )
    )
  );
}

function formatDurationShort(sec) {
  if (!sec || sec <= 0) return '';
  const hr = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  if (hr > 0) return `${hr}:${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}`;
  return `${m}:${s.toString().padStart(2, '0')}`;
}