import React from 'react';
import { api } from '../api.js';
import { cn, formatBytes, formatTime, toast, Icon, Card, Button, StatusBadge, Pagination, EmptyState } from '../components/utils.js';
const { createElement: h, useState, useEffect, useCallback, useRef } = React;

export function VideosPage() {
  const [videos, setVideos] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize] = useState(20);
  const [status, setStatus] = useState('');
  const [search, setSearch] = useState('');
  const [sort, setSort] = useState('created_desc');
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState(new Set());
  const [viewMode, setViewMode] = useState('table');
  const searchTimer = useRef(null);
  const [progress, setProgress] = useState([]);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api.getVideos({ page, page_size: pageSize, status, search, sort });
      const d = res.data || {};
      setVideos(d.items || []);
      setTotal(d.total || 0);
    } catch (e) { toast.error(e.message); }
    finally { setLoading(false); }
  }, [page, pageSize, status, search, sort]);

  useEffect(() => { load(); }, [load]);

  // SSE 进度
  useEffect(() => {
    let es;
    try {
      es = new EventSource('/api/events');
      es.addEventListener('progress', (e) => { try { setProgress(JSON.parse(e.data) || []); } catch {} });
    } catch {}
    return () => { if (es) es.close(); };
  }, []);

  const handleSearch = (value) => {
    if (searchTimer.current) clearTimeout(searchTimer.current);
    searchTimer.current = setTimeout(() => { setSearch(value); setPage(1); }, 300);
  };

  const handleBatch = async (action) => {
    if (selected.size === 0) return;
    try {
      await api.batchVideos(action, Array.from(selected));
      toast.success(`批量${action === 'retry' ? '重试' : action === 'cancel' ? '取消' : '删除'}成功`);
      setSelected(new Set()); load();
    } catch (e) { toast.error(e.message); }
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
    { value: 'pending', label: '待处理' },
  ];

  const getProgress = (videoId) => progress.find(p => String(p.id) === String(videoId) || String(p.download_id) === String(videoId));

  return h('div', { className: 'page-enter space-y-4' },
    // 顶栏
    h('div', { className: 'flex items-center justify-between flex-wrap gap-3' },
      h('h2', { className: 'text-lg font-semibold' }, '视频列表'),
      h('div', { className: 'flex items-center gap-2' },
        h('button', { onClick: () => setViewMode('table'), className: cn('p-2 rounded-lg', viewMode === 'table' ? 'bg-slate-700 text-white' : 'text-slate-500') }, h(Icon, { name: 'list', size: 16 })),
        h('button', { onClick: () => setViewMode('card'), className: cn('p-2 rounded-lg', viewMode === 'card' ? 'bg-slate-700 text-white' : 'text-slate-500') }, h(Icon, { name: 'grid', size: 16 })),
      )
    ),
    // 筛选栏
    h('div', { className: 'flex items-center gap-3 flex-wrap' },
      h('div', { className: 'relative' },
        h(Icon, { name: 'search', size: 16, className: 'absolute left-3 top-1/2 -translate-y-1/2 text-slate-500' }),
        h('input', {
          type: 'text', placeholder: '搜索标题/UP主...',
          onChange: (e) => handleSearch(e.target.value),
          className: 'bg-slate-900 border border-slate-700 rounded-lg pl-9 pr-3 py-2 text-sm text-slate-200 placeholder-slate-600 focus:outline-none focus:border-blue-500 w-64'
        })
      ),
      h('div', { className: 'flex gap-1' },
        statusFilters.map(f =>
          h('button', {
            key: f.value,
            onClick: () => { setStatus(f.value); setPage(1); },
            className: cn('px-3 py-1.5 rounded-lg text-xs font-medium transition-colors', status === f.value ? 'bg-blue-500/20 text-blue-400' : 'text-slate-500 hover:text-slate-300')
          }, f.label)
        )
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
      ),
    ),
    // 批量操作栏
    selected.size > 0 && h('div', { className: 'flex items-center gap-3 bg-blue-500/10 border border-blue-500/30 rounded-lg px-4 py-2' },
      h('span', { className: 'text-sm text-blue-400' }, `已选 ${selected.size} 项`),
      h(Button, { onClick: () => handleBatch('retry'), variant: 'secondary', size: 'sm' }, '重试'),
      h(Button, { onClick: () => handleBatch('cancel'), variant: 'secondary', size: 'sm' }, '取消'),
      h(Button, { onClick: () => handleBatch('delete'), variant: 'danger', size: 'sm' }, '删除'),
      h('button', { onClick: () => setSelected(new Set()), className: 'text-xs text-slate-500 hover:text-slate-300 ml-auto' }, '清除选择')
    ),
    // 内容
    loading
      ? h('div', { className: 'space-y-3' }, Array.from({ length: 5 }, (_, i) => h('div', { key: i, className: 'skeleton h-16 rounded-lg' })))
      : videos.length === 0
        ? h(EmptyState, { icon: 'video', message: status ? '该状态下暂无视频' : '暂无视频' })
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
                    const prog = getProgress(v.id);
                    return h('tr', { key: v.id, className: 'border-b border-slate-700/30 hover:bg-slate-800/50' },
                      h('td', { className: 'py-3 pr-3' },
                        h('input', { type: 'checkbox', checked: selected.has(v.id), onChange: () => toggleSelect(v.id), className: 'rounded' })
                      ),
                      h('td', { className: 'py-3 pr-3' },
                        h('div', { className: 'flex items-center gap-3' },
                          h('div', { className: 'w-20 h-12 rounded bg-slate-700 flex-shrink-0 flex items-center justify-center' }, h(Icon, { name: 'video', size: 16, className: 'text-slate-600' })),
                          h('div', { className: 'min-w-0' },
                            h('div', { className: 'text-sm truncate max-w-xs' }, v.title || v.video_id),
                            prog && h('div', { className: 'w-32 bg-slate-700 rounded-full h-1 mt-1' },
                              h('div', { className: 'bg-blue-500 h-1 rounded-full progress-bar', style: { width: (prog.percent || 0) + '%' } })
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
                          (v.status === 'failed' || v.status === 'permanent_failed') && h('button', {
                            onClick: async () => { try { await api.retryVideo(v.id); toast.success('已重试'); load(); } catch (e) { toast.error(e.message); } },
                            className: 'p-1.5 rounded hover:bg-slate-700 text-slate-400', title: '重试'
                          }, h(Icon, { name: 'refresh', size: 14 })),
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
              videos.map(v => h(Card, { key: v.id, hover: true, className: 'group' },
                h('div', { className: 'flex gap-3' },
                  h('div', { className: 'w-24 h-16 rounded bg-slate-700 flex-shrink-0 flex items-center justify-center' }, h(Icon, { name: 'video', size: 16, className: 'text-slate-600' })),
                  h('div', { className: 'flex-1 min-w-0' },
                    h('div', { className: 'text-sm font-medium truncate' }, v.title || v.video_id),
                    h('div', { className: 'text-xs text-slate-500 mt-0.5' }, v.uploader || '--'),
                    h('div', { className: 'flex items-center gap-2 mt-1' },
                      h(StatusBadge, { status: v.status }),
                      v.file_size > 0 && h('span', { className: 'text-xs text-slate-500' }, formatBytes(v.file_size))
                    )
                  )
                )
              ))
            ),
    h(Pagination, { page, pageSize, total, onChange: setPage })
  );
}
