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

function renderState() {
  if (!state) return;
  const dot = $('status-dot');
  let cls = 'ok';
  if (!state.logged_in) cls = 'err';
  else if (state.paused || !state.enabled) cls = 'warn';
  dot.className = 'dot ' + cls;

  const ver = String(state.version || '');
  $('version-label').textContent = (ver && ver !== 'dev') ? 'v' + ver.replace(/^v/, '') : '';
  $('server-label').textContent = state.server || '未登录';
  $('user-label').textContent = state.username ? '· ' + state.username : '';
  $('pause-btn').textContent = state.paused ? '恢复' : '暂停';
  $('queue-label').textContent = state.queue > 0
    ? (state.active > 0 ? '上传中 ' + state.active + ' 个，排队 ' + (state.queue - state.active) + ' 个' : '排队 ' + state.queue + ' 个')
    : '';

  const hint = state.last_err || state.last_skip || '';
  $('server-label').title = hint;
}

function renderRecords() {
  const list = $('records');
  $('records-empty').hidden = records.length > 0;
  list.innerHTML = records.map((r) => {
    const stCls = r.state === 'ok' ? 'ok' : (r.state === 'failed' ? 'err' : (r.state === 'canceled' || r.state === 'withdrawn' ? '' : 'warn'));
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
      <li class="record">
        <div class="head">
          <span class="kind">${esc(KIND_LABEL[r.kind] || r.kind)}</span>
          <span class="time">${esc(fmtTime(r.at))}</span>
          <span class="state ${stCls}">${esc(STATE_LABEL[r.state] || r.state)}</span>
          <div class="actions">${actions.join('')}</div>
        </div>
        <div class="preview">${esc(r.preview || r.text || '（无文本）')}</div>
        ${progress}
        <div class="foot muted small">${esc(meta)}</div>
        ${r.error ? `<div class="err-msg">${esc(r.error)}</div>` : ''}
      </li>`;
  }).join('');
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
      <span class="muted small">${esc(f.url || f.host || '')}</span>
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
  if (!confirm('确定退出登录？本地会话会被删除。')) return;
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
      if (!confirm('撤回会同时删除服务器上的这条内容，继续？')) return;
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
  const url = $('url').value.trim();
  const username = $('username').value.trim();
  const password = $('password').value;
  const pin = $('trust').checked ? probedFP : '';
  $('login-err').textContent = '';
  if (!url) { $('login-err').textContent = '请填写服务器地址'; return; }
  if (!username) { $('login-err').textContent = '请填写用户名'; return; }
  $('login-btn').disabled = true;
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
    showMain();
  } catch (e) {
    $('login-err').textContent = errText(e);
  } finally {
    $('login-btn').disabled = false;
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

// ---------- 事件绑定 ----------

function bind() {
  $('pause-btn').addEventListener('click', doPause);
  $('logout-btn').addEventListener('click', doLogout);
  $('send-btn').addEventListener('click', doSend);
  $('pick-files-btn').addEventListener('click', doPickFiles);
  $('pick-folder-btn').addEventListener('click', doPickFolder);
  $('discover-btn').addEventListener('click', doDiscover);
  $('probe-btn').addEventListener('click', doProbe);
  $('login-btn').addEventListener('click', doLogin);
  $('save-settings-btn').addEventListener('click', doSaveSettings);
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
  bind();
  subscribe();
  try {
    state = await call('GetState');
    records = (await call('GetHistory')) || [];
    settings = await call('GetSettings');
    fillSettings(settings);
    renderState();
    renderRecords();
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
