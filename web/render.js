// 纯展示逻辑：格式化与卡片渲染。
import { paintAvatar, iconEl } from './ui.js';

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

  const hm = pad(d.getHours()) + ':' + pad(d.getMinutes());
  const yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1);
  const sameDay = d.toDateString() === now.toDateString();
  if (sameDay) return '今天 ' + hm;
  if (d.toDateString() === yesterday.toDateString()) return '昨天 ' + hm;
  if (d.getFullYear() === now.getFullYear()) {
    return `${d.getMonth() + 1} 月 ${d.getDate()} 日 ${hm}`;
  }
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${hm}`;
}

function fmtAbsolute(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
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
  node.querySelector('.memo-text').textContent = item.text || '';

  const fileList = node.querySelector('.files');
  const files = [...(item.files || [])].sort((a, b) =>
    (a.path || a.name).localeCompare(b.path || b.name));

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
