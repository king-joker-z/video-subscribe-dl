import React from 'react';
import { api } from '../api.js';
import { cn, toast, Icon, Card, Button, Badge, EmptyState } from '../components/utils.js';
const { createElement: h, useState, useEffect, useCallback } = React;

const typeLabels = { up: 'UP 主', season: '合集', favorite: '收藏夹', watchlater: '稍后再看', series: '系列' };
const typeColors = { up: 'default', season: 'success', favorite: 'warning', watchlater: 'outline', series: 'default' };

const qualityOptions = [
  { value: 'best', label: '最高画质' },
  { value: '127', label: '8K' },
  { value: '126', label: '杜比视界' },
  { value: '125', label: 'HDR' },
  { value: '120', label: '4K' },
  { value: '116', label: '1080P60' },
  { value: '112', label: '1080P+' },
  { value: '80', label: '1080P' },
  { value: '64', label: '720P' },
  { value: '32', label: '480P' },
  { value: '16', label: '360P' },
];

// 编辑弹窗组件
function EditModal({ source, onSave, onClose }) {
  const [form, setForm] = useState({
    name: source.name || '',
    enabled: source.enabled !== false,
    download_quality: source.download_quality || 'best',
    download_quality_min: source.download_quality_min || '',
    download_filter: source.download_filter || '',
    download_codec: source.download_codec || 'all',
    download_danmaku: source.download_danmaku || false,
    skip_nfo: source.skip_nfo || false,
    skip_poster: source.skip_poster || false,
    check_interval: source.check_interval || 1800,
  });
  const [saving, setSaving] = useState(false);

  const update = (key, value) => setForm(prev => ({ ...prev, [key]: value }));

  const handleSave = async () => {
    setSaving(true);
    try {
      await api.updateSource(source.id, form);
      toast.success('保存成功');
      onSave();
    } catch (e) {
      toast.error('保存失败: ' + e.message);
    } finally {
      setSaving(false);
    }
  };

  const inputClass = 'w-full bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-blue-500';
  const labelClass = 'text-sm text-slate-400 mb-1';

  return h('div', { className: 'fixed inset-0 bg-black/60 flex items-center justify-center z-50', onClick: (e) => { if (e.target === e.currentTarget) onClose(); } },
    h('div', { className: 'bg-slate-800 border border-slate-700 rounded-xl p-6 w-full max-w-lg max-h-[90vh] overflow-y-auto space-y-4' },
      // 标题
      h('div', { className: 'flex items-center justify-between' },
        h('h3', { className: 'text-lg font-semibold text-slate-200' }, '编辑订阅源'),
        h('button', { onClick: onClose, className: 'p-1 rounded hover:bg-slate-700 text-slate-400' }, h(Icon, { name: 'x', size: 18 }))
      ),

      // 名称
      h('div', null,
        h('label', { className: labelClass }, '显示名称'),
        h('input', { type: 'text', value: form.name, onChange: (e) => update('name', e.target.value), className: inputClass })
      ),

      // 启用
      h('div', { className: 'flex items-center justify-between' },
        h('label', { className: 'text-sm text-slate-400' }, '启用'),
        h('button', {
          onClick: () => update('enabled', !form.enabled),
          className: cn('w-10 h-6 rounded-full transition-colors', form.enabled ? 'bg-blue-500' : 'bg-slate-600')
        },
          h('div', { className: cn('w-4 h-4 rounded-full bg-white transition-transform mx-1', form.enabled ? 'translate-x-4' : 'translate-x-0') })
        )
      ),

      // 画质
      h('div', null,
        h('label', { className: labelClass }, '画质偏好'),
        h('select', { value: form.download_quality, onChange: (e) => update('download_quality', e.target.value), className: inputClass },
          qualityOptions.map(o => h('option', { key: o.value, value: o.value }, o.label))
        )
      ),

      // 最低画质
      h('div', null,
        h('label', { className: labelClass }, '最低画质（留空不限制）'),
        h('select', { value: form.download_quality_min, onChange: (e) => update('download_quality_min', e.target.value), className: inputClass },
          h('option', { value: '' }, '不限制'),
          qualityOptions.filter(o => o.value !== 'best').map(o => h('option', { key: o.value, value: o.value }, o.label))
        )
      ),

      // 编码
      h('div', null,
        h('label', { className: labelClass }, '视频编码'),
        h('select', { value: form.download_codec, onChange: (e) => update('download_codec', e.target.value), className: inputClass },
          h('option', { value: 'all' }, '自动'),
          h('option', { value: 'avc' }, 'H.264 (AVC)'),
          h('option', { value: 'hevc' }, 'H.265 (HEVC)'),
          h('option', { value: 'av1' }, 'AV1')
        )
      ),

      // 标题过滤关键词
      h('div', null,
        h('label', { className: labelClass }, '标题过滤关键词（匹配才下载，留空不过滤）'),
        h('input', { type: 'text', value: form.download_filter, onChange: (e) => update('download_filter', e.target.value), placeholder: '关键词1|关键词2', className: inputClass })
      ),

      // 检查间隔
      h('div', null,
        h('label', { className: labelClass }, '检查间隔（秒）'),
        h('input', { type: 'number', value: form.check_interval, onChange: (e) => update('check_interval', parseInt(e.target.value) || 1800), min: 300, className: inputClass })
      ),

      // 开关组
      h('div', { className: 'grid grid-cols-3 gap-3' },
        h('div', { className: 'flex items-center gap-2' },
          h('input', { type: 'checkbox', checked: form.download_danmaku, onChange: (e) => update('download_danmaku', e.target.checked), className: 'rounded bg-slate-700 border-slate-600' }),
          h('label', { className: 'text-sm text-slate-400' }, '下载弹幕')
        ),
        h('div', { className: 'flex items-center gap-2' },
          h('input', { type: 'checkbox', checked: form.skip_nfo, onChange: (e) => update('skip_nfo', e.target.checked), className: 'rounded bg-slate-700 border-slate-600' }),
          h('label', { className: 'text-sm text-slate-400' }, '跳过 NFO')
        ),
        h('div', { className: 'flex items-center gap-2' },
          h('input', { type: 'checkbox', checked: form.skip_poster, onChange: (e) => update('skip_poster', e.target.checked), className: 'rounded bg-slate-700 border-slate-600' }),
          h('label', { className: 'text-sm text-slate-400' }, '跳过封面')
        )
      ),

      // URL（只读）
      h('div', null,
        h('label', { className: labelClass }, 'URL'),
        h('div', { className: 'text-xs text-slate-600 truncate bg-slate-900/50 rounded-lg px-3 py-2' }, source.url)
      ),

      // 按钮
      h('div', { className: 'flex justify-end gap-2 pt-2' },
        h(Button, { onClick: onClose, variant: 'ghost', size: 'md' }, '取消'),
        h(Button, { onClick: handleSave, disabled: saving, size: 'md' }, saving ? '保存中...' : '保存')
      )
    )
  );
}

export function SourcesPage({ onNavigate }) {
  const [sources, setSources] = useState([]);
  const [loading, setLoading] = useState(true);
  const [showAdd, setShowAdd] = useState(false);
  const [newURL, setNewURL] = useState('');
  const [adding, setAdding] = useState(false);
  const [editSource, setEditSource] = useState(null);

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
      toast.success('已添加: ' + (res.data.name || '新订阅源'));
      setNewURL(''); setShowAdd(false); load();
    } catch (e) { toast.error(e.message); }
    finally { setAdding(false); }
  };

  const handleDelete = async (id, name) => {
    if (!confirm('确定删除「' + name + '」？关联的下载记录也会被删除。')) return;
    try { await api.deleteSource(id); toast.success('已删除'); load(); }
    catch (e) { toast.error(e.message); }
  };

  const handleSync = async (id) => {
    try { await api.syncSource(id); toast.success('同步已触发'); }
    catch (e) { toast.error(e.message); }
  };

  return h('div', { className: 'page-enter space-y-4' },
    // 编辑弹窗
    editSource && h(EditModal, {
      source: editSource,
      onSave: () => { setEditSource(null); load(); },
      onClose: () => setEditSource(null)
    }),
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
                  h('button', { onClick: () => setEditSource(s), className: 'p-1.5 rounded hover:bg-slate-700 text-slate-400', title: '编辑' }, h(Icon, { name: 'edit', size: 14 })),
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
