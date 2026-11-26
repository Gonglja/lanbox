// discovery.js：服务器簿、局域网发现（mDNS）、扫码添加与已连接时的服务器面板。
//
// 浏览器拿不到原始组播，所以「发现」有两种来源：
//   1. 本地探测：请系统解析器解析 .local 名称（macOS/iOS/Android/Linux 原生支持 mDNS），
//      再打公开的 /api/healthz 确认是 lanbox；
//   2. 已连上一台时问它：GET /api/discover 由服务端做真正的 mDNS 浏览（同 lanbox-cli discover）。
// 扫码用 BarcodeDetector（Android Chrome 等支持）；不支持时退回粘贴地址。
import { api } from '../web/api.js';
import { apiBase, setToken } from '../web/env.js';
import { confirmDialog, openPanel } from '../web/ui.js';
import { fillURLOptions, renderQR } from '../web/connect.js';
import {
  TOKEN_KEY,
  applyConnection,
  hostOf,
  normalizeServer,
  setAuthStatus,
  storage,
  syncLoginHost,
} from './state.js';

const $ = (id) => document.getElementById(id);

const SERVERS_KEY = 'lanbox.client.servers';
const MAX_SERVERS = 12;

// ---------- 服务器簿 ----------

function loadServerBook() {
  try {
    const raw = JSON.parse(localStorage.getItem(SERVERS_KEY) || '[]');
    if (!Array.isArray(raw)) return [];
    return raw.filter((x) => x && typeof x.url === 'string' && /^https?:\/\//.test(x.url)).slice(0, MAX_SERVERS);
  } catch {
    return [];
  }
}

function writeServerBook(book) {
  try {
    localStorage.setItem(SERVERS_KEY, JSON.stringify(book.slice(0, MAX_SERVERS)));
  } catch {
    /* 存不下就只在本次生效 */
  }
}

// saveServerEntry 记住一台服务器（按 URL 去重，最近使用的排最前）。
export function saveServerEntry(entry) {
  if (!entry || !entry.url) return;
  const book = loadServerBook().filter((x) => x.url !== entry.url);
  book.unshift({
    url: entry.url,
    name: entry.name || '',
    version: entry.version || '',
    at: Date.now(),
  });
  writeServerBook(book);
  renderServerBook();
}

function removeServerEntry(url) {
  writeServerBook(loadServerBook().filter((x) => x.url !== url));
  renderServerBook();
}

function renderServerBook() {
  const list = $('server-list');
  if (!list) return;
  const book = loadServerBook();
  list.hidden = book.length === 0;
  list.replaceChildren(
    ...book.map((item) => {
      const li = document.createElement('li');
      li.className = 'server-item';

      const pick = document.createElement('button');
      pick.type = 'button';
      pick.className = 'server-pick';
      pick.title = `使用 ${item.url}`;
      const name = document.createElement('span');
      name.className = 'server-name';
      name.textContent = item.name || hostOf(item.url);
      const url = document.createElement('span');
      url.className = 'server-url';
      url.textContent = item.url;
      pick.append(name, url);
      pick.addEventListener('click', () => {
        chooseServer(item.url);
        $('login-password')?.focus();
      });

      const rm = document.createElement('button');
      rm.type = 'button';
      rm.className = 'chip-x';
      rm.title = '从列表移除';
      rm.setAttribute('aria-label', `移除 ${item.url}`);
      rm.textContent = '×';
      rm.addEventListener('click', () => removeServerEntry(item.url));

      li.append(pick, rm);
      return li;
    }),
  );
}

// chooseServer 把地址填进连接页并同步注入点（顺带清掉旧令牌）。
export function chooseServer(url) {
  const input = $('server-input');
  if (input) input.value = url;
  applyConnection(url, '');
  syncLoginHost();
  setAuthStatus('');
}

// noteCurrentServer 登录成功后把当前服务器记进簿子（名字/版本从 /api/connect 取）。
// 同时记住服务端给出的 mDNS 名：下次「发现局域网」就能按名字探测到它（即使端口不是 443）。
export function noteCurrentServer(origin) {
  api
    .connect()
    .then((info) => {
      const entry = { name: info.name, version: info.version };
      saveServerEntry({ ...entry, url: origin });
      for (const u of info.urls || []) {
        if (u.kind === 'mdns' && u.url !== origin) saveServerEntry({ ...entry, url: u.url });
      }
    })
    .catch(() => saveServerEntry({ url: origin }));
}

// ---------- 探测 ----------

// probeServer 用公开的 /api/healthz 确认某个地址上跑的是 lanbox，并取回名字/版本。
export async function probeServer(origin) {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 3000);
  try {
    const resp = await fetch(origin + '/api/healthz', { cache: 'no-store', signal: ctrl.signal });
    if (!resp.ok) return null;
    const data = await resp.json();
    if (!data || data.ok !== true) return null;
    return { url: origin, name: data.name || '', version: data.version || '' };
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

// lanCandidates 生成探测候选：服务端 mDNS 的默认主机名 + 服务器簿里出现过的 .local 名，
// 每个名字探它自己的端口（簿子里记过的）以及 443 / 8443 两个默认端口。
// 改过 mdns.hostname 或用了非默认端口时，靠簿子里的记录仍能命中。
function lanCandidates() {
  const targets = new Map(); // host -> Set<port>
  const add = (host, port) => {
    const h = String(host || '').toLowerCase();
    if (!h) return;
    if (!targets.has(h)) targets.set(h, new Set());
    targets.get(h).add(String(port || '443'));
  };
  add('lanbox.local', '443');
  add('lanbox.local', '8443');
  for (const item of loadServerBook()) {
    try {
      const u = new URL(item.url);
      if (u.hostname.endsWith('.local')) {
        add(u.hostname, u.port || '443');
        add(u.hostname, '443');
        add(u.hostname, '8443');
      }
    } catch {
      /* 忽略坏数据 */
    }
  }
  const out = [];
  for (const [host, ports] of targets) {
    for (const port of ports) {
      out.push(port === '443' ? `https://${host}` : `https://${host}:${port}`);
    }
  }
  return out;
}

async function discoverLAN() {
  const btn = $('btn-scan-lan');
  const candidates = lanCandidates();
  if (btn) btn.disabled = true;
  setAuthStatus(`正在通过 mDNS 探测 ${candidates.length} 个地址…`);
  try {
    const hits = (await Promise.all(candidates.map(probeServer))).filter(Boolean);
    if (!hits.length) {
      setAuthStatus(
        '没找到 lanbox.local。改过 mDNS 主机名的话请手动填地址；也可以先连上一台，再用「发现其他实例」。',
        'warn',
      );
      return;
    }
    for (const hit of hits) saveServerEntry(hit);
    chooseServer(hits[0].url);
    setAuthStatus(`发现 ${hits.length} 个实例，已填入地址`);
    $('login-password')?.focus();
  } finally {
    if (btn) btn.disabled = false;
  }
}

// ---------- 扫码 ----------

// extractServerURL 从二维码内容里取出服务器地址（兼容纯 URL 与带说明文字的文本）。
function extractServerURL(text) {
  const raw = String(text || '').trim();
  if (!raw) return '';
  const m = raw.match(/https?:\/\/[^\s"'<>]+/i);
  if (m) return m[0];
  if (/^[a-z0-9.-]+(\.[a-z0-9-]+)+(:\d+)?$/i.test(raw) || /^\d+\.\d+\.\d+\.\d+(:\d+)?$/.test(raw)) {
    return raw;
  }
  return '';
}

// handleScanned 校验并登记扫到的地址。
async function handleScanned(text) {
  const url = extractServerURL(text);
  if (!url) {
    setAuthStatus('这段内容里没有可识别的服务器地址', 'warn');
    return '';
  }
  const res = normalizeServer(url);
  if (res.error) {
    setAuthStatus(res.error, 'warn');
    return '';
  }
  const hit = await probeServer(res.origin);
  saveServerEntry(hit || { url: res.origin });
  chooseServer(res.origin);
  setAuthStatus(hit ? `已添加 ${hit.name || hostOf(res.origin)}` : '已添加地址（暂时没连上，可稍后重试）');
  return res.origin;
}

// openScanPanel 打开扫码面板：浏览器支持 BarcodeDetector 就用摄像头，否则粘贴地址。
export async function openScanPanel() {
  const canScan = 'BarcodeDetector' in window && !!navigator.mediaDevices?.getUserMedia;
  const wrap = document.createElement('div');
  wrap.className = 'scan-wrap';

  const video = document.createElement('video');
  video.className = 'scan-video';
  video.playsInline = true;
  video.muted = true;
  video.hidden = !canScan;

  const tip = document.createElement('p');
  tip.className = 'hint';
  tip.textContent = canScan
    ? '把服务器页面上「手机扫码连接」的二维码放进取景框'
    : '当前浏览器不支持扫码（Android Chrome 支持），可以粘贴二维码内容或手动填地址';

  const pasteLabel = document.createElement('label');
  pasteLabel.className = 'field';
  const pasteText = document.createElement('span');
  pasteText.className = 'field-label';
  pasteText.textContent = '服务器地址';
  const paste = document.createElement('input');
  paste.type = 'url';
  paste.placeholder = 'https://192.168.1.9';
  paste.autocomplete = 'off';
  paste.spellcheck = false;
  pasteLabel.append(pasteText, paste);

  const actions = document.createElement('div');
  actions.className = 'dialog-actions';
  const submit = document.createElement('button');
  submit.type = 'button';
  submit.className = 'btn btn-primary';
  submit.textContent = '使用这个地址';
  actions.append(submit);

  wrap.append(video, tip, pasteLabel, actions);

  let stopCamera = () => {};
  const panel = openPanel({ title: '扫码添加服务器', content: wrap, onClose: () => stopCamera() });

  const accept = async () => {
    const value = paste.value.trim();
    if (!value) return;
    const origin = await handleScanned(value);
    if (origin) panel.close();
  };
  submit.addEventListener('click', accept);
  paste.addEventListener('keydown', (ev) => {
    if (ev.key === 'Enter') {
      ev.preventDefault();
      accept();
    }
  });

  if (!canScan) {
    paste.focus();
    return;
  }

  let stopped = false;
  let stream = null;
  stopCamera = () => {
    stopped = true;
    if (stream) for (const track of stream.getTracks()) track.stop();
  };
  try {
    stream = await navigator.mediaDevices.getUserMedia({
      video: { facingMode: { ideal: 'environment' } },
      audio: false,
    });
  } catch (err) {
    tip.textContent = `打不开摄像头（${err.name || '错误'}），可以粘贴地址`;
    video.hidden = true;
    paste.focus();
    return;
  }
  if (stopped) {
    // 摄像头到手之前面板已被关掉
    for (const track of stream.getTracks()) track.stop();
    return;
  }
  video.srcObject = stream;
  await video.play().catch(() => {});

  const detector = new BarcodeDetector({ formats: ['qr_code'] });
  const tick = async () => {
    if (stopped) return;
    try {
      const codes = await detector.detect(video);
      if (codes && codes.length) {
        stopCamera();
        const origin = await handleScanned(codes[0].rawValue);
        if (origin) panel.close();
        return;
      }
    } catch {
      /* 单帧识别失败继续下一帧 */
    }
    setTimeout(tick, 300);
  };
  setTimeout(tick, 400);
}

// ---------- 已连接时的服务器面板 ----------

function hintNode(text) {
  const p = document.createElement('p');
  p.className = 'hint';
  p.textContent = text;
  return p;
}

// disconnectAndReload 清掉本机会话并回到连接页（地址保留，方便改）。
export async function disconnectAndReload() {
  const ok = await confirmDialog({
    title: '断开并切换服务器？',
    message: '只清除本机登录状态，服务器上的内容不受影响。',
    confirmText: '断开',
    danger: true,
  });
  if (!ok) return;
  try {
    await api.logout();
  } catch {
    /* 令牌可能已失效 */
  }
  setToken('');
  storage.set(TOKEN_KEY, '');
  location.reload();
}

// openServerPanel 显示：当前地址的二维码（给另一台设备扫）、地址列表、发现其他实例、断开。
export async function openServerPanel() {
  const wrap = document.createElement('div');
  wrap.className = 'server-panel';

  const current = document.createElement('p');
  current.className = 'dialog-text';
  current.textContent = `当前：${apiBase()}`;

  const qrBox = document.createElement('div');
  qrBox.className = 'connect-qr';

  const select = document.createElement('select');
  select.setAttribute('aria-label', '连接地址');

  const list = document.createElement('ul');
  list.className = 'server-list';

  const actions = document.createElement('div');
  actions.className = 'dialog-actions';
  const findBtn = document.createElement('button');
  findBtn.type = 'button';
  findBtn.className = 'btn';
  findBtn.textContent = '发现其他实例';
  const cutBtn = document.createElement('button');
  cutBtn.type = 'button';
  cutBtn.className = 'btn btn-danger';
  cutBtn.textContent = '断开并切换';
  actions.append(findBtn, cutBtn);

  wrap.append(current, qrBox, select, list, actions);
  const panel = openPanel({ title: '服务器与扫码连接', content: wrap });

  select.addEventListener('change', () => renderQR(qrBox, select.value, select.value));

  try {
    const info = await api.connect();
    const pick = fillURLOptions(select, info.urls || [], info.preferred);
    renderQR(qrBox, pick, pick);
  } catch {
    renderQR(qrBox, apiBase(), apiBase());
  }

  findBtn.addEventListener('click', async () => {
    findBtn.disabled = true;
    list.replaceChildren(hintNode('正在浏览局域网（约 1–2 秒）…'));
    try {
      const res = await api.discover();
      const servers = (res && res.servers) || [];
      if (!servers.length) {
        list.replaceChildren(hintNode('没有发现其他实例（对方可能关掉了 mDNS，或不在同一网段）'));
        return;
      }
      list.replaceChildren(
        ...servers.map((srv) => {
          const li = document.createElement('li');
          li.className = 'server-item';
          const btn = document.createElement('button');
          btn.type = 'button';
          btn.className = 'server-pick';
          const target = (srv.urls && srv.urls[0]) || '';
          btn.disabled = !target;
          const name = document.createElement('span');
          name.className = 'server-name';
          name.textContent = srv.name + (srv.current ? '（本机）' : '');
          const url = document.createElement('span');
          url.className = 'server-url';
          url.textContent = target || '没有可用地址';
          btn.append(name, url);
          btn.addEventListener('click', async () => {
            saveServerEntry({ url: target, name: srv.name });
            chooseServer(target);
            panel.close();
            await disconnectAndReload();
          });
          li.append(btn);
          return li;
        }),
      );
    } catch (err) {
      list.replaceChildren(hintNode(err.message || '发现失败'));
    } finally {
      findBtn.disabled = false;
    }
  });

  cutBtn.addEventListener('click', async () => {
    panel.close();
    await disconnectAndReload();
  });
}

// ---------- 初始化 ----------

// initDiscovery 绑定连接页上的「扫码添加 / 发现局域网」与服务器簿。
export function initDiscovery() {
  renderServerBook();
  $('btn-scan-qr')?.addEventListener('click', () => openScanPanel());
  $('btn-scan-lan')?.addEventListener('click', () => discoverLAN());
}
