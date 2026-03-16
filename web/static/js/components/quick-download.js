import React from 'react';
import { api } from '../api.js';
import { cn, toast, Icon, Button, Card, StatusBadge } from './utils.js';
const { createElement: h, useState, useCallback, useEffect, useRef, Fragment } = React;

// 格式化秒数为 MM:SS
function formatDuration(sec) {
  if (!sec || sec <= 0) return '--';
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return `${m}:${s.toString().padStart(2, '0')}`;
}

// 格式化大数字
function formatCount(n) {
  if (!n) return '0';
  if (n >= 10000) return (n / 10000).toFixed(1) + '万';
  return n.toLocaleString();
}

// 检测文本中是否包含 bilibili 链接或 BV/AV 号
export function extractBiliUrl(text) {
  if (!text || typeof text !== 'string') return '';
  const trimmed = text.trim();
  // bilibili.com 视频链接
  if (/bilibili\.com\/video\/(BV[\w]+|av\d+)/i.test(trimmed)) return trimmed;
  // b23.tv 短链接
  if (/b23\.tv\/[\w]+/i.test(trimmed)) return trimmed;
  // 纯 BV 号
  if (/^BV[\w]{10}$/i.test(trimmed)) return trimmed;
  // 纯 AV 号
  if (/^av\d+$/i.test(trimmed)) return trimmed;
  // 从混合文本中提取 bilibili 链接
  const urlMatch = trimmed.match(/https?:\/\/(?:www\.)?bilibili\.com\/video\/(BV[\w]+|av\d+)[^\s]*/i);
  if (urlMatch) return urlMatch[0];
  const shortMatch = trimmed.match(/https?:\/\/b23\.tv\/[\w]+/i);
  if (shortMatch) return shortMatch[0];
  // 从混合文本中提取 BV 号
  const bvMatch = trimmed.match(/(BV[\w]{10})/i);
  if (bvMatch) return bvMatch[1];
  return '';
}

// 检测文本中是否包含抖音链接
export function extractDouyinUrl(text) {
  if (!text || typeof text !== 'string') return '';
  const trimmed = text.trim();
  // v.douyin.com 短链接
  if (/v\.douyin\.com\/[\w]+/i.test(trimmed)) {
    const m = trimmed.match(/https?:\/\/v\.douyin\.com\/[\w]+\/?/i);
    return m ? m[0] : trimmed;
  }
  // www.douyin.com/video/xxx
  if (/douyin\.com\/video\/\d+/i.test(trimmed)) {
    const m = trimmed.match(/https?:\/\/(?:www\.)?douyin\.com\/video\/\d+[^\s]*/i);
    return m ? m[0] : trimmed;
  }
  // www.iesdouyin.com/share/video/xxx
  if (/iesdouyin\.com\/share\/video\/\d+/i.test(trimmed)) {
    const m = trimmed.match(/https?:\/\/(?:www\.)?iesdouyin\.com\/share\/video\/\d+[^\s]*/i);
    return m ? m[0] : trimmed;
  }
  // 从混合文本中提取
  const shortM = trimmed.match(/https?:\/\/v\.douyin\.com\/[\w]+\/?/i);
  if (shortM) return shortM[0];
  const fullM = trimmed.match(/https?:\/\/(?:www\.)?douyin\.com\/video\/\d+[^\s]*/i);
  if (fullM) return fullM[0];
  return '';
}

// 综合检测：B站 or 抖音
export function extractVideoUrl(text) {
  return extractBiliUrl(text) || extractDouyinUrl(text);
}

// 检测平台类型
function detectPlatform(url) {
  if (!url) return null;
  if (/douyin\.com|iesdouyin\.com/i.test(url)) return 'douyin';
  if (/bilibili\.com|b23\.tv|^BV|^av\d/i.test(url)) return 'bilibili';
  return null;
}

export function QuickDownloadDialog({ open, onClose, initialUrl = '' }) {
  const [url, setUrl] = useState('');
  const [preview, setPreview] = useState(null);
  const [loading, setLoading] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const inputRef = useRef(null);
  const autoPreviewDone = useRef(false);

  // 打开时聚焦输入框，如果有 initialUrl 则自动填入
  useEffect(() => {
    if (open) {
      const startUrl = initialUrl || '';
      setUrl(startUrl);
      setPreview(null);
      autoPreviewDone.current = false;
      setTimeout(() => inputRef.current?.focus(), 100);
    }
  }, [open, initialUrl]);

  // 自动预览：当有 initialUrl 时自动触发解析
  useEffect(() => {
    if (open && initialUrl && !autoPreviewDone.current && !loading && !preview) {
      autoPreviewDone.current = true;
      (async () => {
        setLoading(true);
        try {
          const res = await api.previewDownload(initialUrl.trim());
          setPreview(res.data);
        } catch (e) {
          toast.error('解析失败: ' + e.message);
        } finally {
          setLoading(false);
        }
      })();
    }
  }, [open, initialUrl, loading, preview]);

  // ESC 关闭
  useEffect(() => {
    if (!open) return;
    const handler = (e) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [open, onClose]);

  const handlePreview = useCallback(async () => {
    if (!url.trim()) return;
    setLoading(true);
    setPreview(null);
    try {
      const res = await api.previewDownload(url.trim());
      setPreview(res.data);
    } catch (e) {
      toast.error(e.message);
    } finally {
      setLoading(false);
    }
  }, [url]);

  const handleDownload = useCallback(async () => {
    if (!url.trim()) return;
    setDownloading(true);
    try {
      const res = await api.quickDownload(url.trim());
      const d = res.data;
      if (d.exists) {
        toast.info(d.message);
      } else {
        toast.success(d.message || '已提交下载');
      }
      onClose();
    } catch (e) {
      toast.error(e.message);
    } finally {
      setDownloading(false);
    }
  }, [url, onClose]);

  const handleKeyDown = (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      if (preview) {
        handleDownload();
      } else {
        handlePreview();
      }
    }
  };

  if (!open) return null;

  const platform = preview?.platform || detectPlatform(url);
  const isDouyin = platform === 'douyin';

  return h('div', {
    className: 'fixed inset-0 bg-black/60 backdrop-blur-sm z-50 flex items-start justify-center pt-[15vh]',
    onClick: (e) => { if (e.target === e.currentTarget) onClose(); }
  },
    h('div', {
      className: 'bg-slate-800 border border-slate-700/50 rounded-2xl shadow-2xl w-full max-w-lg mx-4 overflow-hidden',
      style: { animation: 'slideIn 0.2s ease' }
    },
      // Header
      h('div', { className: 'flex items-center justify-between px-5 py-4 border-b border-slate-700/30' },
        h('div', { className: 'flex items-center gap-2' },
          h(Icon, { name: 'download', size: 18, className: 'text-blue-400' }),
          h('h3', { className: 'font-medium text-slate-200' }, '快速下载')
        ),
        h('button', { onClick: onClose, className: 'p-1 rounded-lg hover:bg-slate-700 text-slate-400' },
          h(Icon, { name: 'x', size: 18 })
        )
      ),

      // Body
      h('div', { className: 'p-5 space-y-4' },
        // Input
        h('div', { className: 'flex gap-2' },
          h('input', {
            ref: inputRef,
            type: 'text',
            value: url,
            onChange: (e) => { setUrl(e.target.value); setPreview(null); },
            onKeyDown: handleKeyDown,
            placeholder: '粘贴 B站/抖音 视频链接...',
            className: 'flex-1 bg-slate-900 border border-slate-700 rounded-lg px-3 py-2.5 text-sm text-slate-200 placeholder-slate-500 focus:outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500/30'
          }),
          h(Button, {
            onClick: preview ? handleDownload : handlePreview,
            disabled: !url.trim() || loading || downloading,
            size: 'md'
          },
            loading ? h(Fragment, null, h('span', { className: 'animate-spin inline-block' }, '⏳'), ' 解析中')
            : downloading ? h(Fragment, null, h('span', { className: 'animate-spin inline-block' }, '⏳'), ' 提交中')
            : preview ? h(Fragment, null, h(Icon, { name: 'download', size: 16 }), preview.is_note ? ' 下载图集' : ' 下载')
            : h(Fragment, null, h(Icon, { name: 'search', size: 16 }), ' 解析')
          )
        ),

        // Tips
        !preview && !loading && h('div', { className: 'text-xs text-slate-500 space-y-1' },
          h('div', null, '支持格式:'),
          h('div', { className: 'text-slate-600 space-y-0.5 ml-2' },
            h('div', null, '• bilibili.com/video/BVxxxxxx'),
            h('div', null, '• b23.tv/xxxxxx（B站短链接）'),
            h('div', null, '• BV 号或 AV 号（如 BV1xx411c7mD、av12345）'),
            h('div', { className: 'mt-1 text-pink-600/80' }, '• v.douyin.com/xxxxxx（抖音分享链接）'),
            h('div', { className: 'text-pink-600/80' }, '• douyin.com/video/xxxxxx（抖音视频链接）')
          )
        ),

        // Preview Card
        preview && h(Card, { className: 'bg-slate-900/50' },
          h('div', { className: 'flex gap-3' },
            // 封面
            preview.pic && h('div', { className: 'flex-shrink-0 w-32 h-20 rounded-lg overflow-hidden bg-slate-800' },
              h('img', {
                src: preview.pic.replace('http:', 'https:'),
                className: 'w-full h-full object-cover',
                referrerPolicy: 'no-referrer',
                onError: (e) => { e.target.style.display = 'none'; }
              })
            ),
            // 信息
            h('div', { className: 'flex-1 min-w-0 space-y-1' },
              h('div', { className: 'text-sm font-medium text-slate-200 line-clamp-2' }, preview.title),
              h('div', { className: 'text-xs text-slate-400 flex items-center gap-2 flex-wrap' },
                h('span', null, preview.uploader),
                // 平台标识
                isDouyin && h('span', { className: 'text-xs px-1.5 py-0.5 rounded bg-pink-500/15 text-pink-400' }, '抖音'),
                !isDouyin && preview.tname && h(Fragment, null,
                  h('span', { className: 'text-slate-600' }, '·'),
                  h('span', null, preview.tname)
                ),
                h('span', { className: 'text-slate-600' }, '·'),
                h('span', null, formatDuration(preview.duration))
              ),
              h('div', { className: 'text-xs text-slate-500 flex items-center gap-3 flex-wrap' },
                // B站统计
                !isDouyin && h(Fragment, null,
                  h('span', null, '▶ ' + formatCount(preview.stat?.view)),
                  h('span', null, '👍 ' + formatCount(preview.stat?.like)),
                  h('span', null, '💰 ' + formatCount(preview.stat?.coin)),
                  h('span', null, '⭐ ' + formatCount(preview.stat?.favorite)),
                ),
                // 抖音统计
                isDouyin && h(Fragment, null,
                  h('span', null, '❤️ ' + formatCount(preview.stat?.like)),
                  h('span', null, '💬 ' + formatCount(preview.stat?.comment)),
                  h('span', null, '🔗 ' + formatCount(preview.stat?.share)),
                ),
              ),
              !isDouyin && preview.pages > 1 && h('div', { className: 'text-xs text-blue-400' }, `${preview.pages} 个分P`)
            )
          ),

          // 状态标记
          (preview.is_charge_plus || preview.is_bangumi || preview.is_unavailable || preview.existing_status || preview.is_note) &&
            h('div', { className: 'flex gap-2 mt-3 flex-wrap' },
              preview.is_charge_plus && h('span', { className: 'text-xs px-2 py-0.5 rounded-full bg-yellow-500/15 text-yellow-400' }, '充电专属'),
              preview.is_bangumi && h('span', { className: 'text-xs px-2 py-0.5 rounded-full bg-purple-500/15 text-purple-400' }, '番剧'),
              preview.is_unavailable && h('span', { className: 'text-xs px-2 py-0.5 rounded-full bg-red-500/15 text-red-400' }, '不可用'),
              preview.is_note && h('span', { className: 'text-xs px-2 py-0.5 rounded-full bg-orange-500/15 text-orange-400' }, '图集' + (preview.image_count ? ' · ' + preview.image_count + '张' : '')),
              preview.existing_status && h('span', { className: 'text-xs px-2 py-0.5 rounded-full bg-emerald-500/15 text-emerald-400' }, '已' + preview.existing_status)
            )
        )
      ),

      // Footer hint
      h('div', { className: 'px-5 py-3 border-t border-slate-700/30 text-xs text-slate-600 flex items-center justify-between' },
        h('span', null, 'Enter 快速操作 · Esc 关闭 · Ctrl+D 开关'),
        preview && h('span', { className: 'text-slate-500' }, preview.bvid || preview.aweme_id || '')
      )
    )
  );
}

// 全局拖拽区域指示器
export function DropZoneOverlay({ active }) {
  if (!active) return null;
  return h('div', {
    className: 'fixed inset-0 z-[60] bg-blue-500/10 backdrop-blur-sm flex items-center justify-center pointer-events-none',
    style: { animation: 'fadeIn 0.15s ease' }
  },
    h('div', {
      className: 'bg-slate-800/95 border-2 border-dashed border-blue-400 rounded-2xl p-12 text-center shadow-2xl'
    },
      h(Icon, { name: 'download', size: 48, className: 'text-blue-400 mx-auto mb-4' }),
      h('div', { className: 'text-lg font-medium text-slate-200 mb-1' }, '松开以下载视频'),
      h('div', { className: 'text-sm text-slate-400' }, '支持 B站/抖音 视频链接')
    )
  );
}

// 快速下载 FAB 按钮
export function QuickDownloadFAB({ onClick }) {
  return h('button', {
    onClick,
    title: '快速下载视频 (Ctrl+D)',
    className: 'fixed bottom-6 right-6 w-14 h-14 rounded-full bg-blue-500 hover:bg-blue-400 text-white shadow-lg shadow-blue-500/25 flex items-center justify-center transition-all hover:scale-105 active:scale-95 z-40'
  },
    h(Icon, { name: 'download', size: 24 })
  );
}
