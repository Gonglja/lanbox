// status.js：状态页。数据来自 GET /api/stats，页面打开时每 5 秒自动刷新。
import { api } from './api.js';
import { fmtBytes } from './render.js';
import { fillURLOptions, renderQR } from './connect.js';
import { openPanel } from './ui.js';

const $ = (id) => document.getElementById(id);
const REFRESH_MS = 5000;

let timer = 0;
let inFlight = false;

// fmtUptime 与 CLI status 的输出保持一致：3d4h / 5h6m / 7m / 8s。
function fmtUptime(sec) {
  if (!Number.isFinite(sec) || sec < 0) sec = 0;
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = Math.floor(sec % 60);
  if (d > 0 && h > 0) return `${d}d${h}h`;
  if (d > 0) return `${d}d`;
  if (h > 0 && m > 0) return `${h}h${m}m`;
  if (h > 0) return `${h}h`;
  if (m > 0) return `${m}m`;
  return `${s}s`;
}

// mdnsText 把 mDNS 状态压成一行，口径与 lanbox-cli status 相同。
function mdnsText(m) {
  if (!m) return '未启用';
  if (!m.enabled) return '未启用（配置关闭）';
  if (!m.running) return m.error ? `未运行：${m.error}` : '未运行';
  const target = m.host ? (m.port ? `${m.host}:${m.port}` : m.host) : '已运行';
  const addrs = m.addrs && m.addrs.length ? `（${m.addrs.join('，')}）` : '';
  return target + addrs;
}

function diskValue(disk) {
  if (disk && typeof disk.free_bytes === 'number') {
    return { value: fmtBytes(disk.free_bytes) };
  }
  if (disk && disk.error) return { value: `未知（${disk.error}）`, error: true };
  return { value: '未知' };
}

function row(label, value, opts = {}) {
  const div = document.createElement('div');
  div.className = 'status-row';
  const dt = document.createElement('dt');
  dt.textContent = label;
  const dd = document.createElement('dd');
  dd.textContent = value;
  if (opts.mono) dd.classList.add('is-mono');
  if (opts.error) dd.classList.add('is-error');
  div.append(dt, dd);
  return div;
}

function card(title, rows) {
  const box = document.createElement('section');
  box.className = 'status-card';
  const h = document.createElement('h3');
  h.className = 'status-card-title';
  h.textContent = title;
  const list = document.createElement('dl');
  list.className = 'status-list';
  for (const r of rows) list.append(row(...r));
  box.append(h, list);
  return box;
}

function note(text) {
  const p = document.createElement('p');
  p.className = 'status-note';
  p.textContent = text;
  return p;
}

function render(st) {
  const nodes = [
    card('服务', [
      ['版本', st.version || '未知'],
      ['运行时长', fmtUptime(st.uptime_s)],
      ['当前地址', location.origin],
    ]),
    card('内容', [
      ['全部', `${st.items.total} 条`],
      ['未取走', `${st.items.todo} 条`],
      ['已取走', `${st.items.taken} 条`],
    ]),
    card('附件', [
      ['数量', `${st.files.count} 个`],
      ['占用空间', fmtBytes(st.files.bytes)],
    ]),
  ];

  if (st.admin) {
    const disk = diskValue(st.admin.disk);
    nodes.push(
      card('存储与运行', [
        ['用户', `${st.admin.users} 个`],
        ['磁盘剩余', disk.value, { error: disk.error }],
        ['监听地址', st.admin.listen || '未知', { mono: true }],
        ['存储目录', st.admin.items_dir || '未知', { mono: true }],
        ['mDNS', mdnsText(st.admin.mdns)],
      ]),
    );
  } else {
    nodes.push(note('磁盘、用户与 mDNS 状态仅管理员可见。'));
  }

  $('status-cards').replaceChildren(...nodes);
}

function stamp() {
  const d = new Date();
  const p = (x) => String(x).padStart(2, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

export async function refreshStatus() {
  if (inFlight) return;
  inFlight = true;
  const err = $('status-error');
  try {
    const st = await api.stats();
    render(st);
    err.hidden = true;
    $('status-hint').textContent = `更新于 ${stamp()} · 每 5 秒自动刷新`;
  } catch (e) {
    // 保留上一次的数据，只提示这次刷新失败
    err.textContent = e.message || String(e);
    err.hidden = false;
  } finally {
    inFlight = false;
  }
}

function startTimer() {
  stopTimer();
  timer = setInterval(() => {
    if (!document.hidden) refreshStatus();
  }, REFRESH_MS);
}

function stopTimer() {
  if (timer) {
    clearInterval(timer);
    timer = 0;
  }
}

// refreshConnect 渲染「手机扫码连接」卡片：换地址就换二维码。
async function refreshConnect() {
  const card = $('connect-card');
  if (!card) return;
  try {
    const info = await api.connect();
    const urls = (info && info.urls) || [];
    if (!urls.length) {
      card.hidden = true;
      return;
    }
    const pick = fillURLOptions($('connect-url'), urls, info.preferred);
    renderQR($('connect-qr'), pick, pick);
    $('connect-name').textContent = info.name || '';
    card.hidden = false;
  } catch {
    // 连接信息拿不到时静默隐藏，不影响其它状态卡片
    card.hidden = true;
  }
}

// openConnectPanel 打开「手机扫码连接」对话框（侧栏入口，不必先进状态页）。
// 数据与状态页那张卡片同源：GET /api/connect → 地址下拉 + 二维码 SVG。
export async function openConnectPanel() {
  const wrap = document.createElement('div');
  wrap.className = 'connect-panel';

  const qrBox = document.createElement('div');
  qrBox.className = 'connect-qr';

  const select = document.createElement('select');
  select.setAttribute('aria-label', '连接地址');

  const field = document.createElement('label');
  field.className = 'field';
  const fieldLabel = document.createElement('span');
  fieldLabel.className = 'field-label';
  fieldLabel.textContent = '连接地址';
  field.append(fieldLabel, select);

  const hint = document.createElement('p');
  hint.className = 'hint';
  hint.textContent = '手机相机扫码即可打开；自签证书首次还需要在手机上选择「继续访问」。';

  const side = document.createElement('div');
  side.className = 'connect-side';
  side.append(field, hint);

  const body = document.createElement('div');
  body.className = 'connect-body';
  body.append(qrBox, side);
  wrap.append(body);

  openPanel({ title: '手机扫码连接', content: wrap });

  select.addEventListener('change', () => renderQR(qrBox, select.value, select.value));

  try {
    const info = await api.connect();
    const pick = fillURLOptions(select, (info && info.urls) || [], info && info.preferred);
    if (!pick) {
      qrBox.remove();
      field.hidden = true;
      hint.textContent = '没有可用的连接地址（检查 listen 配置；只监听 127.0.0.1 时手机连不上）。';
      return;
    }
    renderQR(qrBox, pick, pick);
  } catch (err) {
    // 拿不到连接信息时给出可读原因，不要让对话框空白
    qrBox.remove();
    field.hidden = true;
    hint.textContent = (err && err.message) || '连接信息获取失败';
  }
}

// openStatusPage 进入状态页：立刻拉一次，然后开始轮询。
export function openStatusPage() {
  refreshStatus();
  refreshConnect();
  startTimer();
}

export function closeStatusPage() {
  stopTimer();
}

export function initStatusPage() {
  $('btn-status-refresh').addEventListener('click', () => {
    refreshStatus();
    refreshConnect();
  });
  $('connect-url')?.addEventListener('change', (ev) => {
    renderQR($('connect-qr'), ev.target.value, ev.target.value);
  });
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden && !$('status-view').hidden) refreshStatus();
  });
}
