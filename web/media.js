// media.js：附件预览分类（图片/视频）与全屏查看（lightbox）。
// 与服务端 internal/httpapi/preview.go 的白名单保持一致；服务端白名单更宽
// （还包含音频），但界面只做图片与视频预览，其余仍走附件下载。
import { iconEl } from './ui.js';
import { authedURL } from './env.js';

const IMAGE_EXT = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'avif', 'bmp', 'ico', 'svg']);
const VIDEO_EXT = new Set(['mp4', 'm4v', 'webm', 'ogv', 'mov', 'mkv']);

function ext(name) {
  const s = String(name || '');
  const i = s.lastIndexOf('.');
  return i < 0 ? '' : s.slice(i + 1).toLowerCase();
}

// previewKind 返回 'image' | 'video' | ''（不支持预览）。
export function previewKind(name) {
  const e = ext(name);
  if (IMAGE_EXT.has(e)) return 'image';
  if (VIDEO_EXT.has(e)) return 'video';
  return '';
}

// mediaSrc 是内联预览地址（服务端按 ?inline=1 返回正确 MIME 并支持 Range）。
export function mediaSrc(itemId, fileId) {
  return authedURL(fileURL(itemId, fileId) + '?inline=1');
}

// downloadSrc 是原始附件地址（Content-Disposition: attachment）。
export function downloadSrc(itemId, fileId) {
  return authedURL(fileURL(itemId, fileId));
}

// zipSrc 是整条内容打包下载地址。
export function zipSrc(itemId) {
  return authedURL(`/api/items/${encodeURIComponent(itemId)}/zip`);
}

function fileURL(itemId, fileId) {
  return `/api/items/${encodeURIComponent(itemId)}/files/${encodeURIComponent(fileId)}`;
}

// ---------- 全屏预览 ----------

let active = null;

export function openLightbox({ src, name, kind, href, opener }) {
  closeLightbox();

  const root = document.createElement('div');
  root.className = 'lb';
  root.setAttribute('role', 'dialog');
  root.setAttribute('aria-modal', 'true');
  root.setAttribute('aria-label', name || '预览');

  const stage = document.createElement('div');
  stage.className = 'lb-stage';

  if (kind === 'video') {
    const video = document.createElement('video');
    video.src = src;
    video.controls = true;
    video.autoplay = true;
    video.playsInline = true;
    video.className = 'lb-media';
    stage.append(video);
  } else {
    const img = document.createElement('img');
    img.src = src;
    img.alt = name || '';
    img.className = 'lb-media';
    stage.append(img);
  }

  const bar = document.createElement('div');
  bar.className = 'lb-bar';

  const label = document.createElement('span');
  label.className = 'lb-name';
  label.textContent = name || '';

  const dl = document.createElement('a');
  dl.className = 'lb-btn';
  dl.href = href;
  dl.setAttribute('download', name || '');
  dl.title = '下载原文件';
  dl.append(iconEl('download'), document.createTextNode('下载'));

  const close = document.createElement('button');
  close.type = 'button';
  close.className = 'lb-btn';
  close.title = '关闭（Esc）';
  close.setAttribute('aria-label', '关闭预览');
  close.append(iconEl('close'), document.createTextNode('关闭'));

  bar.append(label, dl, close);
  root.append(stage, bar);
  document.body.append(root);
  document.body.classList.add('lb-open');

  const onKey = (ev) => {
    if (ev.key === 'Escape') {
      ev.preventDefault();
      ev.stopPropagation();
      closeLightbox();
    }
  };
  document.addEventListener('keydown', onKey, true);
  root.addEventListener('click', (ev) => {
    // 点空白处关闭；点媒体本体不关闭
    if (ev.target === root || ev.target === stage) closeLightbox();
  });
  close.addEventListener('click', closeLightbox);

  active = { root, onKey, opener };
  requestAnimationFrame(() => close.focus());
}

export function closeLightbox() {
  if (!active) return;
  const { root, onKey, opener } = active;
  active = null;
  document.removeEventListener('keydown', onKey, true);
  document.body.classList.remove('lb-open');
  for (const video of root.querySelectorAll('video')) {
    try {
      video.pause();
    } catch {
      /* 忽略 */
    }
  }
  root.remove();
  if (opener && document.contains(opener)) opener.focus();
}
