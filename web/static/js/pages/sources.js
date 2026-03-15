import React from 'react';
import { api } from '../api.js';
import { cn, toast, Icon, Card, Button, Badge, EmptyState } from '../components/utils.js';
const { createElement: h, useState, useEffect, useCallback } = React;

const typeLabels = { up: 'UP 主', season: '合集', favorite: '收藏夹', watchlater: '稍后再看', series: '系列' };
const typeColors = { up: 'default', season: 'success', favorite: 'warning', watchlater: 'outline', series: 'default' };

export function SourcesPage({ onNavigate }) {
  const [sources, setSources] = useState([]);
  const [loading, setLoading] = useState(true);
  const [showAdd, setShowAdd] = useState(false);
  const [newURL, setNewURL] = useState('');
  const [adding, setAdding] = useState(false);

  const load = useCallback(async () => {
    try { const res = await api.getSources(); setSources(res.data || []); }
    catch (e) { toast.error(e.message); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleAdd = async () => {
    if (!newURL.trim()) return;
    setAdding(true);
    try {
      const res = await api.createSource({ url: newURL.trim() });
      toast.success(`已添加: ${res.data.name || '新订阅源'}`);
      setNewURL(''); setShowAdd(false); load();
    } catch (e) { toast.error(e.message); }
    finally { setAdding(false); }
  };

  const handleDelete = async (id, name) => {
    if (!confirm(`确定删除「${name}」？关联的下载记录也会被删除。`)) return;
    try { await api.deleteSource(id); toast.success('已删除'); load(); }
    catch (e) { toast.error(e.message); }
  };

  const handleSync = async (id) => {
    try { await api.syncSource(id); toast.success('同步已触发'); }
    catch (e) { toast.error(e.message); }
  };

  return h('div', { className: 'page-enter space-y-4' },
    // 顶栏
    h('div', { className: 'flex items-center justify-between' },
      h('h2', { className: 'text-lg font-semibold' }, '订阅源'),
      h(Button, { onClick: () => setShowAdd(!showAdd), size: 'sm' },
        h(Icon, { name: 'plus', size: 14 }), '新增')
    ),
    // 新增表单
    showAdd && h(Card, { className: 'space-y-3' },
      h('div', { className: 'text-sm text-slate-400 mb-2' }, '输入 B 站链接，自动识别类型'),
      h('div', { className: 'flex gap-2' },
        h('input', {
          type: 'text', value: newURL, placeholder: 'https://space.bilibili.com/xxx 或 合集/收藏夹链接',
          onChange: (e) => setNewURL(e.target.value),
          onKeyDown: (e) => e.key === 'Enter' && handleAdd(),
          className: 'flex-1 bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-200 placeholder-slate-600 focus:outline-none focus:border-blue-500'
        }),
        h(Button, { onClick: handleAdd, disabled: adding || !newURL.trim(), size: 'md' }, adding ? '添加中...' : '添加'),
        h(Button, { onClick: () => { setShowAdd(false); setNewURL(''); }, variant: 'ghost', size: 'md' }, '取消')
      )
    ),
    // 列表
    loading
      ? h('div', { className: 'grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4' },
          Array.from({ length: 6 }, (_, i) => h(Card, { key: i }, h('div', { className: 'skeleton h-24 rounded-lg' }))))
      : sources.length === 0
        ? h(EmptyState, { icon: 'rss', message: '还没有订阅源', action: h(Button, { onClick: () => setShowAdd(true), size: 'sm' }, h(Icon, { name: 'plus', size: 14 }), '添加第一个') })
        : h('div', { className: 'grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4' },
            sources.map(s => h(Card, { key: s.id, hover: true, className: 'group' },
              h('div', { className: 'flex items-start justify-between mb-3' },
                h('div', { className: 'flex-1 min-w-0' },
                  h('h3', { className: 'font-medium text-slate-200 truncate' }, s.name || s.url),
                  h('div', { className: 'flex items-center gap-2 mt-1' },
                    h(Badge, { variant: typeColors[s.type] || 'outline' }, typeLabels[s.type] || s.type),
                    !s.enabled && h(Badge, { variant: 'outline' }, '已禁用')
                  )
                ),
                h('div', { className: 'flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity' },
                  h('button', { onClick: () => handleSync(s.id), className: 'p-1.5 rounded hover:bg-slate-700 text-slate-400', title: '同步' }, h(Icon, { name: 'sync', size: 14 })),
                  h('button', { onClick: () => handleDelete(s.id, s.name), className: 'p-1.5 rounded hover:bg-red-900/50 text-slate-400 hover:text-red-400', title: '删除' }, h(Icon, { name: 'trash', size: 14 }))
                )
              ),
              h('div', { className: 'grid grid-cols-4 gap-2 text-center' },
                h('div', null, h('div', { className: 'text-lg font-bold text-slate-200' }, s.video_count || 0), h('div', { className: 'text-xs text-slate-500' }, '总数')),
                h('div', null, h('div', { className: 'text-lg font-bold text-emerald-400' }, s.completed_count || 0), h('div', { className: 'text-xs text-slate-500' }, '完成')),
                h('div', null, h('div', { className: 'text-lg font-bold text-red-400' }, s.failed_count || 0), h('div', { className: 'text-xs text-slate-500' }, '失败')),
                h('div', null, h('div', { className: 'text-lg font-bold text-amber-400' }, s.pending_count || 0), h('div', { className: 'text-xs text-slate-500' }, '待处理'))
              ),
              h('div', { className: 'mt-3 text-xs text-slate-600 truncate' }, s.url)
            ))
          )
  );
}
