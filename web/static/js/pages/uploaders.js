import React from 'react';
import { api } from '../api.js';
import { cn, toast, Icon, Card, Badge, Button, Pagination, EmptyState } from '../components/utils.js';
const { createElement: h, useState, useEffect, useCallback, useRef } = React;


// UP主头像组件
function UploaderAvatar({ name, hasAvatar }) {
  const [imgError, setImgError] = React.useState(false);
  const avatarUrl = '/api/avatar/' + encodeURIComponent(name);

  if (!hasAvatar || imgError) {
    // 显示名字首字的彩色圆形
    const colors = ['bg-blue-600', 'bg-emerald-600', 'bg-purple-600', 'bg-amber-600', 'bg-rose-600', 'bg-cyan-600', 'bg-indigo-600', 'bg-teal-600'];
    const colorIdx = (name || '').split('').reduce((acc, c) => acc + c.charCodeAt(0), 0) % colors.length;
    const initial = (name || '?').charAt(0).toUpperCase();
    return h('div', {
      className: cn('w-10 h-10 rounded-full flex-shrink-0 flex items-center justify-center text-white text-sm font-bold', colors[colorIdx])
    }, initial);
  }

  return h('img', {
    src: avatarUrl,
    className: 'w-10 h-10 rounded-full flex-shrink-0 object-cover bg-slate-800',
    referrerPolicy: 'no-referrer',
    loading: 'lazy',
    onError: () => setImgError(true)
  });
}

export function UploadersPage({ onNavigate }) {
  const [uploaders, setUploaders] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const [sort, setSort] = useState('recent');
  const [loading, setLoading] = useState(true);
  const searchTimer = useRef(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api.getUploaders({ page, page_size: 24, search, sort });
      const d = res.data || {};
      setUploaders(d.items || []);
      setTotal(d.total || 0);
    } catch (e) { toast.error(e.message); }
    finally { setLoading(false); }
  }, [page, search, sort]);

  useEffect(() => { load(); }, [load]);

  const handleDownloadPending = async (uploaderName, e) => {
    e.stopPropagation();
    try {
      const res = await api.uploaderDownloadPending(uploaderName);
      toast.success(res.data.message || '已提交');
    } catch (e2) { toast.error(e2.message); }
  };

  const handleSearch = (value) => {
    if (searchTimer.current) clearTimeout(searchTimer.current);
    searchTimer.current = setTimeout(() => { setSearch(value); setPage(1); }, 300);
  };

  return h('div', { className: 'page-enter space-y-4' },
    h('div', { className: 'flex items-center justify-between flex-wrap gap-3' },
      h('h2', { className: 'text-lg font-semibold' }, 'UP 主'),
      h('div', { className: 'flex items-center gap-2' },
        h('div', { className: 'relative' },
          h(Icon, { name: 'search', size: 16, className: 'absolute left-3 top-1/2 -translate-y-1/2 text-slate-500' }),
          h('input', {
            type: 'text', placeholder: '搜索 UP 主...',
            onChange: (e) => handleSearch(e.target.value),
            className: 'bg-slate-900 border border-slate-700 rounded-lg pl-9 pr-3 py-2 text-sm text-slate-200 placeholder-slate-600 focus:outline-none focus:border-blue-500 w-56'
          })
        ),
        h('select', {
          value: sort,
          onChange: (e) => { setSort(e.target.value); setPage(1); },
          className: 'bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-xs text-slate-300'
        },
          h('option', { value: 'recent' }, '最近活跃'),
          h('option', { value: 'total_desc' }, '视频最多'),
          h('option', { value: 'completed_desc' }, '完成最多'),
          h('option', { value: 'failed_desc' }, '失败最多'),
          h('option', { value: 'pending_desc' }, '待处理最多'),
          h('option', { value: 'name_asc' }, '名称 A-Z'),
        )
      )
    ),
    loading
      ? h('div', { className: 'grid grid-cols-2 md:grid-cols-3 xl:grid-cols-4 gap-4' },
          Array.from({ length: 8 }, (_, i) => h(Card, { key: i }, h('div', { className: 'skeleton h-24 rounded-lg' }))))
      : uploaders.length === 0
        ? h(EmptyState, { icon: 'users', message: '暂无 UP 主数据' })
        : h('div', { className: 'grid grid-cols-2 md:grid-cols-3 xl:grid-cols-4 gap-4' },
            uploaders.map(u =>
              h(Card, {
                key: u.uploader,
                hover: true,
                onClick: () => onNavigate('videos', { uploader: u.uploader }),
              },
                h('div', { className: 'flex items-center gap-3 mb-3' },
                  h(UploaderAvatar, { name: u.uploader, hasAvatar: u.has_avatar }),
                  h('div', { className: 'min-w-0 flex-1' },
                    h('div', { className: 'font-medium text-sm truncate text-slate-200' }, u.uploader),
                    u.mid && h('div', { className: 'text-xs text-slate-500 mt-0.5' }, 'UID: ' + u.mid)
                  )
                ),
                h('div', { className: 'grid grid-cols-3 gap-2 text-center' },
                  h('div', null,
                    h('div', { className: 'text-lg font-bold text-slate-200' }, u.total || 0),
                    h('div', { className: 'text-xs text-slate-500' }, '总数')
                  ),
                  h('div', null,
                    h('div', { className: 'text-lg font-bold text-emerald-400' }, u.completed || 0),
                    h('div', { className: 'text-xs text-slate-500' }, '完成')
                  ),
                  h('div', null,
                    h('div', { className: 'text-lg font-bold text-red-400' }, u.failed || 0),
                    h('div', { className: 'text-xs text-slate-500' }, '失败')
                  ),
                ),
                (u.pending > 0) && h('div', { className: 'mt-3 pt-2 border-t border-slate-700/50' },
                  h(Button, {
                    onClick: (e) => handleDownloadPending(u.uploader, e),
                    variant: 'secondary', size: 'sm',
                    className: 'w-full text-xs'
                  }, `下载待处理 (${u.pending})`)
                )
              )
            )
          ),
    h(Pagination, { page, pageSize: 24, total, onChange: setPage })
  );
}
