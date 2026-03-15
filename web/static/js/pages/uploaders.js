import React from 'react';
import { api } from '../api.js';
import { cn, toast, Icon, Card, Badge, Pagination, EmptyState } from '../components/utils.js';
const { createElement: h, useState, useEffect, useCallback, useRef } = React;

export function UploadersPage({ onNavigate }) {
  const [uploaders, setUploaders] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const [loading, setLoading] = useState(true);
  const searchTimer = useRef(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api.getUploaders({ page, page_size: 24, search });
      const d = res.data || {};
      setUploaders(d.items || []);
      setTotal(d.total || 0);
    } catch (e) { toast.error(e.message); }
    finally { setLoading(false); }
  }, [page, search]);

  useEffect(() => { load(); }, [load]);

  const handleSearch = (value) => {
    if (searchTimer.current) clearTimeout(searchTimer.current);
    searchTimer.current = setTimeout(() => { setSearch(value); setPage(1); }, 300);
  };

  return h('div', { className: 'page-enter space-y-4' },
    h('div', { className: 'flex items-center justify-between' },
      h('h2', { className: 'text-lg font-semibold' }, 'UP 主'),
      h('div', { className: 'relative' },
        h(Icon, { name: 'search', size: 16, className: 'absolute left-3 top-1/2 -translate-y-1/2 text-slate-500' }),
        h('input', {
          type: 'text', placeholder: '搜索 UP 主...',
          onChange: (e) => handleSearch(e.target.value),
          className: 'bg-slate-900 border border-slate-700 rounded-lg pl-9 pr-3 py-2 text-sm text-slate-200 placeholder-slate-600 focus:outline-none focus:border-blue-500 w-56'
        })
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
                h('div', { className: 'mb-3' },
                  h('div', { className: 'font-medium text-sm truncate text-slate-200' }, u.uploader),
                  u.mid && h('div', { className: 'text-xs text-slate-500 mt-0.5' }, 'UID: ' + u.mid)
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
                )
              )
            )
          ),
    h(Pagination, { page, pageSize: 24, total, onChange: setPage })
  );
}
