'use strict';

// lanbox-agent 前端：只依赖 Wails 注入的 window.go / window.runtime，
// 不引入任何构建工具或框架。

const $ = (id) => document.getElementById(id);
const api = () => (window.go && window.go.main && window.go.main.App) || null;

let state = null;
let settings = null;
let records = [];
let found = [];
let picked = [];
let probedFP = '';

// ---------- 基础工具 ----------

async function call(name, ...args) {
  const a = api();
  if (!a || typeof a[name] !== 'function') {
    throw new Error('客户端接口未就绪');
  }
  return a[name](...args);
}

function errText(e) {
  if (e == null) return '未知错误';
  if (typeof e === 'string') return e;
  if (e.message) return e.message;
  return String(e);
}

let toastTimer = 0;
function toast(msg, ms = 2800) {
  const el = $('toast');
  el.textContent = msg;
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, ms);
}

function esc(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// ---------- 确认框 ----------

// 不用 window.confirm：WebView 的原生弹框不受窗口尺寸约束，比 456 宽的窗口还大。
// 这里在页面内画一个小卡片（与服务端 Web UI 的对话框同款）。
let activeDialog = null;

function closeDialog(result) {
  if (!activeDialog) return;
  const { root, resolve } = activeDialog;
  activeDialog = null;
  root.hidden = true;
  root.replaceChildren();
  root.onclick = null;
  document.removeEventListener('keydown', onDialogKey, true);
  resolve(result);
}

function onDialogKey(ev) {
  if (!activeDialog) return;
  if (ev.key === 'Escape') {
    ev.preventDefault();
    ev.stopPropagation();
    closeDialog(false);
  }
}

// confirmDialog 返回 Promise<boolean>；危险操作的确认按钮用红色。
function confirmDialog({ title, message, confirmText = '确定', cancelText = '取消', danger = false }) {
  const root = $('dialog-root');
  if (!root) return Promise.resolve(false);
  if (activeDialog) closeDialog(false);

  const dialog = document.createElement('div');
  dialog.className = 'dialog';
  dialog.setAttribute('role', 'dialog');
  dialog.setAttribute('aria-modal', 'true');
  dialog.setAttribute('aria-label', title || '');

  const h = document.createElement('h2');
  h.className = 'dialog-title';
  h.textContent = title || '';
  dialog.append(h);

  if (message) {
    const p = document.createElement('p');
    p.className = 'dialog-text';
    p.textContent = message;
    dialog.append(p);
  }

  const actions = document.createElement('div');
  actions.className = 'dialog-actions';

  const cancel = document.createElement('button');
  cancel.type = 'button';
  cancel.className = 'btn btn-sm';
  cancel.textContent = cancelText;
  cancel.addEventListener('click', () => closeDialog(false));

  const ok = document.createElement('button');
  ok.type = 'button';
  ok.className = 'btn btn-sm ' + (danger ? 'btn-danger' : 'btn-primary');
  ok.textContent = confirmText;
  ok.addEventListener('click', () => closeDialog(true));

  actions.append(cancel, ok);
  dialog.append(actions);

  root.replaceChildren(dialog);
  root.hidden = false;
  root.onclick = (ev) => { if (ev.target === root) closeDialog(false); };
  document.addEventListener('keydown', onDialogKey, true);
  // 默认聚焦在取消上：回车不会误触危险操作
  requestAnimationFrame(() => cancel.focus());

  return new Promise((resolve) => { activeDialog = { root, resolve }; });
}

function fmtSize(n) {
  if (!n) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let v = n, i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return (i === 0 ? v : v.toFixed(1)) + ' ' + units[i];
}

function fmtTime(iso) {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '';
  const pad = (n) => String(n).padStart(2, '0');
  const today = new Date();
  const sameDay = d.toDateString() === today.toDateString();
  const hm = pad(d.getHours()) + ':' + pad(d.getMinutes());
  return sameDay ? hm : (d.getMonth() + 1) + '/' + d.getDate() + ' ' + hm;
}

// ---------- 渲染 ----------

const KIND_LABEL = { text: '文本', files: '文件', image: '图片' };
const STATE_LABEL = {
  queued: '排队中', uploading: '上传中', ok: '已上传',
  failed: '失败', canceled: '已取消', withdrawn: '已撤回',
};

// ---------- 断线重连 ----------
// 后端每秒会用 state 事件推一次状态，next_retry_at 是 epoch 毫秒；
// 倒计时在本地每秒重算一次，不靠后端推秒。

function retryLeft() {
  const at = Number((state && state.next_retry_at) || 0);
  if (!at) return 0;
  return Math.max(0, Math.round((at - Date.now()) / 1000));
}

function paintReconnectText() {
  const text = $('reconnect-text');
  if (!text || !state) return;
  if (state.reconnect_stopped) {
    text.textContent = '未连接到服务器：' + (state.reconnect_reason || '已停止自动重连') + '。';
    return;
  }
  const left = retryLeft();
  const nth = (state.reconnect_attempts || 0) + 1;
  text.textContent = left > 0
    ? '未连接到服务器，正在自动重连（下次 ' + left + 's 后，第 ' + nth + ' 次尝试）'
    : '未连接到服务器，正在自动重连…';
}

function renderReconnect() {
  const banner = $('reconnect-banner');
  if (!banner) return;
  const down = Boolean(state && (state.reconnecting || state.reconnect_stopped));
  banner.hidden = !down;
  if (!down) return;
  banner.classList.toggle('is-stopped', Boolean(state.reconnect_stopped));
  $('reconnect-login-btn').hidden = !state.reconnect_stopped;
  paintReconnectText();
}

function renderState() {
  if (!state) return;
  const dot = $('status-dot');
  let cls = 'ok';
  if (state.reconnect_stopped) cls = 'err';
  else if (state.reconnecting) cls = 'warn';
  else if (!state.logged_in) cls = 'err';
  else if (state.paused || !state.enabled) cls = 'warn';
  dot.className = 'dot ' + cls;

  const ver = String(state.version || '');
  $('version-label').textContent = (ver && ver !== 'dev') ? 'v' + ver.replace(/^v/, '') : '';
  const server = state.server || '未登录';
  $('server-label').textContent = server + (state.reconnecting ? ' · 重连中' : '');
  $('user-label').textContent = state.username ? '· ' + state.username : '';
  $('pause-btn').textContent = state.paused ? '恢复' : '暂停';
  $('logout-btn').title = state.username ? '退出登录（' + state.username + '）' : '退出登录';
  $('queue-label').textContent = state.queue > 0
    ? (state.active > 0 ? '上传中 ' + state.active + ' 个，排队 ' + (state.queue - state.active) + ' 个' : '排队 ' + state.queue + ' 个')
    : '';

  const hint = state.reconnect_stopped ? (state.reconnect_reason || state.last_err)
    : (state.reconnecting ? (state.last_err || '正在自动重连') : (state.last_err || state.last_skip)) || '';
  $('server-label').title = hint;
  renderReconnect();
}

function renderRecords() {
  const list = $('records');
  $('records-empty').hidden = records.length > 0;
  list.innerHTML = records.map((r, i) => {
    const stCls = r.state === 'ok' ? 'pill pill-ok'
      : (r.state === 'failed' ? 'pill pill-danger'
        : ((r.state === 'uploading' || r.state === 'queued') ? 'pill pill-warn' : 'pill'));
    const actions = [];
    if (r.state === 'failed') actions.push(`<button class="link-btn" data-act="retry" data-id="${esc(r.id)}">重试</button>`);
    if (r.state === 'uploading' || r.state === 'queued') actions.push(`<button class="link-btn" data-act="cancel" data-id="${esc(r.id)}">取消</button>`);
    if (r.state === 'ok') actions.push(`<button class="link-btn" data-act="withdraw" data-id="${esc(r.id)}">撤回</button>`);
    const progress = r.state === 'uploading'
      ? `<div class="progress"><i style="width:${Math.max(2, Math.min(100, r.progress || 0))}%"></i></div>` : '';
    const meta = [
      r.source === 'clipboard' ? '剪贴板' : '手动',
      fmtSize(r.size),
      r.file_n > 1 ? r.file_n + ' 个文件' : '',
      r.skipped ? '跳过 ' + r.skipped + ' 个文件' : '',
      r.state === 'uploading' ? (r.progress || 0) + '%' : '',
    ].filter(Boolean).join(' · ');
    return `
      <li class="record" data-i="${i}">
        <div class="head">
          <span class="pill">${esc(KIND_LABEL[r.kind] || r.kind)}</span>
          <span class="time">${esc(fmtTime(r.at))}</span>
          <span class="${stCls}">${esc(STATE_LABEL[r.state] || r.state)}</span>
          <div class="actions">${actions.join('')}</div>
        </div>
        <div class="preview"></div>
        ${progress}
        <div class="foot">${esc(meta)}</div>
        ${r.error ? `<div class="err-msg">${esc(r.error)}</div>` : ''}
      </li>`;
  }).join('');
  renderRecordTexts();
}

// ---------- 记录正文：Markdown ----------
// 正文用与服务端 Web UI 同一份渲染器（markdown.js）画成 DOM，不走 innerHTML。
// 进度刷新会高频重建整个列表，所以渲染结果按「id + 正文」缓存，命中时只 cloneNode。
const mdCache = new Map();
const MD_LIMIT = 8 * 1024;

function mdNode(key, text) {
  const hit = mdCache.get(key);
  if (hit) return hit;
  const box = document.createElement('div');
  box.className = 'md';
  let src = text;
  let clipped = false;
  if (src.length > MD_LIMIT) {
    // 在限制附近找一个换行切开，避免把 Markdown 语法从中间截断得太难看
    const cut = src.lastIndexOf('\n', MD_LIMIT);
    src = src.slice(0, cut > MD_LIMIT / 2 ? cut : MD_LIMIT);
    clipped = true;
  }
  if (window.renderMarkdown) window.renderMarkdown(box, src);
  else box.textContent = src;
  if (clipped) {
    const hint = document.createElement('p');
    hint.className = 'hint';
    hint.textContent = '…（内容过长，预览已截断）';
    box.append(hint);
  }
  mdCache.set(key, box);
  return box;
}

function renderRecordTexts() {
  const list = $('records');
  const used = new Set();
  for (const li of list.children) {
    const r = records[Number(li.getAttribute('data-i'))];
    const host = li.querySelector('.preview');
    if (!r || !host) continue;
    const text = String(r.text || r.preview || '').trim();
    if (!text) {
      host.textContent = '（无文本）';
      continue;
    }
    const key = r.id + '\u0000' + text;
    used.add(key);
    host.replaceChildren(mdNode(key, text).cloneNode(true));
  }
  for (const key of mdCache.keys()) {
    if (!used.has(key)) mdCache.delete(key);
  }
}

function renderFound() {
  const ul = $('found-list');
  if (!found.length) {
    ul.innerHTML = '';
    return;
  }
  ul.innerHTML = found.map((f, i) => `
    <li data-i="${i}">
      <span class="name">${esc(f.instance)}</span>
      <span class="url">${esc(f.url || f.host || '')}</span>
      <span class="fp">${esc((f.fp || '').slice(0, 16))}</span>
    </li>`).join('');
  ul.querySelectorAll('li').forEach((li) => {
    li.addEventListener('click', () => {
      const f = found[Number(li.dataset.i)];
      if (!f) return;
      $('url').value = f.url || '';
      probedFP = f.fp || '';
      $('trust').checked = !!probedFP;
      $('fp-label').textContent = probedFP || '未获取';
      $('username').focus();
    });
  });
}

function renderPicked() {
  const el = $('picked-label');
  if (!picked.length) {
    el.textContent = '';
    return;
  }
  el.textContent = picked.length === 1
    ? picked[0].split(/[\\/]/).pop()
    : picked.length + ' 个路径';
  el.title = picked.join('\n');
}

function showLogin() {
  $('login-view').hidden = false;
  $('main-view').hidden = true;

  // 重置登录卡片：不把上一位用户的密码或错误留在屏幕上
  const pw = $('password');
  pw.value = '';
  pw.type = 'password';
  const toggle = $('toggle-password');
  toggle.setAttribute('aria-pressed', 'false');
  toggle.setAttribute('aria-label', '显示密码');
  toggle.title = '显示密码';
  $('login-caps').hidden = true;
  clearLoginErr();
  // 地址、用户名都齐了就直接光标进密码框
  const target = !$('url').value.trim() ? $('url')
    : (! $('username').value.trim() ? $('username') : pw);
  target.focus();
}

function clearLoginErr() {
  $('login-err').textContent = '';
  $('username').removeAttribute('aria-invalid');
  $('password').removeAttribute('aria-invalid');
}

function showLoginErr(message, focusEl) {
  const el = $('login-err');
  el.textContent = message || '未知错误';
  $('login-err').scrollIntoView({ block: 'nearest' });
  const card = document.querySelector('#login-view .login-card');
  if (card) {
    card.classList.remove('is-shake');
    void card.offsetWidth; // 强制回流，让抖动可以重复播放
    card.classList.add('is-shake');
  }
  if (focusEl) {
    focusEl.focus();
    if (focusEl.select) focusEl.select();
  }
}

function showMain() {
  $('login-view').hidden = true;
  $('main-view').hidden = false;
}

// ---------- 设置 ----------

function fillSettings(s) {
  settings = s;
  const c = s.clipboard || {};
  const a = s.app || {};
  $('set-enabled').checked = !!c.enabled;
  $('set-text').checked = !!c.text;
  $('set-files').checked = !!c.files;
  $('set-images').checked = !!c.images;
  $('set-dedupe').value = c.dedupe_seconds ?? 30;
  $('set-max-text').value = Math.round((c.max_text_bytes || 1048576) / 1024);
  $('set-max-image').value = c.max_image_mb ?? 5;
  $('set-max-total').value = c.max_total_mb ?? 2048;
  $('set-max-files').value = c.max_files ?? 500;
  $('set-history').value = a.history_size ?? 50;
  $('set-prefix').value = c.text_prefix || '';
  $('set-autostart').checked = !!a.autostart;
  $('set-close-tray').checked = a.close_to_tray !== false;
  $('set-remember').checked = a.remember_password !== false;
}

function collectSettings() {
  const s = JSON.parse(JSON.stringify(settings || {}));
  s.clipboard = s.clipboard || {};
  s.app = s.app || {};
  const c = s.clipboard, a = s.app;
  c.enabled = $('set-enabled').checked;
  c.text = $('set-text').checked;
  c.files = $('set-files').checked;
  c.images = $('set-images').checked;
  c.dedupe_seconds = intOr($('set-dedupe').value, 30);
  c.max_text_bytes = Math.max(1024, intOr($('set-max-text').value, 1024) * 1024);
  c.max_image_mb = intOr($('set-max-image').value, 5);
  c.max_total_mb = intOr($('set-max-total').value, 2048);
  c.max_files = intOr($('set-max-files').value, 500);
  c.text_prefix = $('set-prefix').value;
  a.history_size = intOr($('set-history').value, 50);
  a.autostart = $('set-autostart').checked;
  a.close_to_tray = $('set-close-tray').checked;
  a.remember_password = $('set-remember').checked;
  return s;
}

function intOr(v, def) {
  const n = parseInt(v, 10);
  return Number.isFinite(n) && n >= 0 ? n : def;
}

// ---------- 动作 ----------

async function doPause() {
  try {
    state = await call('Pause', !state.paused);
    renderState();
  } catch (e) {
    toast('操作失败: ' + errText(e));
  }
}

async function doLogout() {
  const ok = await confirmDialog({
    title: '退出登录',
    message: '本地会话会被删除，剪贴板自动上传会暂停，需要重新登录。',
    confirmText: '退出登录',
    danger: true,
  });
  if (!ok) return;
  try {
    await call('Logout');
    state = await call('GetState');
    records = await call('GetHistory');
    renderState();
    renderRecords();
    showLogin();
  } catch (e) {
    toast('退出失败: ' + errText(e));
  }
}

async function doSend() {
  const text = $('msg').value;
  const paths = picked.slice();
  if (!text.trim() && paths.length === 0) {
    toast('请输入内容或选择文件');
    return;
  }
  $('send-btn').disabled = true;
  try {
    await call('SendManual', text, paths);
    $('msg').value = '';
    autoGrow();
    refreshPreview();
    picked = [];
    renderPicked();
  } catch (e) {
    toast('发送失败: ' + errText(e));
  } finally {
    $('send-btn').disabled = false;
  }
}

async function doPickFiles() {
  try {
    const paths = await call('PickFiles');
    if (paths && paths.length) {
      picked = paths;
      renderPicked();
    }
  } catch (e) {
    toast('选择文件失败: ' + errText(e));
  }
}

async function doPickFolder() {
  try {
    const p = await call('PickFolder');
    if (p) {
      picked = [p];
      renderPicked();
    }
  } catch (e) {
    toast('选择文件夹失败: ' + errText(e));
  }
}

async function doRecordAction(act, id) {
  try {
    if (act === 'retry') await call('Retry', id);
    if (act === 'cancel') await call('Cancel', id);
    if (act === 'withdraw') {
      const ok = await confirmDialog({
        title: '撤回这条记录？',
        message: '撤回会同时删除服务器上的这条内容。',
        confirmText: '撤回',
        danger: true,
      });
      if (!ok) return;
      await call('Withdraw', id);
      toast('已撤回');
    }
  } catch (e) {
    toast('操作失败: ' + errText(e));
  }
}

async function doDiscover() {
  const btn = $('discover-btn');
  btn.disabled = true;
  $('discover-status').textContent = '搜索中…';
  try {
    found = (await call('Discover')) || [];
    renderFound();
    $('discover-status').textContent = found.length ? ('找到 ' + found.length + ' 个') : '没有发现服务器（检查是否在同一网段）';
  } catch (e) {
    $('discover-status').textContent = '发现失败: ' + errText(e);
  } finally {
    btn.disabled = false;
  }
}

async function doProbe() {
  const url = $('url').value.trim();
  if (!url) { toast('请先填写服务器地址'); return; }
  $('probe-btn').disabled = true;
  $('fp-label').textContent = '获取中…';
  try {
    probedFP = await call('ProbeCert', url);
    $('fp-label').textContent = probedFP;
    $('trust').checked = true;
  } catch (e) {
    probedFP = '';
    $('fp-label').textContent = '获取失败';
    toast('获取指纹失败: ' + errText(e));
  } finally {
    $('probe-btn').disabled = false;
  }
}

async function doLogin() {
  const btn = $('login-btn');
  if (btn.disabled) return; // 连点 / 回车重入时忽略
  const url = $('url').value.trim();
  const username = $('username').value.trim();
  const password = $('password').value;
  const pin = $('trust').checked ? probedFP : '';
  clearLoginErr();
  if (!url) { showLoginErr('请填写服务器地址', $('url')); return; }
  if (!username) { showLoginErr('请填写用户名', $('username')); return; }

  btn.disabled = true;
  btn.classList.add('is-busy');
  btn.setAttribute('aria-busy', 'true');
  btn.textContent = '正在登录…';
  try {
    state = await call('Login', {
      url, username, password,
      pin_fp: pin,
      remember: $('remember').checked,
    });
    records = (await call('GetHistory')) || [];
    settings = await call('GetSettings');
    fillSettings(settings);
    renderState();
    renderRecords();
    $('password').value = '';
    showMain();
  } catch (e) {
    $('username').setAttribute('aria-invalid', 'true');
    $('password').setAttribute('aria-invalid', 'true');
    showLoginErr(errText(e), $('password'));
  } finally {
    btn.disabled = false;
    btn.classList.remove('is-busy');
    btn.removeAttribute('aria-busy');
    btn.textContent = '登录';
  }
}

async function doSaveSettings() {
  const btn = $('save-settings-btn');
  btn.disabled = true;
  $('settings-status').textContent = '';
  try {
    settings = collectSettings();
    await call('SaveSettings', settings);
    state = await call('GetState');
    renderState();
    $('settings-status').textContent = '已保存';
    setTimeout(() => { $('settings-status').textContent = ''; }, 2500);
  } catch (e) {
    $('settings-status').textContent = '保存失败: ' + errText(e);
  } finally {
    btn.disabled = false;
  }
}


// ---------- 主题（与服务端 Web UI 完全一致：system/light/dark/paper） ----------

const THEME_KEY = 'lanbox.theme';
const THEME_VALID = ['system', 'light', 'dark', 'paper'];
const themeMedia = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;
let themePref = readThemePref();

function readThemePref() {
  try {
    const v = localStorage.getItem(THEME_KEY);
    if (THEME_VALID.includes(v)) return v;
  } catch (e) {
    /* 隐私模式忽略 */
  }
  return 'system';
}

function resolveTheme(pref) {
  if (pref === 'system') return themeMedia && themeMedia.matches ? 'dark' : 'light';
  return pref;
}

function applyTheme() {
  const theme = resolveTheme(themePref);
  const root = document.documentElement;
  root.dataset.theme = theme;
  root.dataset.themePref = themePref;
  root.style.colorScheme = theme === 'dark' ? 'dark' : 'light';
  document.querySelectorAll('#theme-menu [data-theme-value]').forEach((btn) => {
    btn.setAttribute('aria-checked', String(btn.dataset.themeValue === themePref));
  });
}

function setThemePref(v) {
  if (!THEME_VALID.includes(v)) return;
  themePref = v;
  try {
    localStorage.setItem(THEME_KEY, v);
  } catch (e) {
    /* 存不下就只在本次生效 */
  }
  applyTheme();
  closeThemeMenu();
}

function openThemeMenu() {
  const menu = $('theme-menu');
  const btn = $('theme-btn');
  menu.hidden = false;
  const r = btn.getBoundingClientRect();
  const w = menu.offsetWidth || 176;
  const left = Math.max(8, Math.min(window.innerWidth - w - 8, r.right - w));
  menu.style.left = left + 'px';
  menu.style.top = r.bottom + 6 + 'px';
  btn.setAttribute('aria-expanded', 'true');
  const active = menu.querySelector('[aria-checked="true"]') || menu.querySelector('button');
  if (active) active.focus();
}

function closeThemeMenu() {
  const menu = $('theme-menu');
  if (!menu || menu.hidden) return;
  menu.hidden = true;
  $('theme-btn').setAttribute('aria-expanded', 'false');
}

function toggleThemeMenu() {
  if ($('theme-menu').hidden) openThemeMenu();
  else closeThemeMenu();
}

function initTheme() {
  applyTheme();
  if (themeMedia) {
    const onChange = () => {
      if (themePref === 'system') applyTheme();
    };
    if (themeMedia.addEventListener) themeMedia.addEventListener('change', onChange);
    else if (themeMedia.addListener) themeMedia.addListener(onChange);
  }
}

// ---------- 事件绑定 ----------

function autoGrow() {
  const el = $('msg');
  el.style.height = 'auto';
  el.style.height = Math.min(320, Math.max(168, el.scrollHeight)) + 'px';
}

// ---------- 发布区 Markdown 预览 ----------
// 与服务端 Web UI 同一个交互：编辑区下方追加一块只读渲染结果，输入框保持可见。

let previewOpen = false;

function refreshPreview() {
  const box = $('composer-preview');
  if (!box) return;
  const text = $('msg').value;
  if (!previewOpen || !text.trim()) {
    box.hidden = true;
    box.replaceChildren();
    return;
  }
  if (window.renderMarkdown) window.renderMarkdown(box, text);
  else box.textContent = text;
  box.hidden = false;
}

function setPreviewOpen(next) {
  previewOpen = Boolean(next);
  const btn = $('btn-md-preview');
  btn.classList.toggle('is-active', previewOpen);
  btn.setAttribute('aria-pressed', String(previewOpen));
  refreshPreview();
}

function bind() {
  $('pause-btn').addEventListener('click', doPause);
  $('logout-btn').addEventListener('click', doLogout);
  $('send-btn').addEventListener('click', doSend);
  $('pick-files-btn').addEventListener('click', doPickFiles);
  $('pick-folder-btn').addEventListener('click', doPickFolder);
  $('btn-md-preview').addEventListener('click', () => setPreviewOpen(!previewOpen));
  $('reconnect-login-btn').addEventListener('click', showLogin);
  $('discover-btn').addEventListener('click', doDiscover);
  $('probe-btn').addEventListener('click', doProbe);
  $('login-btn').addEventListener('click', doLogin);

  // 桌面端习惯：任一字段回车即登录
  for (const id of ['url', 'username', 'password']) {
    $(id).addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter') {
        ev.preventDefault();
        doLogin();
      }
    });
    $(id).addEventListener('input', clearLoginErr);
  }

  $('toggle-password').addEventListener('click', () => {
    const input = $('password');
    const show = input.type === 'password';
    input.type = show ? 'text' : 'password';
    const btn = $('toggle-password');
    btn.setAttribute('aria-pressed', String(show));
    const label = show ? '隐藏密码' : '显示密码';
    btn.setAttribute('aria-label', label);
    btn.title = label;
    input.focus();
    const end = input.value.length;
    try {
      input.setSelectionRange(end, end);
    } catch (e) {
      /* 部分环境不支持 */
    }
  });

  // 大写锁定是很常见的登录失败原因，直接在密码框下提示
  const syncCaps = (ev) => {
    const on = typeof ev.getModifierState === 'function' && ev.getModifierState('CapsLock');
    $('login-caps').hidden = !on;
  };
  $('password').addEventListener('keydown', syncCaps);
  $('password').addEventListener('keyup', syncCaps);
  $('password').addEventListener('blur', () => { $('login-caps').hidden = true; });

  $('save-settings-btn').addEventListener('click', doSaveSettings);
  $('theme-btn').addEventListener('click', (ev) => {
    ev.stopPropagation();
    toggleThemeMenu();
  });
  $('theme-menu').addEventListener('click', (ev) => {
    const btn = ev.target.closest('button[data-theme-value]');
    if (btn) setThemePref(btn.dataset.themeValue);
  });
  document.addEventListener('click', (ev) => {
    const menu = $('theme-menu');
    if (menu.hidden) return;
    if (menu.contains(ev.target) || $('theme-btn').contains(ev.target)) return;
    closeThemeMenu();
  });
  document.addEventListener('keydown', (ev) => {
    if (ev.key === 'Escape') closeThemeMenu();
  });
  window.addEventListener('resize', closeThemeMenu);
  $('open-log-btn').addEventListener('click', async () => {
    try { await call('OpenLog'); } catch (e) { toast(errText(e)); }
  });
  $('open-data-btn').addEventListener('click', async () => {
    try { await call('OpenDataDir'); } catch (e) { toast(errText(e)); }
  });

  $('records').addEventListener('click', (ev) => {
    const btn = ev.target.closest('button[data-act]');
    if (!btn) return;
    doRecordAction(btn.dataset.act, btn.dataset.id);
  });

  $('msg').addEventListener('input', () => {
    autoGrow();
    refreshPreview();
  });
  $('msg').addEventListener('keydown', (ev) => {
    if (ev.key === 'Enter' && (ev.ctrlKey || ev.metaKey)) {
      ev.preventDefault();
      doSend();
    }
  });

  document.querySelectorAll('.tab').forEach((tab) => {
    tab.addEventListener('click', () => {
      document.querySelectorAll('.tab').forEach((t) => t.classList.toggle('active', t === tab));
      const recordsTab = tab.dataset.tab === 'records';
      $('pane-records').hidden = !recordsTab;
      $('pane-settings').hidden = recordsTab;
    });
  });

  // 拖拽视觉反馈（真正的落点由 Go 侧 OnFileDrop 处理）
  let dragDepth = 0;
  window.addEventListener('dragenter', (ev) => {
    ev.preventDefault();
    dragDepth++;
    $('drop-overlay').hidden = false;
  });
  window.addEventListener('dragover', (ev) => ev.preventDefault());
  window.addEventListener('dragleave', (ev) => {
    ev.preventDefault();
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) $('drop-overlay').hidden = true;
  });
  window.addEventListener('drop', (ev) => {
    ev.preventDefault();
    dragDepth = 0;
    $('drop-overlay').hidden = true;
  });
}

function subscribe() {
  if (!window.runtime || !window.runtime.EventsOn) return;
  window.runtime.EventsOn('state', (st) => {
    state = st;
    renderState();
  });
  window.runtime.EventsOn('records', (recs) => {
    records = recs || [];
    renderRecords();
  });
}

// ---------- 启动 ----------

async function boot() {
  if (window.fillIcons) window.fillIcons(); // data-icon → 真实 SVG（与 web 同一套图标）
  // 断线提示条上的倒计时：本地每秒重算，不依赖后端推秒
  setInterval(() => {
    if (state && (state.reconnecting || state.reconnect_stopped)) paintReconnectText();
  }, 1000);
  initTheme();
  bind();
  subscribe();
  try {
    state = await call('GetState');
    records = (await call('GetHistory')) || [];
    settings = await call('GetSettings');
    fillSettings(settings);
    renderState();
    renderRecords();
    autoGrow();
    if (state.logged_in) {
      showMain();
    } else {
      if (state.server) $('url').value = state.server;
      if (state.username) $('username').value = state.username;
      showLogin();
    }
    if (state.last_err) toast(state.last_err);
  } catch (e) {
    console.error(e);
    toast('初始化失败: ' + errText(e), 6000);
  }
}

document.addEventListener('DOMContentLoaded', boot);
