// 纯展示逻辑：格式化与卡片渲染。
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

export function fmtTime(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const pad = (x) => String(x).padStart(2, '0');
  const today = new Date();
  const sameDay = d.toDateString() === today.toDateString();
  const hm = pad(d.getHours()) + ':' + pad(d.getMinutes());
  if (sameDay) return '今天 ' + hm;
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${hm}`;
}

// renderItem 依据模板生成一张卡片，并把事件接到 handlers 上。
export function renderItem(item, handlers) {
  const tpl = document.getElementById('tpl-item');
  const node = tpl.content.firstElementChild.cloneNode(true);

  node.dataset.id = item.id;
  node.classList.toggle('taken', item.taken);
  node.querySelector('.item-user').textContent = item.created_by || '未知';
  node.querySelector('.item-ip').textContent = item.source_ip || '未知来源';
  node.querySelector('.item-time').textContent = fmtTime(item.created_at);
  node.querySelector('.item-taken-tag').hidden = !item.taken;

  node.querySelector('.item-text').textContent = item.text || '';

  const fileList = node.querySelector('.files');
  for (const f of item.files || []) {
    const li = document.createElement('li');
    const name = document.createElement('span');
    name.className = 'fname';
    name.textContent = f.name;

    const meta = document.createElement('span');
    meta.className = 'fmeta';
    meta.textContent = `${fmtBytes(f.size)} · ${(f.sha256 || '').slice(0, 8)} · 下载 ${f.downloads} 次`;

    const link = document.createElement('a');
    link.href = `/api/items/${encodeURIComponent(item.id)}/files/${encodeURIComponent(f.id)}`;
    link.textContent = '下载';
    link.setAttribute('download', f.name);

    const del = document.createElement('button');
    del.type = 'button';
    del.className = 'danger';
    del.textContent = '删除附件';
    del.addEventListener('click', () => handlers.onDeleteFile(item, f));

    li.append(name, meta, link, del);
    fileList.append(li);
  }

  const takenBtn = node.querySelector('.act-taken');
  takenBtn.textContent = item.taken ? '取消取走' : '标记已取走';
  takenBtn.addEventListener('click', () => handlers.onToggleTaken(item));

  node.querySelector('.act-delete').addEventListener('click', () => handlers.onDelete(item));
  return node;
}
