import React from 'react';
import { api } from '../api.js';
import { cn, formatBytes, formatTime, toast, Icon, Card, Button, StatusBadge, Badge } from './utils.js';
const { createElement: h, useState, useEffect, useCallback, Fragment } = React;

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

export function VideoDetailModal({ video, onClose, onAction }) {
  const [detail, setDetail] = useState(null);
  const [loading, setLoading] = useState(false);
  const [imgError, setImgError] = useState(false);
  const [showPlayer, setShowPlayer] = useState(false);
  const videoRef = React.useRef(null);

  useEffect(() => {
    if (!video) return;
    setImgError(false);
    setShowPlayer(false);
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

  if (!video) return null;

  const v = detail || video;
  const bvid = extractBVID(v.video_id);
  const biliURL = bvid ? `https://www.bilibili.com/video/${bvid}` : null;
  const streamURL = `/api/stream/${v.id}`;
  const canPlay = v.file_path && (v.status === 'completed' || v.status === 'relocated') && v.file_size > 0;
  const isNativePlayable = canPlay && /\.(mp4|m4v|webm|mov)$/i.test(v.file_path);
  const thumbSrc = `/api/thumb/${v.id}`;

  const handleRetry = async () => {
    try { await api.retryVideo(v.id); toast.success('已重试'); if (onAction) onAction(); }
    catch (e) { toast.error(e.message); }
  };
  const handleRedownload = async () => {
    if (!confirm('将删除旧文件并重新下载，确认？')) return;
    try { await api.redownloadVideo(v.id); toast.success('已提交重新下载'); if (onAction) onAction(); }
    catch (e) { toast.error(e.message); }
  };
  const handleDelete = async () => {
    if (!confirm('确定删除？')) return;
    try { await api.deleteVideo(v.id); toast.success('已删除'); onClose(); if (onAction) onAction(); }
    catch (e) { toast.error(e.message); }
  };
  const handleDeleteFiles = async () => {
    if (!confirm('删除本地文件（保留记录）？')) return;
    try { await api.deleteVideoFiles(v.id); toast.success('文件已删除'); if (onAction) onAction(); }
    catch (e) { toast.error(e.message); }
  };
  const handleRestore = async () => {
    if (!confirm('恢复并重新下载？')) return;
    try { await api.restoreVideo(v.id); toast.success('已恢复'); if (onAction) onAction(); }
    catch (e) { toast.error(e.message); }
  };
  const handleStartDownload = async () => {
    try { await api.redownloadVideo(v.id); toast.success('已触发下载'); if (onAction) onAction(); }
    catch (e) { toast.error(e.message); }
  };

  return h('div', {
    className: 'fixed inset-0 bg-black/60 backdrop-blur-sm z-50 flex items-start justify-center pt-[8vh] sm:pt-[12vh]',
    onClick: (e) => { if (e.target === e.currentTarget) onClose(); }
  },
    h('div', {
      className: 'bg-slate-800 border border-slate-700/50 rounded-2xl shadow-2xl w-full max-w-2xl mx-4 overflow-hidden max-h-[80vh] flex flex-col',
      style: { animation: 'slideIn 0.2s ease' }
    },
      // Header
      h('div', { className: 'flex items-center justify-between px-5 py-4 border-b border-slate-700/30 flex-shrink-0' },
        h('div', { className: 'flex items-center gap-2 min-w-0 flex-1' },
          h(Icon, { name: 'video', size: 18, className: 'text-blue-400 flex-shrink-0' }),
          h('h3', { className: 'font-medium text-slate-200 truncate' }, '视频详情')
        ),
        h('button', { onClick: onClose, className: 'p-1 rounded-lg hover:bg-slate-700 text-slate-400 flex-shrink-0 ml-2' },
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
          !showPlayer && !imgError && h('div', { className: 'flex-shrink-0 w-40 sm:w-48 rounded-lg overflow-hidden bg-slate-900 relative group cursor-pointer',
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
            h('h4', { className: 'text-base font-medium text-slate-100 leading-snug' }, v.title || v.video_id),
            h('div', { className: 'flex items-center gap-2 flex-wrap' },
              h(StatusBadge, { status: v.status }),
              v.source_id === 0 && h(Badge, { variant: 'outline' }, '快速下载')
            ),
            h('div', { className: 'text-sm text-slate-400' }, v.uploader || '--'),
            h('div', { className: 'flex items-center gap-3 text-xs text-slate-500 flex-wrap' },
              v.duration > 0 && h('span', null, '\u23F1 ' + formatDuration(v.duration)),
              v.file_size > 0 && h('span', null, '\uD83D\uDCBE ' + formatBytes(v.file_size)),
              v.video_id && h('span', { className: 'text-slate-600 font-mono' }, v.video_id)
            )
          )
        ),

        // Description
        v.description && h('div', { className: 'bg-slate-900/50 rounded-lg px-4 py-3' },
          h('div', { className: 'text-xs text-slate-500 mb-1' }, '简介'),
          h('div', { className: 'text-sm text-slate-300 whitespace-pre-line leading-relaxed max-h-24 overflow-y-auto' }, v.description)
        ),

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
          className: 'inline-flex items-center gap-1.5 text-sm text-blue-400 hover:text-blue-300 transition-colors'
        }, h(Icon, { name: 'external-link', size: 14 }), '在 B 站查看'),
      ),

      // Footer actions
      h('div', { className: 'px-5 py-4 border-t border-slate-700/30 flex items-center gap-2 flex-wrap flex-shrink-0' },
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
  );
}

// 信息展示项
function InfoItem({ label, value, mono = false }) {
  return h('div', { className: 'bg-slate-900/30 rounded-lg px-3 py-2' },
    h('div', { className: 'text-xs text-slate-500 mb-0.5' }, label),
    h('div', { className: cn('text-sm text-slate-300 truncate', mono && 'font-mono text-xs') }, value)
  );
}
