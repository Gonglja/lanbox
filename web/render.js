// 纯展示逻辑：格式化与卡片渲染。
import { paintAvatar, iconEl } from './ui.js';
import { renderMarkdown } from './markdown.js';
import { previewKind, mediaSrc, downloadSrc, openLightbox } from './media.js';

export function fmtBytes(n) {
  if (n < 1024) return n + ' B';
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return v.toFixed(v >= 10 ? 0 : 1) + ' ' + units[i];
}

function pad(x) {
  return String(x).padStart(2, '0');
}

// fmtTime 输出相对时间（memos 风格），绝对时间放在 title 里。
function fmtTime(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const now = new Date();
  const diff = (now.getTime() - d.getTime()) / 1000;

  if (diff >= 0 && diff < 60) return '刚刚';
  if (diff >= 0 && diff < 3600) return `${Math.floor(diff / 60)} 分钟前`;
  if (diff >= 0 && diff < 86400) return `${Math.floor(diff / 3600)} 小时前`;

  // 超过一天时日期已由分组行给出，卡片里只留时分。
  return pad(d.getHours()) + ':' + pad(d.getMinutes());
}

function fmtAbsolute(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

// dayKey 用本地日期分组，避免跨零点的内容被拆到两天。
export function dayKey(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return String(iso || '');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

function fmtDay(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '未知日期';
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const that = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  const days = Math.round((today - that) / 86400000);
  if (days === 0) return '今天';
  if (days === 1) return '昨天';
  if (d.getFullYear() === now.getFullYear()) return `${d.getMonth() + 1} 月 ${d.getDate()} 日`;
  return `${d.getFullYear()} 年 ${d.getMonth() + 1} 月 ${d.getDate()} 日`;
}

// renderDay 生成时间线的日期分隔行；同一组只插一次。
export function renderDay(iso) {
  const row = document.createElement('div');
  row.className = 'day-row';
  const label = document.createElement('span');
  label.className = 'day-label';
  label.textContent = fmtDay(iso);
  row.append(label);
  return row;
}

// refreshTimes 只刷新相对时间文本，不重建卡片（避免打断 hover/focus）。
export function refreshTimes(root = document) {
  for (const el of root.querySelectorAll('time.memo-time[datetime]')) {
    el.textContent = fmtTime(el.getAttribute('datetime'));
  }
}

// renderItem 依据模板生成一张卡片，并把事件接到 handlers 上。
export function renderItem(item, handlers) {
  const tpl = document.getElementById('tpl-memo');
  const node = tpl.content.firstElementChild.cloneNode(true);

  node.dataset.id = item.id;
  node.classList.toggle('is-taken', Boolean(item.taken));

  const author = item.created_by || '未知';
  paintAvatar(node.querySelector('.memo-avatar'), author);
  node.querySelector('.memo-author').textContent = author;

  const ipEl = node.querySelector('.memo-ip');
  if (item.source_ip) {
    ipEl.textContent = item.source_ip;
    ipEl.hidden = false;
  }

  const time = node.querySelector('.memo-time');
  time.textContent = fmtTime(item.created_at);
  time.title = fmtAbsolute(item.created_at);
  time.dateTime = item.created_at || '';

  node.querySelector('.memo-taken').hidden = !item.taken;

  const textEl = node.querySelector('.memo-text');
  if (item.text) renderMarkdown(textEl, item.text);

  const fileList = node.querySelector('.files');
  const files = [...(item.files || [])].sort((a, b) =>
    (a.path || a.name).localeCompare(b.path || b.name));

  // 图片/视频先出一行预览，文件列表仍保留（下载、删除、打包都从那里走）。
  const mediaBox = node.querySelector('.media');
  for (const f of files) {
    const tile = mediaTile(item, f);
    if (tile) mediaBox.append(tile);
  }
  mediaBox.hidden = mediaBox.childElementCount === 0;

  for (const f of files) {
    const rel = f.path || f.name;
    const dir = rel.includes('/') ? rel.slice(0, rel.lastIndexOf('/')) : '';

    const li = document.createElement('li');

    if (dir) {
      const dirTag = document.createElement('span');
      dirTag.className = 'fdir';
      dirTag.textContent = dir + '/';
      dirTag.title = dir + '/';
      li.append(dirTag);
    }

    const name = document.createElement('span');
    name.className = 'fname';
    name.textContent = f.name;
    name.title = rel;

    const meta = document.createElement('span');
    meta.className = 'fmeta';
    meta.textContent = `${fmtBytes(f.size)} · 下载 ${f.downloads} 次`;
    meta.title = `SHA-256 ${f.sha256 || '-'}`;

    const acts = document.createElement('span');
    acts.className = 'fact';

    const link = document.createElement('a');
    link.className = 'btn btn-sm btn-icon';
    link.href = `/api/items/${encodeURIComponent(item.id)}/files/${encodeURIComponent(f.id)}`;
    link.setAttribute('download', f.name);
    link.title = '下载';
    link.setAttribute('aria-label', `下载 ${f.name}`);
    link.append(iconEl('download'));

    const del = document.createElement('button');
    del.type = 'button';
    del.className = 'btn btn-sm btn-icon btn-danger';
    del.title = '删除附件';
    del.setAttribute('aria-label', `删除附件 ${f.name}`);
    del.append(iconEl('trash'));
    del.addEventListener('click', () => handlers.onDeleteFile(item, f));

    acts.append(link, del);
    li.append(name, meta, acts);
    fileList.append(li);
  }

  const needsZip = files.length > 1 || files.some((f) => (f.path || f.name).includes('/'));
  if (needsZip) {
    const wrap = document.createElement('div');
    wrap.className = 'zip-row';
    const zip = document.createElement('a');
    zip.className = 'zip-btn';
    zip.href = `/api/items/${encodeURIComponent(item.id)}/zip`;
    zip.setAttribute('download', `lanbox-${item.id}.zip`);
    zip.append(iconEl('archive'), document.createTextNode('下载全部 (.zip)'));
    wrap.append(zip);
    fileList.after(wrap);
  }

  const takenBtn = node.querySelector('.act-taken');
  takenBtn.textContent = '';
  takenBtn.append(
    iconEl(item.taken ? 'undo' : 'check'),
    document.createTextNode(item.taken ? '取消取走' : '标记已取走'),
  );
  takenBtn.addEventListener('click', () => handlers.onToggleTaken(item));

  const delBtn = node.querySelector('.act-delete');
  delBtn.textContent = '';
  delBtn.append(iconEl('trash'), document.createTextNode('删除'));
  delBtn.addEventListener('click', () => handlers.onDelete(item));

  return node;
}

// mediaTile 生成一个图片缩略图或视频首帧卡片；不支持的扩展名返回 null。
function mediaTile(item, f) {
  const kind = previewKind(f.name);
  if (!kind) return null;

  const src = mediaSrc(item.id, f.id);
  const label = f.path || f.name;

  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'media-item';
  btn.title = (kind === 'image' ? '查看图片：' : '播放视频：') + label;
  btn.setAttribute('aria-label', btn.title);

  let media;
  if (kind === 'image') {
    media = document.createElement('img');
    media.loading = 'lazy';
    media.decoding = 'async';
    media.alt = f.name;
    media.src = src;
  } else {
    media = document.createElement('video');
    media.src = src;
    media.preload = 'metadata';
    media.muted = true;
    media.playsInline = true;
    media.tabIndex = -1; // 点击交给外层按钮，避免原生控件抢事件
    media.setAttribute('aria-hidden', 'true');
  }

  // 预览失败（类型被服务端回退/文件损坏）时退化成不可点的小提示，仍可从文件列表下载。
  media.addEventListener('error', () => {
    btn.classList.add('is-broken');
    btn.disabled = true;
    btn.title = '无法预览：' + label;
  });

  btn.append(media);

  if (kind === 'video') {
    const badge = document.createElement('span');
    badge.className = 'media-badge';
    badge.append(iconEl('play'));
    btn.append(badge);
  }

  btn.addEventListener('click', () => {
    openLightbox({
      src,
      name: label,
      kind,
      href: downloadSrc(item.id, f.id),
      opener: btn,
    });
  });

  return btn;
}
