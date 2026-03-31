import React from 'react';
import { api } from '../api.js';
import { cn, formatBytes, formatSpeed, formatETA, formatTime, toast, Icon, Card, Button, StatusBadge, Badge, ConfirmDialog } from './utils.js';
const { createElement: h, useState, useEffect, useCallback, Fragment } = React;

// 从文件路径/文件名推断分辨率标签
function inferResolution(filePath) {
  if (!filePath) return null;
  const name = filePath.split('/').pop() || '';
  // 常见分辨率关键词匹配（优先高精度）
  if (/4K|2160p|2160P|uhd|UHD/i.test(name)) return '4K';
  if (/1440p|1440P|2K/i.test(name)) return '2K';
  if (/1080p|1080P|FHD|fhd/i.test(name)) return '1080p';
  if (/720p|720P|HD(?!R)/i.test(name)) return '720p';
  if (/480p|480P|SD/i.test(name)) return '480p';
  if (/360p|360P/i.test(name)) return '360p';
  if (/HDR/i.test(name)) return 'HDR';
  if (/AV1|av1/i.test(name)) return 'AV1';
  if (/HEVC|hevc|H\.265|x265/i.test(name)) return 'HEVC';
  return null;
}

// 格式化秒数为可读时长
function formatDuration(sec) {
  if (!sec || sec <= 0) return '--';
  const hr = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  if (hr > 0) return `${hr}:${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}`;
  return `${m}:${s.toString().padStart(2, '0')}`;
}

// 提取 BV 号用于链接
function extractBVID(videoID) {
  if (!videoID) return null;
  const match = videoID.match(/^(BV[A-Za-z0-9]+)/);
  return match ? match[1] : null;
}

function extractViewKey(videoID) {
  if (!videoID) return null;
  // viewkey 格式：纯字母数字，不是 BV 开头，不是纯数字（抖音）
  if (/^BV/i.test(videoID) || /^\d+$/.test(videoID)) return null;
  if (/^[a-zA-Z0-9_]{6,30}$/.test(videoID)) return videoID;
  return null;
}

export function VideoDetailModal({ video, onClose, onAction }) {
  const [detail, setDetail] = useState(null);
  const [loading, setLoading] = useState(false);
  const [imgError, setImgError] = useState(false);
  const [showPlayer, setShowPlayer] = useState(false);
  const [progress, setProgress] = useState(null); // SSE 实时进度
  const [confirmAction, setConfirmAction] = useState(null); // { title, message, onConfirm }
  const videoRef = React.useRef(null);

  useEffect(() => {
    if (!video) return;
    setImgError(false);
    setShowPlayer(false);
    setProgress(null);
    setLoading(true);
    api.getVideo(video.id)
      .then(res => setDetail(res.data || video))
      .catch(() => setDetail(video))
      .finally(() => setLoading(false));
  }, [video]);

  useEffect(() => {
    if (!video) return;
    const handler = (e) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [video, onClose]);

  // SSE 进度监听（通过全局单例，避免重复建连）
  // [FIXED: P0-1] 依赖改为 [video?.id, video?.video_id]，避免 stale closure 读到旧 video_id
  useEffect(() => {
    if (!video) return;
    const handler = (e) => {
      try {
        const list = e.detail || [];
        const found = list.find(p =>
          (p.download_id && String(p.download_id) === String(video.id)) ||
          (video.video_id && p.bvid && p.bvid === video.video_id)
        );
        setProgress(found || null);
      } catch {}
    };
    window.addEventListener('vsd:progress', handler);
    return () => window.removeEventListener('vsd:progress', handler);
  }, [video?.id, video?.video_id]);

  if (!video) return null;

  const v = detail || video;
  const resolution = inferResolution(v.file_path);
  const bvid = extractBVID(v.video_id);
  const biliURL = bvid ? `https://www.bilibili.com/video/${bvid}` : null;
  const viewKey = extractViewKey(v.video_id);
  const phURL = viewKey ? `https://www.pornhub.com/view_video.php?viewkey=${viewKey}` : null;
  const streamURL = `/api/stream/${v.id}`;
  const canPlay = v.file_path && (v.status === 'completed' || v.status === 'relocated') && v.file_size > 0;
  const isNativePlayable = canPlay && /\.(mp4|m4v|webm|mov)$/i.test(v.file_path);
  const thumbSrc = `/api/thumb/${v.id}`;

  const handleRetry = async () => {
    try { await api.retryVideo(v.id); toast.success('已重试'); if (onAction) onAction(); }
    catch (e) { toast.error(e.message); }
  };
  // [FIXED: P2-8] 替换 confirm() 为 ConfirmDialog
  const handleRedownload = () => {
    setConfirmAction({
      title: '重新下载',
      message: '将删除旧文件并重新下载，确认？',
      onConfirm: async () => {
        setConfirmAction(null);
        try { await api.redownloadVideo(v.id); toast.success('已提交重新下载'); if (onAction) onAction(); }
        catch (e) { toast.error(e.message); }
      }
    });
  };
  const handleDelete = () => {
    setConfirmAction({
      title: '删除视频',
      message: '确定删除？',
      onConfirm: async () => {
        setConfirmAction(null);
        try { await api.deleteVideo(v.id); toast.success('已删除'); if (onAction) onAction(); onClose(); }
        catch (e) { toast.error(e.message); }
      }
    });
  };
  const handleDeleteFiles = () => {
    setConfirmAction({
      title: '删除文件',
      message: '删除本地文件（保留记录）？',
      onConfirm: async () => {
        setConfirmAction(null);
        try { await api.deleteVideoFiles(v.id); toast.success('文件已删除'); if (onAction) onAction(); }
        catch (e) { toast.error(e.message); }
      }
    });
  };
  const handleRestore = () => {
    setConfirmAction({
      title: '恢复视频',
      message: '恢复并重新下载？',
      onConfirm: async () => {
        setConfirmAction(null);
        try { await api.restoreVideo(v.id); toast.success('已恢复'); if (onAction) onAction(); }
        catch (e) { toast.error(e.message); }
      }
    });
  };
  const handleStartDownload = async () => {
    try { await api.redownloadVideo(v.id); toast.success('已触发下载'); if (onAction) onAction(); }
    catch (e) { toast.error(e.message); }
  };

  return h(Fragment, null,
    // [FIXED: P2-8] 确认弹窗
    confirmAction && h(ConfirmDialog, {
      title: confirmAction.title,
      message: confirmAction.message,
      onConfirm: confirmAction.onConfirm,
      onCancel: () => setConfirmAction(null),
    }),
  h('div', {
    className: 'fixed inset-0 bg-black/60 backdrop-blur-sm z-50 flex items-start justify-center pt-[8vh] sm:pt-[12vh]',
    onClick: (e) => { if (e.target === e.currentTarget) onClose(); }
  },
    h('div', {
      className: 'bg-white border border-slate-200 rounded-2xl shadow-2xl w-full max-w-2xl mx-4 overflow-hidden max-h-[80vh] flex flex-col',
      style: { animation: 'slideIn 0.2s ease' }
    },
      // Header
      h('div', { className: 'flex items-center justify-between px-5 py-4 border-b border-slate-200 flex-shrink-0' },
        h('div', { className: 'flex items-center gap-2 min-w-0 flex-1' },
          h(Icon, { name: 'video', size: 18, className: 'text-blue-400 flex-shrink-0' }),
          h('h3', { className: 'font-medium text-slate-800 truncate' }, '视频详情')
        ),
        h('button', { onClick: onClose, className: 'p-1 rounded-lg hover:bg-slate-100 text-slate-500 flex-shrink-0 ml-2' },
          h(Icon, { name: 'x', size: 18 })
        )
      ),

      // Scrollable body
      h('div', { className: 'overflow-y-auto flex-1 p-5 space-y-4' },
        // Cover + Title block (with inline player support)
        showPlayer && canPlay && h('div', { className: 'relative rounded-xl overflow-hidden bg-black mb-2', style: { aspectRatio: '16/9' } },
          h('video', {
            ref: videoRef,
            src: streamURL,
            controls: true,
            autoPlay: true,
            className: 'w-full h-full',
            style: { maxHeight: '50vh' },
            onError: () => { toast.error('播放失败，格式可能不支持'); setShowPlayer(false); }
          }),
          h('button', {
            onClick: () => { setShowPlayer(false); if (videoRef.current) videoRef.current.pause(); },
            className: 'absolute top-2 right-2 p-1 rounded-full bg-black/60 hover:bg-black/80 text-white z-10'
          }, h(Icon, { name: 'x', size: 16 }))
        ),
        h('div', { className: 'flex gap-4' },
          // Thumbnail (hidden when player is shown)
          !showPlayer && !imgError && h('div', { className: 'flex-shrink-0 w-40 sm:w-48 rounded-lg overflow-hidden bg-slate-100 relative group cursor-pointer',
            onClick: canPlay ? () => setShowPlayer(true) : undefined
          },
            h('img', {
              src: thumbSrc,
              className: 'w-full h-auto object-cover rounded-lg',
              referrerPolicy: 'no-referrer',
              loading: 'lazy',
              onError: () => setImgError(true),
              style: { aspectRatio: '16/10' }
            }),
            // Play overlay on thumbnail
            canPlay && h('div', { className: 'absolute inset-0 flex items-center justify-center bg-black/0 group-hover:bg-black/40 transition-colors' },
              h('div', { className: 'w-10 h-10 rounded-full bg-white/90 flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity shadow-lg' },
                h(Icon, { name: 'play', size: 18, className: 'text-slate-900 ml-0.5' })
              )
            )
          ),
          // Title & meta
          h('div', { className: 'flex-1 min-w-0 space-y-2' },
            h('h4', { className: 'text-base font-medium text-slate-900 leading-snug' }, v.title || v.video_id),
            h('div', { className: 'flex items-center gap-2 flex-wrap' },
              h(StatusBadge, { status: v.status }),
              v.source_id === 0 && h(Badge, { variant: 'outline' }, '快速下载')
            ),
            h('div', { className: 'text-sm text-slate-600' }, v.uploader || '--'),
            h('div', { className: 'flex items-center gap-3 text-xs text-slate-500 flex-wrap' },
              v.duration > 0 && h('span', null, '\u23F1 ' + formatDuration(v.duration)),
              v.file_size > 0 && h('span', null, '\uD83D\uDCBE ' + formatBytes(v.file_size)),
              resolution && h('span', { className: 'bg-indigo-500/15 text-indigo-400 px-1.5 py-0.5 rounded text-[10px] font-medium' }, resolution),
              v.video_id && h('span', { className: 'text-slate-600 font-mono' }, v.video_id)
            )
          )
        ),

        // 实时下载进度条（SSE 驱动，仅下载中时显示）
        progress && h(DownloadProgressBar, { progress }),

        // Description
        v.description && h('div', { className: 'bg-slate-50 rounded-lg px-4 py-3' },
          h('div', { className: 'text-xs text-slate-500 mb-1' }, '简介'),
          h('div', { className: 'text-sm text-slate-700 whitespace-pre-line leading-relaxed max-h-24 overflow-y-auto' }, v.description)
        ),

        // Download component indicators
        v.detail_status > 0 && h(DetailStatusBar, { status: v.detail_status }),

        // Info grid
        h('div', { className: 'grid grid-cols-2 gap-3' },
          h(InfoItem, { label: '创建时间', value: formatTime(v.created_at) }),
          h(InfoItem, { label: '下载时间', value: v.downloaded_at ? formatTime(v.downloaded_at) : '--' }),
          h(InfoItem, { label: '重试次数', value: String(v.retry_count || 0) }),
          h(InfoItem, { label: '文件路径', value: v.file_path || '--', mono: true }),
        ),

        // Error info
        (v.error_message || v.last_error) && h('div', { className: 'bg-red-500/5 border border-red-500/20 rounded-lg px-4 py-3' },
          h('div', { className: 'text-xs text-red-400/80 mb-1' }, '错误信息'),
          h('div', { className: 'text-sm text-red-300 font-mono break-all' }, v.error_message || v.last_error)
        ),

        // B站链接
        biliURL && h('a', {
          href: biliURL, target: '_blank', rel: 'noopener noreferrer',
          className: 'inline-flex items-center gap-1.5 text-sm text-blue-400 hover:text-blue-600 transition-colors'
        }, h(Icon, { name: 'external-link', size: 14 }), '在 B 站查看'),
        phURL && h('a', {
          href: phURL, target: '_blank', rel: 'noopener noreferrer',
          className: 'inline-flex items-center gap-1.5 text-sm text-red-400 hover:text-red-600 transition-colors'
        }, h(Icon, { name: 'external-link', size: 14 }), '在 PH 查看'),
      ),

      // Footer actions
      h('div', { className: 'px-5 py-4 border-t border-slate-200 flex items-center gap-2 flex-wrap flex-shrink-0' },
        canPlay && h(Button, {
          onClick: () => setShowPlayer(true),
          size: 'sm',
          disabled: showPlayer
        }, h(Icon, { name: 'play', size: 14 }), showPlayer ? '播放中' : '播放'),
        !isNativePlayable && canPlay && !showPlayer && h('span', { className: 'text-xs text-slate-500' }, 'MKV 格式兼容性因浏览器而异'),
        v.status === 'pending' && h(Button, { onClick: handleStartDownload, size: 'sm' },
          h(Icon, { name: 'download', size: 14 }), '开始下载'
        ),
        (v.status === 'failed' || v.status === 'permanent_failed') && v.status !== 'charge_blocked' &&
          h(Button, { onClick: handleRetry, variant: 'secondary', size: 'sm' },
            h(Icon, { name: 'refresh', size: 14 }), '重试'
          ),
        (v.status === 'completed' || v.status === 'relocated') &&
          h(Button, { onClick: handleRedownload, variant: 'secondary', size: 'sm' },
            h(Icon, { name: 'refresh', size: 14 }), '重新下载'
          ),
        (v.status === 'completed' || v.status === 'relocated') && v.file_size > 0 &&
          h(Button, { onClick: handleDeleteFiles, variant: 'secondary', size: 'sm' },
            h(Icon, { name: 'file-x', size: 14 }), '删除文件'
          ),
        v.status === 'deleted' &&
          h(Button, { onClick: handleRestore, variant: 'secondary', size: 'sm' },
            h(Icon, { name: 'undo', size: 14 }), '恢复'
          ),
        h('div', { className: 'flex-1' }),
        h(Button, { onClick: handleDelete, variant: 'danger', size: 'sm' },
          h(Icon, { name: 'trash', size: 14 }), '删除'
        )
      )
    )
  ));
}

// 信息展示项
function InfoItem({ label, value, mono = false }) {
  return h('div', { className: 'bg-slate-50 rounded-lg px-3 py-2' },
    h('div', { className: 'text-xs text-slate-500 mb-0.5' }, label),
    h('div', { className: cn('text-sm text-slate-700 truncate', mono && 'font-mono text-xs') }, value)
  );
}

// 下载组件状态指示器
// detail_status 位图: 1=封面 2=视频 4=NFO 8=弹幕 16=字幕
function DetailStatusBar({ status }) {
  const components = [
    { bit: 2,  label: '视频', icon: 'video',    color: 'blue' },
    { bit: 1,  label: '封面', icon: 'image',    color: 'purple' },
    { bit: 4,  label: 'NFO',  icon: 'file-text', color: 'emerald' },
    { bit: 8,  label: '弹幕', icon: 'message-square', color: 'amber' },
    { bit: 16, label: '字幕', icon: 'subtitles', color: 'cyan' },
  ];

  return h('div', { className: 'bg-slate-50 rounded-lg px-4 py-3' },
    h('div', { className: 'text-xs text-slate-500 mb-2' }, '下载组件'),
    h('div', { className: 'flex items-center gap-2 flex-wrap' },
      components.map(c => {
        const has = (status & c.bit) !== 0;
        const colorMap = {
          blue:    has ? 'bg-blue-500/15 text-blue-400 border-blue-500/30' : 'bg-slate-100 text-slate-500 border-slate-200',
          purple:  has ? 'bg-purple-500/15 text-purple-400 border-purple-500/30' : 'bg-slate-100 text-slate-500 border-slate-200',
          emerald: has ? 'bg-emerald-500/15 text-emerald-400 border-emerald-500/30' : 'bg-slate-100 text-slate-500 border-slate-200',
          amber:   has ? 'bg-amber-500/15 text-amber-400 border-amber-500/30' : 'bg-slate-100 text-slate-500 border-slate-200',
          cyan:    has ? 'bg-cyan-500/15 text-cyan-400 border-cyan-500/30' : 'bg-slate-100 text-slate-500 border-slate-200',
        };
        return h('span', {
          key: c.bit,
          className: cn('inline-flex items-center gap-1 px-2 py-1 rounded-md text-xs font-medium border transition-colors', colorMap[c.color]),
          title: has ? c.label + ' 已下载' : c.label + ' 未下载'
        },
          h(Icon, { name: has ? 'check' : 'minus', size: 12 }),
          c.label
        );
      })
    )
  );
}

// 实时下载进度条组件（SSE 驱动）
function DownloadProgressBar({ progress: prog }) {
  if (!prog) return null;
  const pct = prog.percent || 0;
  const hasTotal = prog.total > 0;

  return h('div', { className: 'bg-blue-50 border border-blue-200 rounded-lg px-4 py-3 space-y-2' },
    // 标题行 + 百分比
    h('div', { className: 'flex items-center justify-between' },
      h('div', { className: 'flex items-center gap-2' },
        h('div', { className: 'w-2 h-2 rounded-full bg-blue-400 animate-pulse' }),
        h('span', { className: 'text-xs font-medium text-blue-600' }, '下载中')
      ),
      h('span', { className: 'text-sm font-semibold text-blue-700 tabular-nums' }, pct.toFixed(1) + '%')
    ),
    // 进度条
    h('div', { className: 'h-2 bg-slate-200 rounded-full overflow-hidden' },
      h('div', {
        className: 'h-2 rounded-full transition-all duration-500 progress-bar',
        style: {
          width: pct + '%',
          background: 'linear-gradient(90deg, #3b82f6 0%, #60a5fa 60%, #93c5fd 100%)'
        }
      })
    ),
    // 速度 / 大小 / ETA
    h('div', { className: 'flex items-center gap-3 text-xs text-slate-400 flex-wrap' },
      prog.speed > 0 && h('span', { className: 'text-blue-400 font-semibold' }, formatSpeed(prog.speed)),
      hasTotal && h('span', { className: 'tabular-nums' },
        formatBytes(prog.downloaded || 0) + ' / ' + formatBytes(prog.total)
      ),
      formatETA(prog.downloaded, prog.total, prog.speed) && h('span', { className: 'text-slate-500' },
        'ETA ' + formatETA(prog.downloaded, prog.total, prog.speed)
      )
    )
  );
}
