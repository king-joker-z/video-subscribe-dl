import React from 'react';
import { api } from '../api.js';
import { cn, toast, Icon, Card, Button, Badge, EmptyState, Pagination, formatTimeAgo, formatNextCheck, SourceCardSkeleton, ConfirmDialog } from '../components/utils.js';
const { createElement: h, useState, useEffect, useCallback, useRef } = React;

const typeLabels = { up: 'UP 主', season: '合集', favorite: '收藏夹', watchlater: '稍后再看', series: '系列', douyin: '抖音', douyin_mix: '抖音合集', pornhub: 'Pornhub' };
const typeColors = { up: 'default', season: 'success', favorite: 'warning', watchlater: 'outline', series: 'default', douyin: 'warning', douyin_mix: 'warning', pornhub: 'error' };

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
    download_subtitle: source.download_subtitle || false,
    skip_nfo: source.skip_nfo || false,
    skip_poster: source.skip_poster || false,
    use_dynamic_api: source.use_dynamic_api || false,
    check_interval: source.check_interval || 7200,
    filter_rules: (() => { try { return JSON.parse(source.filter_rules || '[]'); } catch { return []; } })(),
  });

  const addFilterRule = () => update('filter_rules', [...form.filter_rules, { target: 'title', condition: 'contains', value: '', value2: '' }]);
  const removeFilterRule = (i) => update('filter_rules', form.filter_rules.filter((_, idx) => idx !== i));
  const updateFilterRule = (i, key, val) => {
    const rules = [...form.filter_rules];
    rules[i] = { ...rules[i], [key]: val };
    update('filter_rules', rules);
  };
  const [saving, setSaving] = useState(false);

  const update = (key, value) => setForm(prev => ({ ...prev, [key]: value }));

  // [FIXED: P1-3 round3] EditModal 绑定 ESC 关闭，提升键盘可访问性
  useEffect(() => {
    const handler = (e) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [onClose]);

  const handleSave = async () => {
    setSaving(true);
    try {
      const payload = { ...form, filter_rules: JSON.stringify(form.filter_rules || []) };
      await api.updateSource(source.id, payload);
      toast.success('保存成功');
      onSave();
    } catch (e) {
      toast.error('保存失败: ' + e.message);
    } finally {
      setSaving(false);
    }
  };

  const inputClass = 'w-full bg-slate-50 border border-slate-300 rounded-lg px-3 py-2 text-sm text-slate-800 focus:outline-none focus:border-blue-500';
  const labelClass = 'text-sm text-slate-600 mb-1';

  return h('div', { className: 'fixed inset-0 bg-black/60 flex items-center justify-center z-50', onClick: (e) => { if (e.target === e.currentTarget) onClose(); } },
    h('div', { className: 'bg-white border border-slate-200 rounded-xl p-6 w-full max-w-lg max-h-[90vh] overflow-y-auto space-y-4' },
      // 标题
      h('div', { className: 'flex items-center justify-between' },
        h('h3', { className: 'text-lg font-semibold text-slate-800' }, '编辑订阅源'),
        h('button', { onClick: onClose, className: 'p-1 rounded hover:bg-slate-100 text-slate-500' }, h(Icon, { name: 'x', size: 18 }))
      ),

      // 名称
      h('div', null,
        h('label', { className: labelClass }, '显示名称'),
        h('input', { type: 'text', value: form.name, onChange: (e) => update('name', e.target.value), className: inputClass })
      ),

      // 启用
      h('div', { className: 'flex items-center justify-between' },
        h('label', { className: 'text-sm text-slate-600' }, '启用'),
        h('button', {
          onClick: () => update('enabled', !form.enabled),
          className: cn('w-10 h-6 rounded-full transition-colors', form.enabled ? 'bg-blue-500' : 'bg-slate-300')
        },
          h('div', { className: cn('w-4 h-4 rounded-full bg-white transition-transform mx-1', form.enabled ? 'translate-x-4' : 'translate-x-0') })
        )
      ),

      // Pornhub 平台说明（画质/编码不适用）
      source.type === 'pornhub' && h('div', { className: 'text-xs text-slate-400 bg-slate-50 rounded-lg px-3 py-2' },
        '🔞 Pornhub 平台自动下载最高画质 MP4，画质偏好/视频编码设置无效。'
      ),

      // 画质（pornhub 不显示）
      source.type !== 'pornhub' && h('div', null,
        h('label', { className: labelClass }, '画质偏好'),
        h('select', { value: form.download_quality, onChange: (e) => update('download_quality', e.target.value), className: inputClass },
          qualityOptions.map(o => h('option', { key: o.value, value: o.value }, o.label))
        )
      ),

      // 最低画质（pornhub 不显示）
      source.type !== 'pornhub' && h('div', null,
        h('label', { className: labelClass }, '最低画质（留空不限制）'),
        h('select', { value: form.download_quality_min, onChange: (e) => update('download_quality_min', e.target.value), className: inputClass },
          h('option', { value: '' }, '不限制'),
          qualityOptions.filter(o => o.value !== 'best').map(o => h('option', { key: o.value, value: o.value }, o.label))
        )
      ),

      // 编码（pornhub 不显示）
      source.type !== 'pornhub' && h('div', null,
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

      // 高级过滤规则
      h('div', { className: 'space-y-2' },
        h('div', { className: 'flex items-center justify-between' },
          h('label', { className: labelClass }, '高级过滤规则'),
          h(Button, { onClick: addFilterRule, size: 'sm', variant: 'ghost' }, '+ 添加规则')
        ),
        form.filter_rules.map((rule, i) =>
          h('div', { key: i, className: 'flex gap-2 items-center bg-slate-50 rounded-lg px-2 py-1.5' },
            h('select', { value: rule.target, onChange: (e) => updateFilterRule(i, 'target', e.target.value), className: 'bg-white border border-slate-300 rounded px-2 py-1 text-xs text-slate-700 w-20' },
              h('option', { value: 'title' }, '标题'),
              h('option', { value: 'duration' }, '时长(秒)'),
              h('option', { value: 'pages' }, '分P数')
            ),
            h('select', { value: rule.condition, onChange: (e) => updateFilterRule(i, 'condition', e.target.value), className: 'bg-white border border-slate-300 rounded px-2 py-1 text-xs text-slate-700 w-24' },
              h('option', { value: 'contains' }, '包含'),
              h('option', { value: 'not_contains' }, '不包含'),
              h('option', { value: 'regex' }, '正则'),
              h('option', { value: 'gt' }, '大于'),
              h('option', { value: 'lt' }, '小于'),
              h('option', { value: 'between' }, '范围')
            ),
            h('input', { type: 'text', value: rule.value, onChange: (e) => updateFilterRule(i, 'value', e.target.value), placeholder: '值', className: 'flex-1 bg-white border border-slate-300 rounded px-2 py-1 text-xs text-slate-800 min-w-0' }),
            rule.condition === 'between' && h('input', { type: 'text', value: rule.value2 || '', onChange: (e) => updateFilterRule(i, 'value2', e.target.value), placeholder: '到', className: 'w-16 bg-white border border-slate-300 rounded px-2 py-1 text-xs text-slate-800' }),
            h('button', { onClick: () => removeFilterRule(i), className: 'p-1 rounded hover:bg-slate-100 text-slate-500 hover:text-red-500' }, h(Icon, { name: 'x', size: 14 }))
          )
        ),
        form.filter_rules.length > 0 && h('div', { className: 'text-xs text-slate-500' }, '所有规则为 AND 关系，全部满足才下载')
      ),

      // 检查间隔
      h('div', null,
        h('label', { className: labelClass }, '检查间隔（秒）'),
        h('input', { type: 'number', value: form.check_interval, onChange: (e) => update('check_interval', parseInt(e.target.value) || 7200), min: 300, className: inputClass })
      ),

      // 开关组（2×2 网格，每项独立）
      h('div', { className: 'grid grid-cols-2 gap-3' },
        h('div', { className: 'flex items-center gap-2' },
          h('input', { type: 'checkbox', checked: form.download_danmaku, onChange: (e) => update('download_danmaku', e.target.checked), className: 'rounded border-slate-300' }),
          h('label', { className: 'text-sm text-slate-600' }, '下载弹幕')
        ),
        h('div', { className: 'flex items-center gap-2' },
          h('input', { type: 'checkbox', checked: form.download_subtitle, onChange: (e) => update('download_subtitle', e.target.checked), className: 'rounded border-slate-300' }),
          h('label', { className: 'text-sm text-slate-600' }, '下载字幕')
        ),
        h('div', { className: 'flex items-center gap-2' },
          h('input', { type: 'checkbox', checked: form.skip_nfo, onChange: (e) => update('skip_nfo', e.target.checked), className: 'rounded border-slate-300' }),
          h('label', { className: 'text-sm text-slate-600' }, '跳过 NFO')
        ),
        h('div', { className: 'flex items-center gap-2' },
          h('input', { type: 'checkbox', checked: form.skip_poster, onChange: (e) => update('skip_poster', e.target.checked), className: 'rounded border-slate-300' }),
          h('label', { className: 'text-sm text-slate-600' }, '跳过封面')
        )
      ),

      // 动态 API 开关（仅 UP 主类型显示）
      source.type === 'up' && h('div', { className: 'flex items-center justify-between bg-slate-50 rounded-lg px-3 py-2' },
        h('div', null,
          h('label', { className: 'text-sm text-slate-700' }, '使用动态 API'),
          h('div', { className: 'text-xs text-slate-500 mt-0.5' }, '使用动态接口拉取视频，风控概率更低，但可能不包含部分旧视频')
        ),
        h('button', {
          onClick: () => update('use_dynamic_api', !form.use_dynamic_api),
          className: cn('w-10 h-6 rounded-full transition-colors flex-shrink-0 ml-3', form.use_dynamic_api ? 'bg-blue-500' : 'bg-slate-300')
        },
          h('div', { className: cn('w-4 h-4 rounded-full bg-white transition-transform mx-1', form.use_dynamic_api ? 'translate-x-4' : 'translate-x-0') })
        )
      ),

      // URL（只读）
      h('div', null,
        h('label', { className: labelClass }, 'URL'),
        h('div', { className: 'text-xs text-slate-500 truncate bg-slate-50 rounded-lg px-3 py-2' }, source.url)
      ),

      // 按钮
      h('div', { className: 'flex justify-end gap-2 pt-2' },
        h(Button, { onClick: onClose, variant: 'ghost', size: 'md' }, '取消'),
        h(Button, { onClick: handleSave, disabled: saving, size: 'md' }, saving ? '保存中...' : '保存')
      )
    )
  );
}


// 从关注列表导入选项卡
function ImportFollowTab({ onDone }) {
  const [uppers, setUppers] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState("");
  const [searchInput, setSearchInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState(new Set());
  const [subscribing, setSubscribing] = useState(false);
  const pageSize = 20;
  const searchTimer = useRef(null);

  const loadUppers = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api.getMyUppers(page, pageSize, search);
      setUppers(res.data?.items || []);
      setTotal(res.data?.total || 0);
    } catch (e) { toast.error(e.message); }
    finally { setLoading(false); }
  }, [page, search]);

  const handleSearchChange = (value) => {
    setSearchInput(value);
    if (searchTimer.current) clearTimeout(searchTimer.current);
    searchTimer.current = setTimeout(() => { setSearch(value); setPage(1); }, 300);
  };

  useEffect(() => { loadUppers(); }, [loadUppers]);

  const toggle = (mid) => {
    const s = new Set(selected);
    s.has(mid) ? s.delete(mid) : s.add(mid);
    setSelected(s);
  };

  const selectAll = () => {
    const unsubscribed = uppers.filter(u => !u.subscribed).map(u => u.mid);
    setSelected(new Set(unsubscribed));
  };

  const handleSubscribe = async () => {
    if (selected.size === 0) return;
    setSubscribing(true);
    try {
      const res = await api.batchSubscribe({ mids: [...selected], type: "up" });
      toast.success(`已订阅 ${res.data?.created || 0} 个 UP 主`);
      setSelected(new Set());
      loadUppers();
      if (onDone) onDone();
    } catch (e) { toast.error(e.message); }
    finally { setSubscribing(false); }
  };

  const totalPages = Math.ceil(total / pageSize);

  return h("div", { className: "space-y-3" },
    // 搜索框
    h("div", { className: "flex gap-2" },
      h("input", {
        type: "text", value: searchInput, placeholder: "搜索 UP 主...",
        onChange: (e) => handleSearchChange(e.target.value),
        className: "flex-1 bg-slate-50 border border-slate-300 rounded-lg px-3 py-2 text-sm text-slate-800 placeholder-slate-400 focus:outline-none focus:border-blue-500"
      }),
      h(Button, { onClick: selectAll, size: "sm", variant: "secondary" }, "全选未订阅")
    ),
    // 列表
    loading
      ? h("div", { className: "text-center text-slate-500 py-8" }, "加载中...")
      : uppers.length === 0
        ? h("div", { className: "text-center text-slate-500 py-8" }, "未找到关注的 UP 主（请先登录 B 站）")
        : h("div", { className: "space-y-1 max-h-60 overflow-y-auto" },
            uppers.map(u =>
              h("label", { key: u.mid, className: cn("flex items-center gap-3 px-3 py-2 rounded-lg cursor-pointer hover:bg-slate-100", selected.has(u.mid) && "bg-slate-100") },
                h("input", { type: "checkbox", checked: selected.has(u.mid) || u.subscribed, disabled: u.subscribed, onChange: () => toggle(u.mid), className: "rounded border-slate-300" }),
                h("span", { className: "text-sm text-slate-800 flex-1 truncate" }, u.uname || u.name),
                u.subscribed && h(Badge, { variant: "success" }, "已订阅")
              )
            )
          ),
    // 分页
    totalPages > 1 && h("div", { className: "flex items-center justify-center gap-2" },
      h(Button, { onClick: () => setPage(p => Math.max(1, p - 1)), disabled: page <= 1, size: "sm", variant: "ghost" }, "上一页"),
      h("span", { className: "text-xs text-slate-500" }, `${page} / ${totalPages}`),
      h(Button, { onClick: () => setPage(p => Math.min(totalPages, p + 1)), disabled: page >= totalPages, size: "sm", variant: "ghost" }, "下一页")
    ),
    // 订阅按钮
    selected.size > 0 && h("div", { className: "flex justify-end" },
      h(Button, { onClick: handleSubscribe, disabled: subscribing, size: "md" }, subscribing ? "订阅中..." : `一键订阅 ${selected.size} 个 UP 主`)
    )
  );
}
// [FIXED: P1-5] 三选项删除弹窗：取消 / 仅删订阅 / 删订阅+文件
function DeleteSourceDialog({ name, onCancel, onDeleteOnly, onDeleteWithFiles }) {
  return h('div', { className: 'fixed inset-0 z-50 flex items-center justify-center p-4' },
    h('div', { className: 'absolute inset-0 bg-black/40', onClick: onCancel }),
    h('div', { className: 'relative bg-white rounded-2xl shadow-xl p-6 w-full max-w-sm space-y-4' },
      h('h3', { className: 'text-base font-semibold text-slate-800' }, '删除订阅源'),
      h('p', { className: 'text-sm text-slate-500' }, `确定删除订阅「${name}」？`),
      h('div', { className: 'flex flex-col gap-2' },
        h('button', { onClick: onDeleteWithFiles, className: 'px-4 py-2 rounded-lg text-sm bg-red-500 text-white hover:bg-red-600 text-left' }, '删订阅 + 本地文件'),
        h('button', { onClick: onDeleteOnly, className: 'px-4 py-2 rounded-lg text-sm bg-amber-500 text-white hover:bg-amber-600 text-left' }, '仅删订阅（保留文件）'),
        h('button', { onClick: onCancel, className: 'px-4 py-2 rounded-lg text-sm text-slate-600 hover:bg-slate-100' }, '取消')
      )
    )
  );
}

export function SourcesPage({ onNavigate }) {
  const [sources, setSources] = useState([]);
  const [loading, setLoading] = useState(true);
  const [showAdd, setShowAdd] = useState(false);
  const [addPlatform, setAddPlatform] = useState("bili"); // "bili" | "douyin" | "pornhub"
  const [addBiliTab, setAddBiliTab] = useState("url"); // "url" | "import"
  const [addDouyinTab, setAddDouyinTab] = useState("url"); // "url" | "uniqueid"
  const [newURL, setNewURL] = useState('');
  const [adding, setAdding] = useState(false);
  const [editSource, setEditSource] = useState(null);
  // [FIXED: P1-5] 用自定义弹窗替换双重 confirm()
  const [deleteSource, setDeleteSource] = useState(null); // { id, name }
  const [parsing, setParsing] = useState(false);
  const [parseResult, setParseResult] = useState(null);
  const [addForm, setAddForm] = useState({
    name: '',
    enabled: true,
    download_quality: 'best',
    download_codec: 'all',
    download_filter: '',
    skip_nfo: false,
    skip_poster: false,
    check_interval: 7200,
  });

  const [douyinPaused, setDouyinPaused] = useState(null);
  const [checkingIds, setCheckingIds] = useState(new Set());
  const [sourcePage, setSourcePage] = useState(1);
  const [sourceTotal, setSourceTotal] = useState(0);
  const [filterType, setFilterType] = useState('');

  const handleFilterType = (type) => {
    setFilterType(type);
    setSourcePage(1);
  };

  const load = useCallback(async () => {
    // [FIXED: P1-7] 每次刷新前设置 loading=true，让后续刷新也能显示加载状态（初始值已是 true）
    setLoading(true);
    try {
      const params = { page: sourcePage, page_size: 20 };
      if (filterType) params.type = filterType;
      const res = await api.getSources(params);
      if (Array.isArray(res.data)) {
        setSources(res.data);
      } else if (res.data && res.data.sources) {
        setSources(res.data.sources);
        setSourceTotal(res.data.total || 0);
      } else {
        setSources([]);
      }
    }
    catch (e) { toast.error(e.message); }
    finally { setLoading(false); }
  }, [sourcePage, filterType]);

  const loadDouyinStatus = useCallback(async () => {
    try { const res = await api.getDouyinStatus(); setDouyinPaused(res.data || null); }
    catch (e) { /* ignore */ }
  }, []);

  const handleDouyinResume = async () => {
    try {
      await api.resumeDouyin();
      toast.success('抖音下载已恢复');
      setDouyinPaused(null);
      loadDouyinStatus();
    } catch (e) { toast.error('恢复失败: ' + e.message); }
  };

  useEffect(() => { load(); loadDouyinStatus(); }, [load, loadDouyinStatus]);

  // 定时自动刷新（30s），页面不可见时暂停，切回来立即刷一次
  useEffect(() => {
    const INTERVAL = 30000;
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

  const handleParse = async () => {
    if (!newURL.trim()) return;
    setParsing(true);
    try {
      const res = await api.parseSource(newURL.trim());
      const d = res.data || {};
      setParseResult(d);
      setAddForm(prev => ({ ...prev, name: d.name || '' }));
    } catch (e) { toast.error(e.message); setParseResult(null); }
    finally { setParsing(false); }
  };

  const updateAddForm = (key, value) => setAddForm(prev => ({ ...prev, [key]: value }));

  const handleAdd = async () => {
    if (!newURL.trim()) return;
    setAdding(true);
    try {
      const body = { url: newURL.trim() };
      if (addForm.name) body.name = addForm.name;
      body.enabled = addForm.enabled;
      body.download_quality = addForm.download_quality;
      body.download_codec = addForm.download_codec;
      body.download_filter = addForm.download_filter;
      body.skip_nfo = addForm.skip_nfo;
      body.skip_poster = addForm.skip_poster;
      body.check_interval = addForm.check_interval;
      const res = await api.createSource(body);
      toast.success('已添加: ' + (res.data.name || '新订阅源'));
      setNewURL(''); setShowAdd(false); setParseResult(null);
      setAddForm({ name: '', enabled: true, download_quality: 'best', download_codec: 'all', download_filter: '', skip_nfo: false, skip_poster: false, check_interval: 7200 });
      load();
    } catch (e) { toast.error(e.message); }
    finally { setAdding(false); }
  };

  const resetAddModal = () => {
    setShowAdd(false); setNewURL(''); setParseResult(null);
    setAddPlatform('bili'); setAddBiliTab('url'); setAddDouyinTab('url');
    setAddForm({ name: '', enabled: true, download_quality: 'best', download_codec: 'all', download_filter: '', skip_nfo: false, skip_poster: false, check_interval: 7200 });
  };

  // [FIXED: P1-5] 改为自定义弹窗，一次性提供三个选项
  const handleDelete = (id, name) => {
    setDeleteSource({ id, name });
  };

  const executeDelete = async (withFiles) => {
    if (!deleteSource) return;
    const { id } = deleteSource;
    setDeleteSource(null);
    try {
      await api.deleteSource(id, withFiles);
      toast.success(withFiles ? '已删除（含本地文件）' : '已删除');
      load();
    }
    catch (e) { toast.error(e.message); }
  };

  const handleSync = async (id) => {
    setCheckingIds(prev => new Set([...prev, id]));
    try {
      await api.syncSource(id);
      toast.success('已触发检查，稍后刷新查看结果');
      // 延迟 2s 后刷新，给后端一点时间处理
      setTimeout(() => load(), 2000);
    } catch (e) {
      toast.error('触发失败: ' + e.message);
    } finally {
      setCheckingIds(prev => { const s = new Set(prev); s.delete(id); return s; });
    }
  };

  const handleFullScan = async (id) => {
    if (!confirm('确认全量补漏扫描？将翻完所有投稿页，已下载的会自动跳过。')) return;
    try { await api.fullScanSource(id); toast.success('全量补漏扫描已触发'); }
    catch (e) { toast.error(e.message); }
  };

  // === Export / Import ===
  const [showImportResult, setShowImportResult] = useState(null);

  const handleExport = async () => {
    try {
      const { blob, filename } = await api.exportSources();
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url; a.download = filename; a.click();
      URL.revokeObjectURL(url);
      toast.success('导出成功');
    } catch (e) { toast.error('导出失败: ' + e.message); }
  };

  const handleImportFile = () => {
    const input = document.createElement('input');
    input.type = 'file'; input.accept = '.json';
    // [FIXED: P2-12] 先 append 到 DOM 再 click，兼容 Firefox 不响应未挂载元素的 click 事件
    input.style.display = 'none';
    document.body.appendChild(input);
    input.onchange = async (e) => {
      document.body.removeChild(input);
      const file = e.target.files[0];
      if (!file) return;
      try {
        const text = await file.text();
        const data = JSON.parse(text);
        if (!data.sources || !Array.isArray(data.sources)) {
          toast.error('无效的导入文件：缺少 sources 数组');
          return;
        }
        const res = await api.importSources(data);
        const r = res.data;
        setShowImportResult(r);
        if (r.created > 0) {
          toast.success('导入完成: 新增 ' + r.created + ' 个，跳过 ' + r.skipped + ' 个');
          load();
        } else {
          toast.info('导入完成: 全部已存在，跳过 ' + r.skipped + ' 个');
        }
      } catch (err) { toast.error('导入失败: ' + err.message); }
    };
    input.click();
  };

  return h('div', { className: 'page-enter space-y-4' },
    // 编辑弹窗
    editSource && h(EditModal, {
      source: editSource,
      onSave: () => { setEditSource(null); load(); },
      onClose: () => setEditSource(null)
    }),
    // [FIXED: P1-5] 删除订阅源三选项弹窗
    deleteSource && h(DeleteSourceDialog, {
      name: deleteSource.name,
      onCancel: () => setDeleteSource(null),
      onDeleteOnly: () => executeDelete(false),
      onDeleteWithFiles: () => executeDelete(true),
    }),
    // 移动端 FAB 新增按钮（固定右下角，仅手机端显示）
    h('button', {
      onClick: () => setShowAdd(true),
      className: 'lg:hidden fixed bottom-20 right-4 z-40 w-14 h-14 rounded-full bg-blue-500 hover:bg-blue-600 active:bg-blue-700 text-white shadow-lg flex items-center justify-center transition-colors',
      title: '新增订阅源',
      'aria-label': '新增订阅源'
    }, h(Icon, { name: 'plus', size: 24 })),
    // 导入结果弹窗
    showImportResult && h('div', { className: 'fixed inset-0 bg-black/60 flex items-center justify-center z-50', onClick: (e) => { if (e.target === e.currentTarget) setShowImportResult(null); } },
      h('div', { className: 'bg-white border border-slate-200 rounded-xl p-6 w-full max-w-md space-y-4' },
        h('div', { className: 'flex items-center justify-between' },
          h('h3', { className: 'text-lg font-semibold text-slate-800' }, '导入结果'),
          h('button', { onClick: () => setShowImportResult(null), className: 'p-1 rounded hover:bg-slate-100 text-slate-500' }, h(Icon, { name: 'x', size: 18 }))
        ),
        h('div', { className: 'grid grid-cols-3 gap-4 text-center' },
          h('div', null,
            h('div', { className: 'text-2xl font-bold text-emerald-600' }, showImportResult.created),
            h('div', { className: 'text-xs text-slate-500' }, '新增')
          ),
          h('div', null,
            h('div', { className: 'text-2xl font-bold text-amber-600' }, showImportResult.skipped),
            h('div', { className: 'text-xs text-slate-500' }, '跳过')
          ),
          h('div', null,
            h('div', { className: 'text-2xl font-bold text-red-500' }, showImportResult.errors),
            h('div', { className: 'text-xs text-slate-500' }, '失败')
          )
        ),
        showImportResult.details && showImportResult.details.length > 0 && h('div', { className: 'max-h-48 overflow-y-auto space-y-1' },
          showImportResult.details.map((d, i) =>
            h('div', { key: i, className: 'text-xs px-2 py-1 rounded ' + (d.startsWith('创建') ? 'text-emerald-700 bg-emerald-50' : d.startsWith('跳过') ? 'text-amber-700 bg-amber-50' : 'text-red-500 bg-red-50') }, d)
          )
        ),
        h('div', { className: 'flex justify-end pt-2' },
          h(Button, { onClick: () => setShowImportResult(null), size: 'md' }, '确定')
        )
      )
    ),
    // 抖音风控暂停警告
    douyinPaused && douyinPaused.paused && h('div', { className: 'flex items-center justify-between gap-3 px-4 py-3 bg-amber-50 border border-amber-200 rounded-lg' },
      h('div', { className: 'flex items-center gap-2 min-w-0' },
        h(Icon, { name: 'alert-triangle', size: 16, className: 'text-amber-600 shrink-0' }),
        h('div', { className: 'text-sm' },
          h('span', { className: 'font-medium text-amber-700' }, '抖音下载已暂停'),
          h('span', { className: 'text-amber-600 ml-2' }, douyinPaused.reason || '风控触发'),
          douyinPaused.paused_duration && h('span', { className: 'text-amber-500 ml-2 text-xs' }, '已暂停 ' + douyinPaused.paused_duration)
        )
      ),
      h(Button, { onClick: handleDouyinResume, size: 'sm', variant: 'outline', className: 'shrink-0 border-amber-400 text-amber-700 hover:bg-amber-50' }, '恢复下载')
    ),
    // 顶栏
    h('div', { className: 'flex items-center justify-between' },
      h('h2', { className: 'text-lg font-semibold' }, '订阅源'),
      h('div', { className: 'flex items-center gap-2' },
        h(Button, { onClick: handleExport, size: 'sm', variant: 'ghost', title: '导出订阅源' },
          h(Icon, { name: 'download', size: 14 }), '导出'),
        h(Button, { onClick: handleImportFile, size: 'sm', variant: 'ghost', title: '导入订阅源' },
          h(Icon, { name: 'upload', size: 14 }), '导入'),
        h(Button, { onClick: () => setShowAdd(!showAdd), size: 'sm', className: 'hidden lg:flex' },
          h(Icon, { name: 'plus', size: 14 }), '新增')
      )
    ),
    // 类型筛选
    h('div', { className: 'flex flex-wrap gap-1.5' },
      [['', '全部'], ['up', 'UP 主'], ['season', '合集'], ['favorite', '收藏夹'], ['watchlater', '稍后再看'], ['series', '系列'], ['douyin', '抖音'], ['douyin_mix', '抖音合集'], ['pornhub', 'Pornhub']].map(([val, label]) =>
        h('button', {
          key: val,
          onClick: () => handleFilterType(val),
          className: cn('px-3 py-1 rounded-full text-xs border transition-colors',
            filterType === val
              ? 'bg-blue-500 border-blue-500 text-white'
              : 'bg-white border-slate-300 text-slate-600 hover:border-blue-400 hover:text-blue-600')
        }, label)
      )
    ),
    // 新增订阅弹窗
    showAdd && h('div', { className: 'fixed inset-0 bg-black/60 flex items-center justify-center z-50', onClick: (e) => { if (e.target === e.currentTarget) resetAddModal(); } },
      h('div', { className: 'bg-white border border-slate-200 rounded-xl p-6 w-full max-w-lg max-h-[90vh] overflow-y-auto space-y-4' },
        h('div', { className: 'flex items-center justify-between' },
          h('h3', { className: 'text-lg font-semibold text-slate-800' }, '新增订阅源'),
          h('button', { onClick: resetAddModal, className: 'p-1 rounded hover:bg-slate-100 text-slate-500' }, h(Icon, { name: 'x', size: 18 }))
        ),

        // 平台选择 Tab
        h('div', { className: 'flex gap-1 bg-slate-50 rounded-lg p-1' },
          h('button', { onClick: () => setAddPlatform('bili'), className: cn('flex-1 px-3 py-1.5 rounded-md text-sm transition-colors', addPlatform === 'bili' ? 'bg-white text-slate-800 shadow-sm' : 'text-slate-500 hover:text-slate-700') }, '📺 B站'),
          h('button', { onClick: () => setAddPlatform('douyin'), className: cn('flex-1 px-3 py-1.5 rounded-md text-sm transition-colors', addPlatform === 'douyin' ? 'bg-white text-slate-800 shadow-sm' : 'text-slate-500 hover:text-slate-700') }, '🎵 抖音'),
          h('button', { onClick: () => setAddPlatform('pornhub'), className: cn('flex-1 px-3 py-1.5 rounded-md text-sm transition-colors', addPlatform === 'pornhub' ? 'bg-white text-slate-800 shadow-sm' : 'text-slate-500 hover:text-slate-700') }, '🔞 PH')
        ),

        // === B站 Tab ===
        addPlatform === 'bili' && h('div', { className: 'space-y-4' },
          // B站子 Tab
          h('div', { className: 'flex gap-1 bg-slate-100 rounded-lg p-0.5' },
            h('button', { onClick: () => setAddBiliTab('url'), className: cn('flex-1 px-3 py-1.5 rounded-md text-sm transition-colors', addBiliTab === 'url' ? 'bg-white text-slate-800 shadow-sm' : 'text-slate-500 hover:text-slate-700') }, '链接'),
            h('button', { onClick: () => setAddBiliTab('import'), className: cn('flex-1 px-3 py-1.5 rounded-md text-sm transition-colors', addBiliTab === 'import' ? 'bg-white text-slate-800 shadow-sm' : 'text-slate-500 hover:text-slate-700') }, '从关注导入')
          ),

          // B站「链接」子 Tab
          addBiliTab === 'url' && h('div', { className: 'space-y-4' },
          // URL 输入 + 解析按钮
          h('div', null,
            h('label', { className: 'text-sm text-slate-600 mb-1' }, 'B 站链接'),
            h('div', { className: 'flex gap-2' },
              h('input', {
                type: 'text', value: newURL, placeholder: 'bilibili.com/video/BVxxx | bilibili.com/space/xxx | 合集/收藏夹链接',
                onChange: (e) => { setNewURL(e.target.value); setParseResult(null); },
                onKeyDown: (e) => e.key === 'Enter' && handleParse(),
                className: 'flex-1 bg-slate-50 border border-slate-300 rounded-lg px-3 py-2 text-sm text-slate-800 placeholder-slate-400 focus:outline-none focus:border-blue-500'
              }),
              h(Button, { onClick: handleParse, disabled: parsing || !newURL.trim(), size: 'md', variant: 'secondary' }, parsing ? '解析中...' : '解析')
            )
          ),

          // 解析结果展示
          parseResult && h('div', { className: 'bg-slate-50 border border-slate-200 rounded-lg px-4 py-3 space-y-3' },
            h('div', { className: 'flex items-center gap-2' },
              h(Badge, { variant: typeColors[parseResult.type] || 'outline' }, typeLabels[parseResult.type] || parseResult.type),
              parseResult.uploader && h('span', { className: 'text-xs text-slate-500' }, parseResult.uploader),
              parseResult.already_subscribed && h(Badge, { variant: 'success' }, '✓ 已订阅')
            ),
            parseResult.already_subscribed && h('div', { className: 'text-xs text-emerald-600 bg-emerald-50 rounded-lg px-3 py-2' },
              '该订阅源已存在，无需重复添加。'
            ),
            !parseResult.already_subscribed && h('div', null,
              h('label', { className: 'text-sm text-slate-600 mb-1' }, '显示名称'),
              h('input', { type: 'text', value: addForm.name, onChange: (e) => updateAddForm('name', e.target.value), className: 'w-full bg-white border border-slate-300 rounded-lg px-3 py-2 text-sm text-slate-800 focus:outline-none focus:border-blue-500' })
            ),
            !parseResult.already_subscribed && h('div', { className: 'flex items-center justify-between' },
              h('label', { className: 'text-sm text-slate-600' }, '启用'),
              h('button', {
                onClick: () => updateAddForm('enabled', !addForm.enabled),
                className: cn('w-10 h-6 rounded-full transition-colors', addForm.enabled ? 'bg-blue-500' : 'bg-slate-300')
              }, h('div', { className: cn('w-4 h-4 rounded-full bg-white transition-transform mx-1', addForm.enabled ? 'translate-x-4' : 'translate-x-0') }))
            ),
            !parseResult.already_subscribed && h('div', null,
              h('label', { className: 'text-sm text-slate-600 mb-1' }, '画质偏好'),
              h('select', { value: addForm.download_quality, onChange: (e) => updateAddForm('download_quality', e.target.value), className: 'w-full bg-white border border-slate-300 rounded-lg px-3 py-2 text-sm text-slate-800 focus:outline-none focus:border-blue-500' },
                qualityOptions.map(o => h('option', { key: o.value, value: o.value }, o.label))
              )
            ),
            !parseResult.already_subscribed && h('div', null,
              h('label', { className: 'text-sm text-slate-600 mb-1' }, '视频编码'),
              h('select', { value: addForm.download_codec, onChange: (e) => updateAddForm('download_codec', e.target.value), className: 'w-full bg-white border border-slate-300 rounded-lg px-3 py-2 text-sm text-slate-800 focus:outline-none focus:border-blue-500' },
                h('option', { value: 'all' }, '自动'),
                h('option', { value: 'avc' }, 'H.264 (AVC)'),
                h('option', { value: 'hevc' }, 'H.265 (HEVC)'),
                h('option', { value: 'av1' }, 'AV1')
              )
            ),
            !parseResult.already_subscribed && h('div', null,
              h('label', { className: 'text-sm text-slate-600 mb-1' }, '标题过滤关键词（匹配才下载，留空不过滤）'),
              h('input', { type: 'text', value: addForm.download_filter, onChange: (e) => updateAddForm('download_filter', e.target.value), placeholder: '关键词1|关键词2', className: 'w-full bg-white border border-slate-300 rounded-lg px-3 py-2 text-sm text-slate-800 focus:outline-none focus:border-blue-500' })
            ),
            !parseResult.already_subscribed && h('div', null,
              h('label', { className: 'text-sm text-slate-600 mb-1' }, '检查间隔（秒）'),
              h('input', { type: 'number', value: addForm.check_interval, onChange: (e) => updateAddForm('check_interval', parseInt(e.target.value) || 7200), min: 300, className: 'w-full bg-white border border-slate-300 rounded-lg px-3 py-2 text-sm text-slate-800 focus:outline-none focus:border-blue-500' })
            ),
            !parseResult.already_subscribed && h('div', { className: 'grid grid-cols-2 gap-3' },
              h('div', { className: 'flex items-center gap-2' },
                h('input', { type: 'checkbox', checked: addForm.skip_nfo, onChange: (e) => updateAddForm('skip_nfo', e.target.checked), className: 'rounded border-slate-300' }),
                h('label', { className: 'text-sm text-slate-600' }, '跳过 NFO')
              ),
              h('div', { className: 'flex items-center gap-2' },
                h('input', { type: 'checkbox', checked: addForm.skip_poster, onChange: (e) => updateAddForm('skip_poster', e.target.checked), className: 'rounded border-slate-300' }),
                h('label', { className: 'text-sm text-slate-600' }, '跳过封面')
              )
            )
          ),

          // 底部按钮
          h('div', { className: 'flex justify-end gap-2 pt-2' },
            h(Button, { onClick: resetAddModal, variant: 'ghost', size: 'md' }, '取消'),
            h(Button, { onClick: handleAdd, disabled: adding || !newURL.trim() || !!(parseResult && parseResult.already_subscribed), size: 'md' },
              adding ? '添加中...' : (parseResult && parseResult.already_subscribed) ? '已订阅' : '确认添加'
            )
          )
          ),  // 关闭 B站「链接」子 Tab

          // B站「从关注导入」子 Tab
          addBiliTab === 'import' && h(ImportFollowTab, { onDone: () => { resetAddModal(); load(); } })
        ),  // 关闭 B站大 Tab

        // === 抖音 Tab ===
        addPlatform === 'douyin' && h('div', { className: 'space-y-4' },
          h('div', null,
              h('label', { className: 'text-sm text-slate-600 mb-1' }, '抖音链接'),
              h('div', { className: 'text-xs text-slate-400 mb-1.5' }, '支持：用户主页链接，如 https://www.douyin.com/user/xxx'),
              h('div', { className: 'flex gap-2' },
                h('input', {
                  type: 'text', value: newURL,
                  placeholder: 'https://www.douyin.com/user/xxx 或 https://v.douyin.com/xxx',
                  onChange: (e) => { setNewURL(e.target.value); setParseResult(null); },
                  onKeyDown: (e) => e.key === 'Enter' && handleParse(),
                  className: 'flex-1 bg-slate-50 border border-slate-300 rounded-lg px-3 py-2 text-sm text-slate-800 placeholder-slate-400 focus:outline-none focus:border-blue-500'
                }),
                h(Button, { onClick: handleParse, disabled: parsing || !newURL.trim(), size: 'md', variant: 'secondary' }, parsing ? '解析中...' : '解析')
              )
            ),
            parseResult && h('div', { className: 'bg-slate-50 border border-slate-200 rounded-lg px-4 py-3 space-y-3' },
              h('div', { className: 'flex items-center gap-2' },
                h(Badge, { variant: typeColors[parseResult.type] || 'outline' }, typeLabels[parseResult.type] || parseResult.type),
                parseResult.uploader && h('span', { className: 'text-xs text-slate-500' }, parseResult.uploader),
                parseResult.already_subscribed && h(Badge, { variant: 'success' }, '✓ 已订阅')
              ),
              parseResult.already_subscribed && h('div', { className: 'text-xs text-emerald-600 bg-emerald-50 rounded-lg px-3 py-2' },
                '该订阅源已存在，无需重复添加。'
              ),
              !parseResult.already_subscribed && h('div', null,
                h('label', { className: 'text-sm text-slate-600 mb-1' }, '显示名称'),
                h('input', { type: 'text', value: addForm.name, onChange: (e) => updateAddForm('name', e.target.value), className: 'w-full bg-white border border-slate-300 rounded-lg px-3 py-2 text-sm text-slate-800 focus:outline-none focus:border-blue-500' })
              ),
              !parseResult.already_subscribed && h('div', { className: 'flex items-center justify-between' },
                h('label', { className: 'text-sm text-slate-600' }, '启用'),
                h('button', {
                  onClick: () => updateAddForm('enabled', !addForm.enabled),
                  className: cn('w-10 h-6 rounded-full transition-colors', addForm.enabled ? 'bg-blue-500' : 'bg-slate-300')
                }, h('div', { className: cn('w-4 h-4 rounded-full bg-white transition-transform mx-1', addForm.enabled ? 'translate-x-4' : 'translate-x-0') }))
              ),
              !parseResult.already_subscribed && h('div', null,
                h('label', { className: 'text-sm text-slate-600 mb-1' }, '检查间隔（秒）'),
                h('input', { type: 'number', value: addForm.check_interval, onChange: (e) => updateAddForm('check_interval', parseInt(e.target.value) || 7200), min: 300, className: 'w-full bg-white border border-slate-300 rounded-lg px-3 py-2 text-sm text-slate-800 focus:outline-none focus:border-blue-500' })
              ),
              !parseResult.already_subscribed && h('div', { className: 'grid grid-cols-2 gap-3' },
                h('div', { className: 'flex items-center gap-2' },
                  h('input', { type: 'checkbox', checked: addForm.skip_nfo, onChange: (e) => updateAddForm('skip_nfo', e.target.checked), className: 'rounded border-slate-300' }),
                  h('label', { className: 'text-sm text-slate-600' }, '跳过 NFO')
                ),
                h('div', { className: 'flex items-center gap-2' },
                  h('input', { type: 'checkbox', checked: addForm.skip_poster, onChange: (e) => updateAddForm('skip_poster', e.target.checked), className: 'rounded border-slate-300' }),
                  h('label', { className: 'text-sm text-slate-600' }, '跳过封面')
                )
              )
            ),
            h('div', { className: 'flex justify-end gap-2 pt-2' },
              h(Button, { onClick: resetAddModal, variant: 'ghost', size: 'md' }, '取消'),
              h(Button, { onClick: handleAdd, disabled: adding || !newURL.trim() || !!(parseResult && parseResult.already_subscribed), size: 'md' },
                adding ? '添加中...' : (parseResult && parseResult.already_subscribed) ? '已订阅' : '确认添加'
              )
            )
        ),  // 关闭抖音大 Tab

        // === Pornhub Tab ===
        addPlatform === 'pornhub' && h('div', { className: 'space-y-4' },
          h('div', null,
            h('label', { className: 'text-sm text-slate-600 mb-1' }, 'Pornhub 博主链接'),
            h('div', { className: 'text-xs text-slate-400 mb-1.5' }, '支持博主主页：如 https://www.pornhub.com/model/xxx 或 /pornstar/xxx'),
            h('div', { className: 'flex gap-2' },
              h('input', {
                type: 'text', value: newURL,
                placeholder: 'https://www.pornhub.com/model/xxx',
                onChange: (e) => { setNewURL(e.target.value); setParseResult(null); },
                onKeyDown: (e) => e.key === 'Enter' && handleParse(),
                className: 'flex-1 bg-slate-50 border border-slate-300 rounded-lg px-3 py-2 text-sm text-slate-800 placeholder-slate-400 focus:outline-none focus:border-blue-500'
              }),
              h(Button, { onClick: handleParse, disabled: parsing || !newURL.trim(), size: 'md', variant: 'secondary' }, parsing ? '解析中...' : '解析')
            )
          ),
          parseResult && h('div', { className: 'bg-slate-50 border border-slate-200 rounded-lg px-4 py-3 space-y-3' },
            h('div', { className: 'flex items-center gap-2' },
              h(Badge, { variant: 'error' }, 'Pornhub'),
              parseResult.uploader && h('span', { className: 'text-xs text-slate-500' }, parseResult.uploader),
              parseResult.already_subscribed && h(Badge, { variant: 'success' }, '✓ 已订阅')
            ),
            parseResult.already_subscribed && h('div', { className: 'text-xs text-emerald-600 bg-emerald-50 rounded-lg px-3 py-2' },
              '该订阅源已存在，无需重复添加。'
            ),
            !parseResult.already_subscribed && h('div', null,
              h('label', { className: 'text-sm text-slate-600 mb-1' }, '显示名称'),
              h('input', { type: 'text', value: addForm.name, onChange: (e) => updateAddForm('name', e.target.value), className: 'w-full bg-white border border-slate-300 rounded-lg px-3 py-2 text-sm text-slate-800 focus:outline-none focus:border-blue-500' })
            ),
            !parseResult.already_subscribed && h('div', { className: 'flex items-center justify-between' },
              h('label', { className: 'text-sm text-slate-600' }, '启用'),
              h('button', {
                onClick: () => updateAddForm('enabled', !addForm.enabled),
                className: cn('w-10 h-6 rounded-full transition-colors', addForm.enabled ? 'bg-blue-500' : 'bg-slate-300')
              }, h('div', { className: cn('w-4 h-4 rounded-full bg-white transition-transform mx-1', addForm.enabled ? 'translate-x-4' : 'translate-x-0') }))
            ),
            !parseResult.already_subscribed && h('div', null,
              h('label', { className: 'text-sm text-slate-600 mb-1' }, '检查间隔（秒）'),
              h('input', { type: 'number', value: addForm.check_interval, onChange: (e) => updateAddForm('check_interval', parseInt(e.target.value) || 3600), min: 600, className: 'w-full bg-white border border-slate-300 rounded-lg px-3 py-2 text-sm text-slate-800 focus:outline-none focus:border-blue-500' })
            ),
            !parseResult.already_subscribed && h('div', { className: 'grid grid-cols-2 gap-3' },
              h('div', { className: 'flex items-center gap-2' },
                h('input', { type: 'checkbox', checked: addForm.skip_nfo, onChange: (e) => updateAddForm('skip_nfo', e.target.checked), className: 'rounded border-slate-300' }),
                h('label', { className: 'text-sm text-slate-600' }, '跳过 NFO')
              ),
              h('div', { className: 'flex items-center gap-2' },
                h('input', { type: 'checkbox', checked: addForm.skip_poster, onChange: (e) => updateAddForm('skip_poster', e.target.checked), className: 'rounded border-slate-300' }),
                h('label', { className: 'text-sm text-slate-600' }, '跳过封面')
              )
            )
          ),
          h('div', { className: 'flex justify-end gap-2 pt-2' },
            h(Button, { onClick: resetAddModal, variant: 'ghost', size: 'md' }, '取消'),
            h(Button, { onClick: handleAdd, disabled: adding || !newURL.trim() || !!(parseResult && parseResult.already_subscribed), size: 'md' },
              adding ? '添加中...' : (parseResult && parseResult.already_subscribed) ? '已订阅' : '确认添加'
            )
          )
        )  // 关闭 Pornhub 大 Tab
      )
    ),
    // 列表
    loading
      ? h('div', { className: 'grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4' },
          Array.from({ length: 6 }, (_, i) => h(SourceCardSkeleton, { key: i })))
      : sources.length === 0
        // [FIXED: P2-2] action 改为 { label, onClick } 对象形式，而不是 Button 元素
        ? h(EmptyState, { icon: 'rss', message: '还没有订阅源', action: { label: '添加第一个', onClick: () => setShowAdd(true) } })
        : h('div', { className: 'grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4' },
            sources.map(s => h(Card, { key: s.id, hover: true, className: 'group' },
              h('div', { className: 'flex items-start justify-between mb-3' },
                h('div', { className: 'flex-1 min-w-0' },
                  h('h3', { className: 'font-medium text-slate-800 truncate' }, s.name || s.url),
                  h('div', { className: 'flex items-center gap-2 mt-1' },
                    h(Badge, { variant: typeColors[s.type] || 'outline' }, typeLabels[s.type] || s.type),
                    !s.enabled && h(Badge, { variant: 'outline' }, '已禁用')
                  )
                ),
                h('div', { className: 'flex items-center gap-0.5 opacity-100 lg:opacity-0 lg:group-hover:opacity-100 transition-opacity' },
                  h('button', { onClick: () => setEditSource(s), className: 'p-1.5 rounded hover:bg-slate-100 text-slate-500', title: '编辑' }, h(Icon, { name: 'edit', size: 14 })),
                  h('button', { onClick: () => handleSync(s.id), className: 'p-1.5 rounded hover:bg-slate-100 text-slate-500', title: '同步' }, h(Icon, { name: 'sync', size: 14 })),
                  h('button', { onClick: () => handleFullScan(s.id), className: 'p-1.5 rounded hover:bg-slate-100 text-slate-500', title: '全量补漏' }, h(Icon, { name: 'hard-drive', size: 14 })),
                  h('button', { onClick: () => handleDelete(s.id, s.name), className: 'p-1.5 rounded hover:bg-red-50 text-slate-500 hover:text-red-500', title: '删除' }, h(Icon, { name: 'trash', size: 14 }))
                )
              ),
              h('div', { className: 'grid grid-cols-4 gap-2 text-center' },
                h('div', null, h('div', { className: 'text-lg font-bold text-slate-800' }, s.video_count || 0), h('div', { className: 'text-xs text-slate-500' }, '总数')),
                h('div', null, h('div', { className: 'text-lg font-bold text-emerald-600' }, s.completed_count || 0), h('div', { className: 'text-xs text-slate-500' }, '完成')),
                h('div', null, h('div', { className: 'text-lg font-bold text-red-500' }, s.failed_count || 0), h('div', { className: 'text-xs text-slate-500' }, '失败')),
                h('div', null, h('div', { className: 'text-lg font-bold text-amber-600' }, s.pending_count || 0), h('div', { className: 'text-xs text-slate-500' }, '待处理'))
              ),
              // 同步状态信息
              h('div', { className: 'flex flex-wrap items-center gap-3 mt-3 pt-2 border-t border-slate-200 text-xs text-slate-500' },
                s.last_check && h('div', { className: 'flex items-center gap-1', title: '上次检查: ' + new Date(s.last_check).toLocaleString('zh-CN') },
                  h(Icon, { name: 'clock', size: 12 }),
                  formatTimeAgo(s.last_check)
                ),
                s.last_check && s.check_interval && h('div', { className: 'flex items-center gap-1', title: '下次检查' },
                  h(Icon, { name: 'refresh', size: 12 }),
                  formatNextCheck(s.last_check, s.check_interval)
                ),
                !s.last_check && h('div', { className: 'flex items-center gap-1 text-amber-500' },
                  h(Icon, { name: 'clock', size: 12 }),
                  '从未检查'
                ),
                h('div', { className: 'flex-1' }),
                h('button', {
                  onClick: () => handleSync(s.id),
                  disabled: checkingIds.has(s.id),
                  className: 'flex items-center gap-1 text-xs text-slate-500 hover:text-emerald-600 transition-colors flex-shrink-0 disabled:opacity-50 disabled:cursor-not-allowed',
                  title: '立即触发检查'
                },
                  checkingIds.has(s.id)
                    ? h('span', { className: 'inline-block w-3 h-3 border border-slate-300 border-t-emerald-500 rounded-full animate-spin' })
                    : h(Icon, { name: 'refresh', size: 12 }),
                  checkingIds.has(s.id) ? '检查中' : '立即检查'
                ),
                h('button', {
                  onClick: () => onNavigate('videos', { source_id: String(s.id), source_name: s.name || '' }),
                  className: 'flex items-center gap-1 text-xs text-blue-600 hover:text-blue-700 transition-colors flex-shrink-0'
                }, '查看视频', h(Icon, { name: 'chevron-right', size: 12 }))
              )
            ))
          ),
    sourceTotal > 20 && h(Pagination, { page: sourcePage, pageSize: 20, total: sourceTotal, onChange: setSourcePage })
  );
}