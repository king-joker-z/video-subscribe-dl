import React from 'react';
import { api } from '../api.js';
import { cn, formatBytes, formatSpeed, formatETA, formatTime, toast, Icon, Card, Button, StatusBadge, Pagination, EmptyState } from '../components/utils.js';
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
  const searchTimer = useRef(null);
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

  useEffect(() => { load(); }, [load]);

  // 从 URL 参数同步 uploader
  useEffect(() => {
    if (params.uploader !== undefined) {
      setUploader(params.uploader || '');
      setPage(1);
    }
  }, [params.uploader]);

  // 从 URL 参数同步 source_id
  useEffect(() => {
    if (params.source_id !== undefined) {
      setSourceId(params.source_id || '');
      setSourceName(params.source_name || '');
      setPage(1);
    }
  }, [params.source_id, params.source_name]);

  // SSE 进度
  useEffect(() => {
    let es;
    try {
      es = new EventSource('/api/events');
      es.addEventListener('progress', (e) => { try { setProgress(JSON.parse(e.data) || []); } catch {} });
    } catch {}
    return () => { if (es) es.close(); };
  }, []);

  // 监听全局下载事件，自动刷新视频列表
  useEffect(() => {
    const handler = () => { setTimeout(load, 500); };
    window.addEventListener('vsd:download-event', handler);
    return () => window.removeEventListener('vsd:download-event', handler);
  }, [load]);

  // 定时自动刷新（15s），页面不可见时暂停，切回来立即刷一次
  useEffect(() => {
    const INTERVAL = 15000;
    let timer = null;

    const schedule = () => {
      timer = setTimeout(() => {
        if (!document.hidden) {
          load();
        }
        schedule();
      }, INTERVAL);
    };

    const handleVisibility = () => {
      if (!document.hidden) {
        load();
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
    if (searchTimer.current) clearTimeout(searchTimer.current);
    searchTimer.current = setTimeout(() => { setSearch(value); setPage(1); }, 300);
  };

  const handleBatch = async (action) => {
    if (selected.size === 0) return;
    if (action === 'redownload' && !confirm('将删除旧文件并重新下载，确认？')) return;
    if (action === 'delete' && !confirm('确定批量删除？')) return;
    if (action === 'restore' && !confirm('恢复选中视频并重新下载？')) return;
    if (action === 'delete_files' && !confirm('将删除选中视频的本地文件（不删数据库记录），确认？')) return;
    setBatchLoading(true);
    const labels = { retry: '重试', cancel: '取消', delete: '删除', redownload: '重新下载', delete_files: '删除文件', restore: '恢复' };
    try {
      await api.batchVideos(action, Array.from(selected));
      toast.success(`批量${labels[action] || action}成功，共 ${selected.size} 项`);
      setSelected(new Set()); load();
    } catch (e) { toast.error(e.message); }
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
    { value: 'deleted', label: '已删除' },
  ];

  const getProgress = (videoId, videoVid) => progress.find(p => (p.download_id && String(p.download_id) === String(videoId)) || (videoVid && p.bvid && p.bvid === videoVid));

  const handleDetectCharge = async () => {
    try {
      const res = await api.detectCharge();
      toast.success(res.data.message || '已启动充电检测');
    } catch (e) { toast.error(e.message); }
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
        // 手机端隐藏视图切换（强制卡片视图）
        !isMobile && h('button', { onClick: () => setViewMode('table'), className: cn('p-2 rounded-lg', viewMode === 'table' ? 'bg-slate-700 text-white' : 'text-slate-500') }, h(Icon, { name: 'list', size: 16 })),
        !isMobile && h('button', { onClick: () => setViewMode('card'), className: cn('p-2 rounded-lg', viewMode === 'card' ? 'bg-slate-700 text-white' : 'text-slate-500') }, h(Icon, { name: 'grid', size: 16 })),
      )
    ),
    // 筛选栏（手机端可折叠）
    h('div', { className: 'space-y-2' },
      // 手机端折叠控制行
      isMobile && h('button', {
        onClick: () => setFilterExpanded(v => !v),
        className: 'flex items-center gap-2 text-sm text-slate-400 hover:text-slate-200 transition-colors w-full'
      },
        h(Icon, { name: filterExpanded ? 'chevron-up' : 'chevron-down', size: 14 }),
        h('span', null, filterExpanded ? '收起筛选' : '展开筛选'),
        // 有激活筛选时显示小徽章
        (status || search || uploader || sourceId) && h('span', { className: 'ml-auto text-xs bg-blue-500/20 text-blue-400 px-1.5 py-0.5 rounded-full' }, '已筛选')
      ),
    (filterExpanded || !isMobile) && h('div', { className: 'flex items-center gap-3 flex-wrap' },
      h('div', { className: 'relative' },
        h(Icon, { name: 'search', size: 16, className: 'absolute left-3 top-1/2 -translate-y-1/2 text-slate-500' }),
        h('input', {
          type: 'text', placeholder: '搜索标题/UP主...',
          onChange: (e) => handleSearch(e.target.value),
          className: 'bg-slate-900 border border-slate-700 rounded-lg pl-9 pr-3 py-2 text-sm text-slate-200 placeholder-slate-600 focus:outline-none focus:border-blue-500 w-64'
        })
      ),
      h('div', { className: 'flex gap-1 flex-wrap' },
        statusFilters.map(f =>
          h('button', {
            key: f.value,
            onClick: () => { setStatus(f.value); setPage(1); },
            className: cn('px-3 py-1.5 rounded-lg text-xs font-medium transition-colors', status === f.value ? 'bg-blue-500/20 text-blue-400' : 'text-slate-500 hover:text-slate-300')
          }, f.label)
        ),
        uploader && h('button', {
          onClick: () => { setUploader(''); setPage(1); location.hash = '#/videos'; },
          className: 'px-3 py-1.5 rounded-lg text-xs font-medium bg-purple-500/20 text-purple-400 flex items-center gap-1'
        }, 'UP主: ' + uploader, ' ', h(Icon, { name: 'x', size: 12 })),
        sourceId && h('button', {
          onClick: () => { setSourceId(''); setSourceName(''); setPage(1); location.hash = '#/videos'; },
          className: 'px-3 py-1.5 rounded-lg text-xs font-medium bg-cyan-500/20 text-cyan-400 flex items-center gap-1'
        }, '订阅源: ' + (sourceName || '#' + sourceId), ' ', h(Icon, { name: 'x', size: 12 }))
      ),
      h('select', {
        value: sort,
        onChange: (e) => { setSort(e.target.value); setPage(1); },
        className: 'bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-xs text-slate-300'
      },
        h('option', { value: 'created_desc' }, '最新'),
        h('option', { value: 'created_asc' }, '最早'),
        h('option', { value: 'title_asc' }, '标题 A-Z'),
        h('option', { value: 'size_desc' }, '文件最大'),
        h('option', { value: 'downloaded_desc' }, '最近下载'),
      ),
    )), // 结束 filterExpanded 条件 + 外层 space-y-2 div
    // 批量操作栏
    selected.size > 0 && h('div', { className: 'flex items-center gap-2 flex-wrap bg-blue-500/10 border border-blue-500/30 rounded-lg px-3 py-2' },
      h('span', { className: 'text-sm text-blue-400 mr-1' }, batchLoading ? '处理中...' : `已选 ${selected.size} 项`),
      h(Button, { onClick: () => handleBatch('retry'), variant: 'secondary', size: 'sm', disabled: batchLoading, title: '重试' },
        h(Icon, { name: 'refresh', size: 13 }), h('span', { className: 'hidden sm:inline ml-1' }, '重试')
      ),
      h(Button, { onClick: () => handleBatch('redownload'), variant: 'secondary', size: 'sm', disabled: batchLoading, title: '重新下载' },
        h(Icon, { name: 'download', size: 13 }), h('span', { className: 'hidden sm:inline ml-1' }, '重下')
      ),
      h(Button, { onClick: () => handleBatch('cancel'), variant: 'secondary', size: 'sm', disabled: batchLoading, title: '取消下载' },
        h(Icon, { name: 'x', size: 13 }), h('span', { className: 'hidden sm:inline ml-1' }, '取消')
      ),
      h(Button, { onClick: () => handleBatch('delete_files'), variant: 'secondary', size: 'sm', disabled: batchLoading, title: '删除文件' },
        h(Icon, { name: 'file-x', size: 13 }), h('span', { className: 'hidden sm:inline ml-1' }, '删文件')
      ),
      h(Button, { onClick: () => handleBatch('restore'), variant: 'secondary', size: 'sm', disabled: batchLoading, title: '恢复' },
        h(Icon, { name: 'undo', size: 13 }), h('span', { className: 'hidden sm:inline ml-1' }, '恢复')
      ),
      h(Button, { onClick: () => handleBatch('delete'), variant: 'danger', size: 'sm', disabled: batchLoading, title: '删除' },
        h(Icon, { name: 'trash', size: 13 }), h('span', { className: 'hidden sm:inline ml-1' }, '删除')
      ),
      h('button', { onClick: () => setSelected(new Set()), className: 'text-xs text-slate-500 hover:text-slate-300 ml-auto', disabled: batchLoading }, isMobile ? h(Icon, { name: 'x-circle', size: 14 }) : '清除选择')
    ),
    // 内容
    loading
      ? h('div', { className: 'space-y-3' }, Array.from({ length: 5 }, (_, i) => h('div', { key: i, className: 'skeleton h-16 rounded-lg' })))
      : videos.length === 0
        ? h(EmptyState, {
            icon: 'video',
            message: (status || search) ? '没有符合筛选条件的视频' : '暂无视频',
            action: (status || search) ? {
              label: '清除筛选',
              onClick: () => {
                setStatus('');
                setSearch('');
                setPage(1);
                // 同时清空搜索框输入框
                const searchInput = document.querySelector('input[placeholder="搜索标题/UP主..."]');
                if (searchInput) searchInput.value = '';
              }
            } : undefined
          })
        : viewMode === 'table'
          ? h('div', { className: 'overflow-x-auto' },
              h('table', { className: 'w-full' },
                h('thead', null,
                  h('tr', { className: 'text-left text-xs text-slate-500 border-b border-slate-700/50' },
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
                      className: 'border-b border-slate-700/30 hover:bg-slate-800/50 cursor-pointer',
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
                              h('div', { className: 'w-24 bg-slate-700 rounded-full h-1' },
                                h('div', { className: 'bg-blue-500 h-1 rounded-full progress-bar', style: { width: (prog.percent || 0) + '%' } })
                              ),
                              h('span', { className: 'text-[10px] text-slate-500 tabular-nums' }, (prog.percent || 0).toFixed(1) + '%')
                            ),
                            (prog.speed > 0 || prog.total > 0) && h('div', { className: 'flex items-center gap-2 text-[10px] text-slate-500' },
                              prog.speed > 0 && h('span', { className: 'text-blue-400 font-medium' }, formatSpeed(prog.speed)),
                              formatETA(prog.downloaded, prog.total, prog.speed) && h('span', null, 'ETA ' + formatETA(prog.downloaded, prog.total, prog.speed)),
                              prog.total > 0 && h('span', null, formatBytes(prog.downloaded) + '/' + formatBytes(prog.total))
                            )
                          )
                        )
                      ),
                      h('td', { className: 'py-3 pr-3 text-sm text-slate-400 hidden md:table-cell' }, v.uploader || '--'),
                      h('td', { className: 'py-3 pr-3' }, h(StatusBadge, { status: v.status })),
                      h('td', { className: 'py-3 pr-3 text-xs text-slate-500 hidden lg:table-cell' }, v.file_size ? formatBytes(v.file_size) : '--'),
                      h('td', { className: 'py-3 pr-3 text-xs text-slate-500 hidden lg:table-cell' }, formatTime(v.created_at)),
                      h('td', { className: 'py-3' },
                        h('div', { className: 'flex items-center gap-1' },
                          v.status === 'pending' && h('button', {
                            onClick: async () => { try { await api.redownloadVideo(v.id); toast.success('已触发下载'); load(); } catch (e) { toast.error(e.message); } },
                            className: 'p-1.5 rounded hover:bg-green-900/50 text-slate-400 hover:text-green-400', title: '开始下载'
                          }, h(Icon, { name: 'download', size: 14 })),
                          ((v.status === 'failed' || v.status === 'permanent_failed') && v.status !== 'charge_blocked') && h('button', {
                            onClick: async () => { try { await api.retryVideo(v.id); toast.success('已重试'); load(); } catch (e) { toast.error(e.message); } },
                            className: 'p-1.5 rounded hover:bg-slate-700 text-slate-400', title: '重试'
                          }, h(Icon, { name: 'refresh', size: 14 })),
                          (v.status === 'completed' || v.status === 'relocated') && h('button', {
                            onClick: async () => { if (confirm('将删除旧文件并重新下载，确认？')) { try { await api.redownloadVideo(v.id); toast.success('已提交重新下载'); load(); } catch (e) { toast.error(e.message); } } },
                            className: 'p-1.5 rounded hover:bg-blue-900/50 text-slate-400 hover:text-blue-400', title: '重新下载'
                          }, h(Icon, { name: 'refresh', size: 14 })),
                          (v.status === 'completed' || v.status === 'relocated') && v.file_size > 0 && h('button', {
                            onClick: async () => { if (confirm('删除本地文件（保留记录）？')) { try { await api.deleteVideoFiles(v.id); toast.success('文件已删除'); load(); } catch (e) { toast.error(e.message); } } },
                            className: 'p-1.5 rounded hover:bg-orange-900/50 text-slate-400 hover:text-orange-400', title: '删除文件'
                          }, h(Icon, { name: 'file-x', size: 14 })),
                          v.status === 'deleted' && h('button', {
                            onClick: async () => { if (confirm('恢复并重新下载？')) { try { await api.restoreVideo(v.id); toast.success('已恢复'); load(); } catch (e) { toast.error(e.message); } } },
                            className: 'p-1.5 rounded hover:bg-emerald-900/50 text-slate-400 hover:text-emerald-400', title: '恢复'
                          }, h(Icon, { name: 'undo', size: 14 })),
                          h('button', {
                            onClick: async () => { if (confirm('确定删除？')) { try { await api.deleteVideo(v.id); toast.success('已删除'); load(); } catch (e) { toast.error(e.message); } } },
                            className: 'p-1.5 rounded hover:bg-red-900/50 text-slate-400 hover:text-red-400', title: '删除'
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
                onClick: () => setDetailVideo(v),
                onAction: load
              }))
            ),
    h(Pagination, { page, pageSize, total, onChange: setPage })
  );
}

// 判断视频平台（根据 video_id 特征）
function detectPlatform(videoId) {
  if (!videoId) return 'unknown';
  if (/^BV[0-9A-Za-z]{10}$/.test(videoId) || /^av\d+$/i.test(videoId)) return 'bilibili';
  if (/^\d{15,20}$/.test(videoId)) return 'douyin';
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
function VideoCard({ video: v, progress: prog, onClick, isMobile = false, onAction }) {
  const [imgError, setImgError] = useState(false);
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
    return h('div', { className: 'w-full h-full flex items-center justify-center text-slate-700' },
      h(Icon, { name: 'video', size: 32 })
    );
  };

  return h(Card, {
    hover: true,
    className: cn('group overflow-hidden', isDownloading && 'border-l-4 border-blue-500'),
    onClick
  },
    // 封面图区域
    h('div', { className: 'relative -mx-5 -mt-5 mb-3 aspect-video bg-slate-900 overflow-hidden' },
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
      v.duration > 0 && h('span', { className: 'absolute bottom-2 right-2 bg-black/75 text-white text-xs px-1.5 py-0.5 rounded' },
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
    h('div', { className: 'min-w-0' },
      h('div', { className: 'text-sm font-medium truncate leading-snug' }, v.title || v.video_id),
      h('div', { className: 'text-xs text-slate-500 mt-1 truncate' }, v.uploader || '--'),
      h('div', { className: 'flex items-center gap-2 mt-2' },
        h(StatusBadge, { status: v.status }),
        v.file_size > 0 && h('span', { className: 'text-xs text-slate-500' }, formatBytes(v.file_size))
      ),
      // 手机端快捷操作按钮（触摸区域加大，min-h-9）
      isMobile && h('div', { className: 'flex items-center gap-2 mt-3 pt-2 border-t border-slate-700/40' },
        v.status === 'pending' && h('button', {
          onClick: async (e) => { e.stopPropagation(); try { await api.redownloadVideo(v.id); if (onAction) onAction(); } catch {} },
          className: 'flex-1 flex items-center justify-center gap-1.5 min-h-[36px] rounded-lg bg-green-500/15 text-green-400 text-xs font-medium active:bg-green-500/30'
        }, h(Icon, { name: 'download', size: 14 }), '下载'),
        (v.status === 'failed' || v.status === 'permanent_failed') && h('button', {
          onClick: async (e) => { e.stopPropagation(); try { await api.retryVideo(v.id); if (onAction) onAction(); } catch {} },
          className: 'flex-1 flex items-center justify-center gap-1.5 min-h-[36px] rounded-lg bg-slate-700/60 text-slate-300 text-xs font-medium active:bg-slate-700'
        }, h(Icon, { name: 'refresh', size: 14 }), '重试'),
        (v.status === 'completed' || v.status === 'relocated') && h('button', {
          onClick: async (e) => { e.stopPropagation(); if (!confirm('删除本地文件（保留记录）？')) return; try { await api.deleteVideoFiles(v.id); if (onAction) onAction(); } catch {} },
          className: 'flex-1 flex items-center justify-center gap-1.5 min-h-[36px] rounded-lg bg-orange-500/15 text-orange-400 text-xs font-medium active:bg-orange-500/30'
        }, h(Icon, { name: 'file-x', size: 14 }), '删文件'),
        v.status === 'deleted' && h('button', {
          onClick: async (e) => { e.stopPropagation(); if (!confirm('恢复并重新下载？')) return; try { await api.restoreVideo(v.id); if (onAction) onAction(); } catch {} },
          className: 'flex-1 flex items-center justify-center gap-1.5 min-h-[36px] rounded-lg bg-emerald-500/15 text-emerald-400 text-xs font-medium active:bg-emerald-500/30'
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
