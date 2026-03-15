import React from 'react';
import { api } from '../api.js';
import { cn, toast, Icon, Card, Button, Badge } from '../components/utils.js';
const { createElement: h, useState, useEffect, useCallback, Fragment } = React;

export function SettingsPage() {
  const [settings, setSettings] = useState({});
  const [credential, setCredential] = useState(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [showQR, setShowQR] = useState(false);
  const [qrData, setQrData] = useState(null);
  const [dirty, setDirty] = useState({});

  const load = useCallback(async () => {
    try {
      const [sRes, cRes] = await Promise.all([api.getSettings(), api.getCredential()]);
      setSettings(sRes.data || {});
      setCredential(cRes.data || {});
    } catch (e) { toast.error(e.message); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleChange = (key, value) => {
    setDirty(d => ({ ...d, [key]: value }));
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
      // 开始轮询
      const key = res.data.qrcode_key;
      const poll = setInterval(async () => {
        try {
          const pr = await api.pollQRCode(key);
          if (pr.data.status === 0) { // 成功
            clearInterval(poll);
            setShowQR(false);
            toast.success('登录成功' + (pr.data.username ? ': ' + pr.data.username : ''));
            load();
          } else if (pr.data.status === -2) { // 过期
            clearInterval(poll);
            setShowQR(false);
            toast.error('二维码已过期');
          }
        } catch {}
      }, 2000);
      // 3 分钟后停止轮询
      setTimeout(() => { clearInterval(poll); setShowQR(false); }, 180000);
    } catch (e) { toast.error(e.message); }
  };

  const handleRefreshCred = async () => {
    try {
      await api.refreshCredential();
      toast.success('凭证已刷新');
      load();
    } catch (e) { toast.error(e.message); }
  };

  const getValue = (key) => dirty[key] !== undefined ? dirty[key] : (settings[key] || '');

  const SettingRow = ({ label, keyName, type = 'text', placeholder = '', help = '' }) =>
    h('div', { className: 'flex flex-col sm:flex-row sm:items-center gap-2 py-3 border-b border-slate-700/30' },
      h('div', { className: 'sm:w-48 flex-shrink-0' },
        h('label', { className: 'text-sm font-medium text-slate-300' }, label),
        help && h('div', { className: 'text-xs text-slate-500 mt-0.5' }, help)
      ),
      h('input', {
        type, value: getValue(keyName), placeholder,
        onChange: (e) => handleChange(keyName, e.target.value),
        className: 'flex-1 bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-200 placeholder-slate-600 focus:outline-none focus:border-blue-500'
      })
    );

  if (loading) return h('div', { className: 'page-enter space-y-4' },
    Array.from({ length: 4 }, (_, i) => h(Card, { key: i }, h('div', { className: 'skeleton h-32 rounded-lg' })))
  );

  return h('div', { className: 'page-enter space-y-6' },
    h('div', { className: 'flex items-center justify-between' },
      h('h2', { className: 'text-lg font-semibold' }, '设置'),
      Object.keys(dirty).length > 0 && h(Button, { onClick: handleSave, disabled: saving, size: 'sm' }, saving ? '保存中...' : '保存更改')
    ),

    // B 站账号
    h(Card, null,
      h('h3', { className: 'font-medium text-slate-200 mb-4 flex items-center gap-2' },
        h(Icon, { name: 'qr-code', size: 18, className: 'text-blue-400' }), 'B 站账号'
      ),
      credential?.has_credential
        ? h('div', { className: 'space-y-3' },
            h('div', { className: 'flex items-center gap-3' },
              h(Badge, { variant: 'success' }, '已登录'),
              credential.username && h('span', { className: 'text-sm text-slate-300' }, credential.username),
              credential.vip_label && h(Badge, { variant: 'warning' }, credential.vip_label),
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
      h('div', { className: 'bg-slate-800 rounded-xl p-6 max-w-sm mx-4', onClick: e => e.stopPropagation() },
        h('h3', { className: 'font-medium text-center mb-4' }, '打开 B 站 App 扫码登录'),
        qrData?.url
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

    // 下载设置
    h(Card, null,
      h('h3', { className: 'font-medium text-slate-200 mb-4 flex items-center gap-2' },
        h(Icon, { name: 'download', size: 18, className: 'text-blue-400' }), '下载设置'
      ),
      h(SettingRow, { label: '下载画质', keyName: 'download_quality', placeholder: '留空使用最高画质' }),
      h(SettingRow, { label: '视频编码', keyName: 'download_codec', placeholder: 'all / avc / hevc / av1', help: '全局默认视频编码偏好' }),
      h(SettingRow, { label: '下载并发数', keyName: 'max_concurrent', placeholder: '默认 3', help: '同时下载的视频数（热更新生效）' }),
      h(SettingRow, { label: '下载分片数', keyName: 'download_chunks', placeholder: '默认 3', help: '每个视频并行分片数' }),
      h(SettingRow, { label: '速度限制 (MB/s)', keyName: 'max_download_speed_mb', placeholder: '0 = 无限制' }),
      h(SettingRow, { label: '最小磁盘空间 (GB)', keyName: 'min_disk_free_gb', placeholder: '默认 5', help: '低于此值停止下载' }),
      h(SettingRow, { label: '文件名模板', keyName: 'filename_template', placeholder: '{{.Title}} [{{.BvID}}]', help: '可用变量: Title, BvID, UploaderName, Quality, Codec, PubDate, PartIndex, PartTitle' }),
    ),

    // 检查间隔
    h(Card, null,
      h('h3', { className: 'font-medium text-slate-200 mb-4 flex items-center gap-2' },
        h(Icon, { name: 'settings', size: 18, className: 'text-blue-400' }), '高级设置'
      ),
      h(SettingRow, { label: '检查间隔 (分钟)', keyName: 'check_interval_minutes', placeholder: '默认 30' }),
      h(SettingRow, { label: 'NFO 格式', keyName: 'nfo_type', placeholder: 'kodi 或 emby' }),
      h(SettingRow, { label: '下载弹幕', keyName: 'download_danmaku', placeholder: 'true/false' }),
      h(SettingRow, { label: '请求限速 (次/分钟)', keyName: 'rate_limit_per_minute', placeholder: '200', help: 'API 速率限制（热更新生效）' }),
      h(SettingRow, { label: '请求间隔 (秒)', keyName: 'request_interval', placeholder: '30', help: '两次请求之间的最小间隔' }),
      h(SettingRow, { label: '风控冷却 (分钟)', keyName: 'cooldown_minutes', placeholder: '30', help: '触发风控后冷却时间（热更新生效）' }),
      h(SettingRow, { label: 'Cron 调度表达式', keyName: 'schedule_cron', placeholder: '例: 0 */30 * * * *', help: '留空使用固定间隔。格式: 秒 分 时 日 月 周。需重启生效' }),
      h(SettingRow, { label: 'NFO 日期类型', keyName: 'nfo_time_type', placeholder: 'pubdate 或 favtime', help: '收藏夹场景可用 favtime（收藏时间）。暂未完整实现' }),
      h(SettingRow, { label: '充电视频尝试下载', keyName: 'try_upower', placeholder: 'true/false', help: '设为 true 时不跳过充电专属视频' }),
      h(SettingRow, { label: '视频并发数', keyName: 'concurrent_video', placeholder: '默认 3', help: '同时处理的视频数（需重启生效）' }),
      h(SettingRow, { label: '分P并发数', keyName: 'concurrent_page', placeholder: '默认 2', help: '每个视频的分P并行数（需重启生效）' }),
    ),

    // 通知设置
    h(Card, null,
      h('h3', { className: 'font-medium text-slate-200 mb-4' }, '通知设置'),
      h(SettingRow, { label: '通知类型', keyName: 'notify_type', placeholder: 'webhook / telegram / bark' }),
      h(SettingRow, { label: 'Webhook URL', keyName: 'webhook_url', placeholder: 'https://...' }),
      h(SettingRow, { label: 'Telegram Bot Token', keyName: 'telegram_bot_token', type: 'password' }),
      h(SettingRow, { label: 'Telegram Chat ID', keyName: 'telegram_chat_id' }),
      h(SettingRow, { label: 'Bark Server', keyName: 'bark_server' }),
      h(SettingRow, { label: 'Bark Key', keyName: 'bark_key', type: 'password' }),
    ),

    // 保存按钮
    Object.keys(dirty).length > 0 && h('div', { className: 'flex justify-end' },
      h(Button, { onClick: handleSave, disabled: saving, size: 'md' }, saving ? '保存中...' : '保存更改')
    )
  );
}
