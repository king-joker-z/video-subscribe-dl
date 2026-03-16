import React from 'react';
import { api } from '../api.js';
import { cn, toast, Icon, Card, Button, Badge } from '../components/utils.js';
const { createElement: h, useState, useEffect, useCallback, Fragment } = React;

// Toggle 开关组件
function Toggle({ checked, onChange, disabled = false }) {
  return h('button', {
    type: 'button',
    role: 'switch',
    'aria-checked': checked,
    disabled,
    onClick: () => !disabled && onChange(!checked),
    className: cn(
      'relative inline-flex h-6 w-11 items-center rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500/40',
      checked ? 'bg-blue-500' : 'bg-slate-600',
      disabled && 'opacity-50 cursor-not-allowed'
    )
  },
    h('span', {
      className: cn(
        'inline-block h-4 w-4 transform rounded-full bg-white transition-transform shadow-sm',
        checked ? 'translate-x-6' : 'translate-x-1'
      )
    })
  );
}

// Select 下拉组件
function Select({ value, onChange, options, placeholder = '请选择...' }) {
  return h('select', {
    value: value || '',
    onChange: (e) => onChange(e.target.value),
    className: 'flex-1 bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-blue-500 appearance-none cursor-pointer'
  },
    h('option', { value: '' }, placeholder),
    options.map(opt =>
      h('option', { key: opt.value, value: opt.value }, opt.label)
    )
  );
}

// Number 输入组件
function NumberInput({ value, onChange, placeholder, min, max, step }) {
  return h('input', {
    type: 'number',
    value: value || '',
    placeholder,
    min, max, step: step || 1,
    onChange: (e) => onChange(e.target.value),
    className: 'flex-1 bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-200 placeholder-slate-600 focus:outline-none focus:border-blue-500 [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none'
  });
}

export function SettingsPage() {
  const [settings, setSettings] = useState({});
  const [credential, setCredential] = useState(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [showQR, setShowQR] = useState(false);
  const [qrData, setQrData] = useState(null);
  const [dirty, setDirty] = useState({});
  const [testingNotify, setTestingNotify] = useState(false);
  const [douyinCookieInput, setDouyinCookieInput] = useState('');
  const [douyinCookieStatus, setDouyinCookieStatus] = useState(null);
  const [validatingDouyinCookie, setValidatingDouyinCookie] = useState(false);
  const [savingDouyinCookie, setSavingDouyinCookie] = useState(false);
  const [templatePreview, setTemplatePreview] = useState('');
  const previewTimer = React.useRef(null);

  const load = useCallback(async () => {
    try {
      const [sRes, cRes, dyRes] = await Promise.all([api.getSettings(), api.getCredential(), api.getDouyinCookieStatus()]);
      setSettings(sRes.data || {});
      setCredential(cRes.data || {});
      setDouyinCookieStatus(dyRes.data || null);
    } catch (e) { toast.error(e.message); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleChange = (key, value) => {
    setDirty(d => ({ ...d, [key]: value }));
  };

  // 文件名模板预览
  const handleTemplateChange = (value) => {
    handleChange('filename_template', value);
    if (previewTimer.current) clearTimeout(previewTimer.current);
    previewTimer.current = setTimeout(async () => {
      if (!value) { setTemplatePreview(''); return; }
      try {
        const res = await api.previewTemplate(value);
        setTemplatePreview((res.data && res.data.preview) || '');
      } catch { setTemplatePreview(''); }
    }, 500);
  };

  const handleSave = async () => {
    if (Object.keys(dirty).length === 0) return;
    setSaving(true);
    try {
      await api.updateSettings(dirty);
      toast.success('设置已保存');
      setDirty({});
      load();
    } catch (e) { toast.error(e.message); }
    finally { setSaving(false); }
  };

  const handleQRLogin = async () => {
    try {
      const res = await api.generateQRCode();
      setQrData(res.data);
      setShowQR(true);
      const key = res.data.qrcode_key;
      const poll = setInterval(async () => {
        try {
          const pr = await api.pollQRCode(key);
          if (pr.data.status === 0) {
            clearInterval(poll);
            setShowQR(false);
            toast.success('登录成功' + (pr.data.username ? ': ' + pr.data.username : ''));
            load();
          } else if (pr.data.status === -2) {
            clearInterval(poll);
            setShowQR(false);
            toast.error('二维码已过期');
          }
        } catch {}
      }, 2000);
      setTimeout(() => { clearInterval(poll); setShowQR(false); }, 180000);
    } catch (e) { toast.error(e.message); }
  };

  const handleTestNotify = async () => {
    setTestingNotify(true);
    try {
      const res = await api.testNotification();
      toast.success(res.data?.message || '测试通知已发送');
    } catch (e) { toast.error(e.message); }
    finally { setTestingNotify(false); }
  };
  const handleRefreshCred = async () => {
    try {
      await api.refreshCredential();
      toast.success('凭证已刷新');
      load();
    } catch (e) { toast.error(e.message); }
  };

  const getValue = (key) => dirty[key] !== undefined ? dirty[key] : (settings[key] || '');
  const getBool = (key) => {
    const v = getValue(key);
    return v === 'true' || v === true;
  };
  const getNotifyType = () => getValue('notify_type');

  // 通用行布局
  const RowLayout = ({ label, help, children }) =>
    h('div', { className: 'flex flex-col sm:flex-row sm:items-center gap-2 py-3 border-b border-slate-700/30 last:border-b-0' },
      h('div', { className: 'sm:w-48 flex-shrink-0' },
        h('label', { className: 'text-sm font-medium text-slate-300' }, label),
        help && h('div', { className: 'text-xs text-slate-500 mt-0.5' }, help)
      ),
      h('div', { className: 'flex-1 flex items-center' }, children)
    );

  // 文本输入行
  const TextRow = ({ label, keyName, type = 'text', placeholder = '', help = '' }) =>
    h(RowLayout, { label, help },
      h('input', {
        type, value: getValue(keyName), placeholder,
        onChange: (e) => handleChange(keyName, e.target.value),
        className: 'flex-1 bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-200 placeholder-slate-600 focus:outline-none focus:border-blue-500'
      })
    );

  // 数字输入行
  const NumRow = ({ label, keyName, placeholder = '', help = '', min, max, step }) =>
    h(RowLayout, { label, help },
      h(NumberInput, { value: getValue(keyName), onChange: (v) => handleChange(keyName, v), placeholder, min, max, step })
    );

  // Toggle 行
  const ToggleRow = ({ label, keyName, help = '' }) =>
    h(RowLayout, { label, help },
      h('div', { className: 'flex items-center gap-3' },
        h(Toggle, { checked: getBool(keyName), onChange: (v) => handleChange(keyName, v ? 'true' : 'false') }),
        h('span', { className: 'text-xs text-slate-500' }, getBool(keyName) ? '已开启' : '已关闭')
      )
    );

  // Select 行
  const SelectRow = ({ label, keyName, options, placeholder = '请选择...', help = '' }) =>
    h(RowLayout, { label, help },
      h(Select, { value: getValue(keyName), onChange: (v) => handleChange(keyName, v), options, placeholder })
    );

  if (loading) return h('div', { className: 'page-enter space-y-4' },
    Array.from({ length: 4 }, (_, i) => h(Card, { key: i }, h('div', { className: 'skeleton h-32 rounded-lg' })))
  );

  const hasDirty = Object.keys(dirty).length > 0;

  return h('div', { className: 'page-enter space-y-6' },
    h('div', { className: 'flex items-center justify-between' },
      h('h2', { className: 'text-lg font-semibold' }, '设置'),
      hasDirty && h('div', { className: 'flex items-center gap-2' },
        h('span', { className: 'text-xs text-amber-400 flex items-center gap-1' },
          h(Icon, { name: 'alert-circle', size: 12 }), Object.keys(dirty).length + ' 项未保存'
        ),
        h(Button, { onClick: handleSave, disabled: saving, size: 'sm' }, saving ? '保存中...' : '保存更改')
      )
    ),

    // B 站账号
    h(Card, null,
      h('h3', { className: 'font-medium text-slate-200 mb-4 flex items-center gap-2' },
        h(Icon, { name: 'qr-code', size: 18, className: 'text-blue-400' }), 'B 站账号'
      ),
      credential && credential.has_credential
        ? h('div', { className: 'space-y-3' },
            h('div', { className: 'flex items-center gap-3' },
              h(Badge, { variant: 'success' }, '已登录'),
              credential.username && h('span', { className: 'text-sm text-slate-300' }, credential.username),
              credential.vip_label && h(Badge, { variant: 'warning' }, credential.vip_label)
            ),
            credential.need_refresh && h('div', { className: 'text-xs text-amber-400 flex items-center gap-1' },
              h(Icon, { name: 'alert-circle', size: 14 }), '凭证需要刷新'
            ),
            h('div', { className: 'text-xs text-slate-500' },
              '来源: ' + (credential.source || '--') + (credential.updated_at ? ' | 更新: ' + credential.updated_at : '')
            ),
            h('div', { className: 'flex gap-2 mt-3' },
              h(Button, { onClick: handleRefreshCred, variant: 'secondary', size: 'sm' }, h(Icon, { name: 'refresh', size: 14 }), '刷新凭证'),
              h(Button, { onClick: handleQRLogin, variant: 'outline', size: 'sm' }, h(Icon, { name: 'qr-code', size: 14 }), '重新扫码')
            )
          )
        : h('div', { className: 'space-y-3' },
            h('div', { className: 'flex items-center gap-2' },
              h(Badge, { variant: 'outline' }, '未登录'),
              h('span', { className: 'text-sm text-slate-500' }, '下载限制 480p')
            ),
            h(Button, { onClick: handleQRLogin, size: 'sm' }, h(Icon, { name: 'qr-code', size: 14 }), '扫码登录')
          )
    ),

    // 扫码弹窗
    showQR && h('div', { className: 'fixed inset-0 bg-black/60 z-50 flex items-center justify-center', onClick: () => setShowQR(false) },
      h('div', { className: 'bg-slate-800 rounded-xl p-6 max-w-sm mx-4', onClick: function(e) { e.stopPropagation(); } },
        h('h3', { className: 'font-medium text-center mb-4' }, '打开 B 站 App 扫码登录'),
        qrData && qrData.url
          ? h('div', { className: 'flex justify-center mb-4' },
              h('img', {
                src: 'https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=' + encodeURIComponent(qrData.url),
                className: 'w-48 h-48 rounded-lg bg-white p-2', alt: 'QR Code'
              })
            )
          : h('div', { className: 'skeleton w-48 h-48 mx-auto rounded-lg' }),
        h('p', { className: 'text-center text-sm text-slate-400' }, '等待扫码...'),
        h('div', { className: 'flex justify-center mt-4' },
          h(Button, { onClick: () => setShowQR(false), variant: 'ghost', size: 'sm' }, '取消')
        )
      )
    ),

    // 抖音 Cookie 管理
    h(Card, null,
      h('h3', { className: 'font-medium text-slate-200 mb-4 flex items-center gap-2' },
        h(Icon, { name: 'settings', size: 18, className: 'text-blue-400' }), '抖音 Cookie'
      ),
      h('div', { className: 'space-y-3' },
        // 状态显示
        douyinCookieStatus && h('div', { className: 'flex items-center gap-2 mb-2' },
          h(Badge, { variant: douyinCookieStatus.mode === 'user' ? 'success' : 'outline' },
            douyinCookieStatus.mode === 'user' ? '用户 Cookie' : '自动生成'
          ),
          h('span', { className: 'text-xs text-slate-500' },
            douyinCookieStatus.mode === 'user'
              ? '使用用户配置的浏览器 Cookie，翻页更稳定'
              : '使用自动生成的 Cookie，翻页可能被拦截'
          )
        ),
        // Cookie 输入框
        h('div', { className: 'space-y-1.5' },
          h('label', { className: 'text-sm text-slate-400' }, '浏览器 Cookie'),
          h('textarea', {
            value: douyinCookieInput,
            onChange: (e) => setDouyinCookieInput(e.target.value),
            placeholder: '从浏览器开发者工具复制抖音的 Cookie，例如: msToken=xxx; ttwid=xxx; sessionid=xxx; ...',
            rows: 3,
            className: 'w-full bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-200 placeholder-slate-600 focus:outline-none focus:border-blue-500 font-mono resize-none'
          })
        ),
        h('div', { className: 'text-xs text-slate-500' },
          '打开抖音网页版 → F12 开发者工具 → Network 标签 → 复制任意请求的 Cookie 请求头'
        ),
        // 操作按钮
        h('div', { className: 'flex gap-2 mt-2' },
          h(Button, {
            onClick: async () => {
              if (!douyinCookieInput.trim()) { toast.error('请输入 Cookie'); return; }
              setValidatingDouyinCookie(true);
              try {
                const res = await api.validateDouyinCookie(douyinCookieInput.trim());
                if (res.data && res.data.valid) {
                  toast.success('Cookie 验证通过: ' + (res.data.message || ''));
                } else {
                  toast.error('Cookie 验证失败: ' + (res.data && res.data.message || '无效'));
                }
              } catch (e) { toast.error(e.message); }
              finally { setValidatingDouyinCookie(false); }
            },
            disabled: validatingDouyinCookie || !douyinCookieInput.trim(),
            variant: 'secondary', size: 'sm'
          }, validatingDouyinCookie ? '验证中...' : '验证 Cookie'),
          h(Button, {
            onClick: async () => {
              if (!douyinCookieInput.trim()) { toast.error('请输入 Cookie'); return; }
              setSavingDouyinCookie(true);
              try {
                await api.updateSettings({ douyin_cookie: douyinCookieInput.trim() });
                toast.success('抖音 Cookie 已保存');
                setDouyinCookieInput('');
                load();
              } catch (e) { toast.error(e.message); }
              finally { setSavingDouyinCookie(false); }
            },
            disabled: savingDouyinCookie || !douyinCookieInput.trim(),
            size: 'sm'
          }, savingDouyinCookie ? '保存中...' : '保存 Cookie'),
          douyinCookieStatus && douyinCookieStatus.has_user_cookie && h(Button, {
            onClick: async () => {
              try {
                await api.updateSettings({ douyin_cookie: '' });
                toast.success('已清除用户 Cookie，恢复自动生成模式');
                setDouyinCookieInput('');
                load();
              } catch (e) { toast.error(e.message); }
            },
            variant: 'ghost', size: 'sm'
          }, '清除 Cookie')
        )
      )
    ),

    // 下载设置
    h(Card, null,
      h('h3', { className: 'font-medium text-slate-200 mb-4 flex items-center gap-2' },
        h(Icon, { name: 'download', size: 18, className: 'text-blue-400' }), '下载设置'
      ),
      h(SelectRow, {
        label: '下载画质', keyName: 'download_quality',
        placeholder: '最高画质（默认）',
        options: [
          { value: '4K', label: '4K (2160P)' },
          { value: '1080p60', label: '1080P 60帧' },
          { value: '1080p+', label: '1080P 高码率' },
          { value: '1080p', label: '1080P' },
          { value: '720p', label: '720P' },
          { value: '480p', label: '480P' },
          { value: '360p', label: '360P' },
        ]
      }),
      h(SelectRow, {
        label: '视频编码', keyName: 'download_codec',
        help: '全局默认视频编码偏好',
        placeholder: '自动选择（默认）',
        options: [
          { value: 'avc', label: 'AVC (H.264) — 兼容性最好' },
          { value: 'hevc', label: 'HEVC (H.265) — 体积更小' },
          { value: 'av1', label: 'AV1 — 最高压缩比' },
        ]
      }),
      h(NumRow, { label: '下载并发数', keyName: 'max_concurrent', placeholder: '3', help: '同时下载的视频数（热更新生效）', min: 1, max: 10 }),
      h(NumRow, { label: '下载分片数', keyName: 'download_chunks', placeholder: '3', help: '每个视频并行分片数', min: 1, max: 16 }),
      h(NumRow, { label: '速度限制 (MB/s)', keyName: 'max_download_speed_mb', placeholder: '0 = 无限制', min: 0, step: 0.5 }),
      h(NumRow, { label: '最小磁盘空间 (GB)', keyName: 'min_disk_free_gb', placeholder: '5', help: '低于此值停止下载', min: 1 }),
      // 文件名模板（带预览）
      h(RowLayout, { label: '文件名模板', help: '变量: Title, BvID, UploaderName, Quality, Codec, PubDate, PartIndex, PartTitle' },
        h('div', { className: 'flex-1 space-y-1.5' },
          h('input', {
            type: 'text',
            value: getValue('filename_template'),
            placeholder: '{{.Title}} [{{.BvID}}]',
            onChange: (e) => handleTemplateChange(e.target.value),
            className: 'w-full bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-200 placeholder-slate-600 focus:outline-none focus:border-blue-500 font-mono'
          }),
          (templatePreview || getValue('filename_template')) && h('div', { className: 'text-xs text-slate-500 flex items-center gap-1.5' },
            h('span', { className: 'text-slate-600' }, '预览:'),
            h('span', { className: 'text-slate-400 font-mono' }, templatePreview || '...')
          )
        )
      )
    ),

    // 调度与高级
    h(Card, null,
      h('h3', { className: 'font-medium text-slate-200 mb-4 flex items-center gap-2' },
        h(Icon, { name: 'settings', size: 18, className: 'text-blue-400' }), '调度与高级'
      ),
      h(NumRow, { label: '检查间隔 (分钟)', keyName: 'check_interval_minutes', placeholder: '30', min: 1 }),
      h(TextRow, { label: 'Cron 调度表达式', keyName: 'schedule_cron', placeholder: '例: 0 */30 * * * *', help: '留空使用固定间隔。格式: 秒 分 时 日 月 周。需重启生效' }),
      h(ToggleRow, { label: '下载弹幕', keyName: 'download_danmaku', help: '同步下载弹幕文件（ASS 格式）' }),
      h(ToggleRow, { label: '充电视频尝试下载', keyName: 'try_upower', help: '开启后不跳过充电专属视频' }),
      h(SelectRow, {
        label: 'NFO 格式', keyName: 'nfo_type',
        placeholder: 'Kodi（默认）',
        options: [
          { value: 'kodi', label: 'Kodi' },
          { value: 'emby', label: 'Emby / Jellyfin' },
        ]
      }),
      h(SelectRow, {
        label: 'NFO 日期类型', keyName: 'nfo_time_type',
        help: '收藏夹场景可用 favtime（收藏时间）',
        placeholder: '发布时间（默认）',
        options: [
          { value: 'pubdate', label: '发布时间 (pubdate)' },
          { value: 'favtime', label: '收藏时间 (favtime)' },
        ]
      })
    ),

    // 性能调优
    h(Card, null,
      h('h3', { className: 'font-medium text-slate-200 mb-4 flex items-center gap-2' },
        h(Icon, { name: 'hard-drive', size: 18, className: 'text-blue-400' }), '性能与限流'
      ),
      h(NumRow, { label: '请求限速 (次/分钟)', keyName: 'rate_limit_per_minute', placeholder: '200', help: 'API 速率限制（热更新生效）', min: 10 }),
      h(NumRow, { label: '请求间隔 (秒)', keyName: 'request_interval', placeholder: '30', help: '两次请求之间的最小间隔', min: 1 }),
      h(NumRow, { label: '风控冷却 (分钟)', keyName: 'cooldown_minutes', placeholder: '30', help: '触发风控后冷却时间（热更新生效）', min: 5 }),
      h(NumRow, { label: '视频并发数', keyName: 'concurrent_video', placeholder: '3', help: '同时处理的视频数（需重启生效）', min: 1, max: 10 }),
      h(NumRow, { label: '分P并发数', keyName: 'concurrent_page', placeholder: '2', help: '每个视频的分P并行数（需重启生效）', min: 1, max: 5 })
    ),

    // 存储管理
    h(Card, null,
      h('h3', { className: 'font-medium text-slate-200 mb-4 flex items-center gap-2' },
        h(Icon, { name: 'hard-drive', size: 18, className: 'text-blue-400' }), '存储管理'
      ),
      h(NumRow, { label: '保留天数', keyName: 'retention_days', placeholder: '0 = 永久保留', help: '超过天数的视频文件自动清理', min: 0 }),
      h(ToggleRow, { label: '低磁盘自动清理', keyName: 'auto_cleanup_on_low_disk', help: '磁盘空间不足时自动清理旧视频' })
    ),

    // 通知设置 — 条件显示
    h(Card, null,
      h('h3', { className: 'font-medium text-slate-200 mb-4 flex items-center gap-2' },
        h(Icon, { name: 'alert-circle', size: 18, className: 'text-blue-400' }), '通知设置'
      ),
      h(SelectRow, {
        label: '通知类型', keyName: 'notify_type',
        placeholder: '关闭通知',
        options: [
          { value: 'webhook', label: 'Webhook' },
          { value: 'telegram', label: 'Telegram Bot' },
          { value: 'bark', label: 'Bark (iOS)' },
        ]
      }),
      // Webhook 配置
      getNotifyType() === 'webhook' && h(Fragment, null,
        h(TextRow, { label: 'Webhook URL', keyName: 'webhook_url', placeholder: 'https://your-webhook-url...' })
      ),
      // Telegram 配置
      getNotifyType() === 'telegram' && h(Fragment, null,
        h(TextRow, { label: 'Bot Token', keyName: 'telegram_bot_token', type: 'password', placeholder: '123456:ABC-DEF...' }),
        h(TextRow, { label: 'Chat ID', keyName: 'telegram_chat_id', placeholder: '数字 Chat ID' })
      ),
      // Bark 配置
      getNotifyType() === 'bark' && h(Fragment, null,
        h(TextRow, { label: 'Bark Server', keyName: 'bark_server', placeholder: 'https://api.day.app 或自建地址' }),
        h(TextRow, { label: 'Bark Key', keyName: 'bark_key', type: 'password', placeholder: 'Device Key' })
      ),
      // 通用通知开关（任何通知类型选中后显示）
      getNotifyType() && h('div', { className: 'mt-3 pt-3 border-t border-slate-700/30' },
        h('div', { className: 'text-xs text-slate-500 mb-2' }, '通知事件'),
        h(ToggleRow, { label: '下载完成', keyName: 'notify_on_complete' }),
        h(ToggleRow, { label: '下载失败', keyName: 'notify_on_error' }),
        h(ToggleRow, { label: 'Cookie 过期', keyName: 'notify_on_cookie_expire' }),
        h(ToggleRow, { label: '同步完成', keyName: 'notify_on_sync' })
        ,h('div', { className: 'mt-3 pt-3 border-t border-slate-700/30 flex items-center justify-between' },
          h('span', { className: 'text-xs text-slate-500' }, '验证通知配置是否正常工作'),
          h(Button, {
            onClick: handleTestNotify,
            disabled: testingNotify || hasDirty,
            variant: 'secondary', size: 'sm'
          }, h(Icon, { name: 'bell', size: 14 }), testingNotify ? '发送中...' : '发送测试通知')
        )
      )
    ),

    // 底部保存按钮（浮动式）
    hasDirty && h('div', { className: 'sticky bottom-4 flex justify-end' },
      h('div', { className: 'bg-slate-800/95 backdrop-blur-sm border border-slate-700/50 rounded-xl px-4 py-3 shadow-2xl flex items-center gap-3' },
        h('span', { className: 'text-sm text-amber-400' }, Object.keys(dirty).length + ' 项更改未保存'),
        h(Button, { onClick: function() { setDirty({}); }, variant: 'ghost', size: 'sm' }, '放弃'),
        h(Button, { onClick: handleSave, disabled: saving, size: 'sm' }, saving ? '保存中...' : '保存更改')
      )
    )
  );
}
