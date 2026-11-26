import { api, ApiError, uploadItem } from './api.js';
import { renderItem, fmtBytes, dayKey, renderDay, refreshTimes } from './render.js';
import { renderMarkdown } from './markdown.js';
import { connectEvents } from './sse.js';
import { initAdmin, closeAdminDrawer } from './admin.js';
import { initAccountMenu, closeAccountMenu } from './account.js';
import { closeStatusPage, initStatusPage, openConnectPanel, openStatusPage } from './status.js';
import {
  fillIcons,
  initTheme,
  paintAvatar,
  setIcon,
  toast,
  confirmDialog,
} from './ui.js';

const $ = (id) => document.getElementById(id);

// hooks 供纯 web 客户端（client/app.js）挂接，不改变服务端 UI 的任何行为。
// showLogin：会话不可用，已回到登录页；showApp：已拿到身份并进入主界面。
export const hooks = { showLogin: [], showApp: [] };

function runHooks(name) {
  for (const fn of hooks[name]) {
    try {
      fn(state.user);
    } catch (err) {
      console.error(err);
    }
  }
}

const state = {
  user: null,
  items: new Map(), // id -> item，最新在前
  cursor: '',
  filter: 'all',
  view: 'timeline', // timeline（时间线）/ status（状态页）
  q: '',
  pending: [], // 待上传文件
  events: null,
  loaded: false,
  timesTimer: 0,
  dayStamp: '',
};

// ---------- 侧栏收合 ----------

const SIDEBAR_KEY = 'lanbox.sidebar';
// sidebarRail 为 true 时侧栏只留图标（宽度由 :root[data-sidebar='rail'] 控制）；
// theme-boot 在首帧前写入 data-sidebar，刷新不会先闪一下宽侧栏。
let sidebarRail = document.documentElement.dataset.sidebar === 'rail';

function applySidebar() {
  document.documentElement.dataset.sidebar = sidebarRail ? 'rail' : 'wide';
  const btn = $('btn-sidebar-collapse');
  if (!btn) return;
  const label = sidebarRail ? '展开侧栏' : '收起侧栏';
  btn.title = label;
  btn.setAttribute('aria-label', label);
  btn.setAttribute('aria-expanded', String(!sidebarRail));
  const icon = $('sidebar-toggle-icon');
  if (icon) setIcon(icon, sidebarRail ? 'chevron-right' : 'chevron-left');
}

function setSidebarRail(next) {
  closeAccountMenu();
  sidebarRail = Boolean(next);
  try {
    localStorage.setItem(SIDEBAR_KEY, sidebarRail ? 'rail' : 'wide');
  } catch {
    /* 隐私模式只在本次生效 */
  }
  applySidebar();
}

// ---------- 视图切换 ----------

// 登录状态：记住的用户名与错误提示都只影响登录卡片本身
const LOGIN_USER_KEY = 'lanbox.login.username';

function readRememberedUser() {
  try {
    return localStorage.getItem(LOGIN_USER_KEY) || '';
  } catch {
    return ''; // 隐私模式
  }
}

function saveRememberedUser(name) {
  try {
    if (name) localStorage.setItem(LOGIN_USER_KEY, name);
    else localStorage.removeItem(LOGIN_USER_KEY);
  } catch {
    /* 存不下就只在本次生效 */
  }
}

function clearLoginError() {
  $('login-error').hidden = true;
  $('login-username').removeAttribute('aria-invalid');
  $('login-password').removeAttribute('aria-invalid');
}

function showLoginError(message, focusEl) {
  const error = $('login-error');
  error.textContent = message;
  error.hidden = false;
  $('login-username').setAttribute('aria-invalid', 'true');
  $('login-password').setAttribute('aria-invalid', 'true');
  const card = $('login-form');
  card.classList.remove('is-shake');
  void card.offsetWidth; // 强制回流，让抖动可以重复播放
  card.classList.add('is-shake');
  if (focusEl) {
    focusEl.focus();
    if (focusEl.select) focusEl.select();
  }
}

function showLogin() {
  $('login-view').hidden = false;
  $('app-view').hidden = true;
  closeAdminDrawer();
  closeAccountMenu();
  closeSidebar();
  if (state.events) {
    state.events.close();
    state.events = null;
  }
  stopTicker();
  setView('timeline');

  // 重置登录卡片：不把上一位用户的密码或错误留在屏幕上
  const remembered = readRememberedUser();
  const username = $('login-username');
  const password = $('login-password');
  username.value = remembered;
  password.value = '';
  password.type = 'password';
  $('login-remember').checked = Boolean(remembered);
  $('login-caps').hidden = true;
  $('btn-toggle-password').setAttribute('aria-pressed', 'false');
  $('btn-toggle-password').setAttribute('aria-label', '显示密码');
  $('btn-toggle-password').title = '显示密码';
  clearLoginError();
  $('login-host').textContent = location.host || 'lanbox';
  (remembered ? password : username).focus();
  runHooks('showLogin');
}

function showApp() {
  $('login-view').hidden = true;
  $('app-view').hidden = false;
  const role = state.user.is_admin ? '管理员' : '成员';
  $('me-name').textContent = state.user.username;
  $('me-role').textContent = role;
  paintAvatar($('me-avatar'), state.user.username);
  paintAvatar($('composer-avatar'), state.user.username);
  $('btn-admin').hidden = !state.user.is_admin;
  setView('timeline');
  startEvents();
  startTicker();
  syncSidebarForViewport();
  runHooks('showApp');
}

// ---------- 时间显示 ----------

// 相对时间会随停留时间变旧（「刚刚」→「5 分钟前」），每 30 秒原地刷新一次；
// 跨零点时日期分隔行需要重排，顺带重建一次列表。
function startTicker() {
  stopTicker();
  state.dayStamp = new Date().toDateString();
  state.timesTimer = setInterval(() => {
    if (document.hidden) return;
    refreshTimes($('items'));
    const stamp = new Date().toDateString();
    if (stamp !== state.dayStamp) {
      state.dayStamp = stamp;
      renderList();
    }
  }, 30000);
}

function stopTicker() {
  if (state.timesTimer) {
    clearInterval(state.timesTimer);
    state.timesTimer = 0;
  }
}

// ---------- 移动端侧栏 ----------

function openSidebar() {
  $('app-view').classList.add('sidebar-open');
  $('sidebar-scrim').hidden = false;
}

function closeSidebar() {
  closeAccountMenu();
  $('app-view').classList.remove('sidebar-open');
  $('sidebar-scrim').hidden = true;
}

function syncSidebarForViewport() {
  // 宽屏时侧栏常驻，遮罩与开合状态都清掉，避免残留
  if (window.innerWidth > 720) closeSidebar();
}

// ---------- 列表 ----------

function updateCounts() {
  // 列表是按当前筛选从服务端拉回来的，别的视图的数量无从得知；
  // 给非当前行标数字会误导（「全部 3」而实际有 5 条），所以只标当前行。
  const all = $('count-all');
  const todo = $('count-todo');
  all.textContent = '';
  todo.textContent = '';
  if (state.filter === 'all') {
    all.textContent = state.items.size ? String(state.items.size) : '';
  } else {
    todo.textContent = state.items.size ? String(state.items.size) : '';
  }
}

function renderList() {
  const box = $('items');
  const nodes = [];
  let lastDay = '';
  for (const item of state.items.values()) {
    const key = dayKey(item.created_at);
    if (key !== lastDay) {
      nodes.push(renderDay(item.created_at));
      lastDay = key;
    }
    nodes.push(renderItem(item, handlers));
  }
  box.replaceChildren(...nodes);
  $('btn-more').hidden = state.view === 'status' || !state.cursor;
  updateCounts();
  updateEmptyState();
}

function updateEmptyState() {
  // 状态页自己占满内容区，不和列表的空状态/标题抢显示
  if (state.view === 'status') return;
  // 默认视图不占标题栏；只有搜索/筛选时用标题说明当前列表
  const title = state.q ? '搜索结果' : state.filter === 'todo' ? '未取走' : '';
  $('view-title').textContent = title;
  $('view-title').hidden = !title;

  const hasAny = state.items.size > 0;
  $('empty').hidden = hasAny;
  if (hasAny) return;

  // 三种空状态：搜索无结果、未取走筛选为空、真的没有内容
  if (state.q) {
    setIcon($('empty-icon'), 'search');
    $('empty-title').textContent = '没有匹配的内容';
    $('empty-text').textContent = '换个关键词，或清空搜索再试。';
  } else if (state.filter === 'todo') {
    setIcon($('empty-icon'), 'check');
    $('empty-title').textContent = '没有未取走的内容';
    $('empty-text').textContent = '新内容发布后会出现在这里。';
  } else {
    setIcon($('empty-icon'), 'sparkles');
    $('empty-title').textContent = '这里还没有内容';
    $('empty-text').textContent = '在上方写点什么，或拖入文件发送给另一端。';
  }
}

function upsert(item) {
  state.items.set(item.id, item);
  // 保持按 id 倒序（服务端 id 字典序即时间序）
  const sorted = [...state.items.values()].sort((a, b) => (a.id < b.id ? 1 : -1));
  state.items = new Map(sorted.map((it) => [it.id, it]));
  renderList();
}

function removeItem(id) {
  state.items.delete(id);
  renderList();
}

async function loadFirstPage() {
  try {
    const res = await api.list({ filter: state.filter, q: state.q });
    state.items = new Map(res.items.map((it) => [it.id, it]));
    state.cursor = res.next_cursor || '';
    state.loaded = true;
    renderList();
  } catch (err) {
    toast(err.message, 'error');
  }
}

async function loadMore() {
  if (!state.cursor) return;
  try {
    const res = await api.list({ filter: state.filter, q: state.q, before: state.cursor });
    for (const it of res.items) state.items.set(it.id, it);
    state.cursor = res.next_cursor || '';
    renderList();
  } catch (err) {
    toast(err.message, 'error');
  }
}

// ---------- 实时 ----------

function startEvents() {
  if (state.events) state.events.close();
  const badge = $('sse-state');
  state.events = connectEvents({
    onState(s) {
      badge.classList.remove('is-on', 'is-off');
      if (s === 'open') {
        badge.classList.add('is-on');
        badge.querySelector('.sse-text').textContent = '实时已连接';
      } else if (s === 'connecting') {
        badge.querySelector('.sse-text').textContent = '连接中…';
      } else {
        badge.classList.add('is-off');
        badge.querySelector('.sse-text').textContent = '连接断开，重试中';
      }
    },
    onEvent(type, data) {
      let payload = null;
      try {
        payload = JSON.parse(data);
      } catch {
        return;
      }
      if (type === 'item.deleted' && payload.id) {
        removeItem(payload.id);
        return;
      }
      if (payload.item) {
        if (state.filter === 'todo' && payload.item.taken) {
          removeItem(payload.item.id);
          return;
        }
        if (state.q) {
          // 有搜索条件时以服务端命中为准，重新拉一次列表
          loadFirstPage();
          return;
        }
        upsert(payload.item);
      }
    },
  });
}

// ---------- 上传 ----------

function refreshPending() {
  const list = $('pending-files');
  const nodes = state.pending.map((f, idx) => {
    const li = document.createElement('li');
    const name = document.createElement('span');
    name.className = 'chip-name';
    name.textContent = f.relPath || f.name;
    name.title = f.relPath || f.name;
    const size = document.createElement('span');
    size.className = 'chip-size';
    size.textContent = fmtBytes(f.size);
    const rm = document.createElement('button');
    rm.type = 'button';
    rm.className = 'chip-x';
    rm.title = '移除';
    rm.setAttribute('aria-label', `移除 ${f.relPath || f.name}`);
    rm.textContent = '×';
    rm.addEventListener('click', () => {
      state.pending.splice(idx, 1);
      refreshPending();
    });
    li.append(name, size, rm);
    return li;
  });
  list.replaceChildren(...nodes);

  const total = state.pending.reduce((n, f) => n + f.size, 0);
  let hint = state.pending.length
    ? `待上传 ${state.pending.length} 个文件，共 ${fmtBytes(total)}`
    : '';
  // 与服务端默认单条附件数上限（500）对齐的提前提示，真实上限以服务端返回为准。
  // 放这里而不是 addFiles 之前，否则会在刷新列表时被覆盖掉。
  if (state.pending.length > 500) hint += '，超过单条默认上限 500 个，可能被服务端拒绝';
  $('limit-hint').textContent = hint;
  autoGrow();
}

function addFiles(files) {
  let added = 0;
  for (const f of files) {
    state.pending.push(f);
    added++;
  }
  if (!added) return;
  refreshPending();
}

function autoGrow() {
  const el = $('composer-text');
  el.style.height = 'auto';
  el.style.height = Math.min(el.scrollHeight, 240) + 'px';
}

// ---------- Markdown 预览 ----------

let previewOpen = false;

function refreshPreview() {
  const box = $('composer-preview');
  const text = $('composer-text').value;
  if (!previewOpen || !text.trim()) {
    box.hidden = true;
    box.replaceChildren();
    return;
  }
  renderMarkdown(box, text);
  box.hidden = false;
}

function setPreviewOpen(next) {
  previewOpen = Boolean(next);
  const btn = $('btn-md-preview');
  btn.classList.toggle('is-active', previewOpen);
  btn.setAttribute('aria-pressed', String(previewOpen));
  refreshPreview();
  autoGrow();
}

async function submitComposer(ev) {
  ev.preventDefault();
  const text = $('composer-text').value.trim();
  if (!text && state.pending.length === 0) return;

  const btn = $('btn-send');
  btn.disabled = true;
  const composer = $('composer');
  const box = $('progress');
  const bar = $('progress-bar');
  const label = $('progress-text');
  box.hidden = false;
  composer.classList.add('has-progress');
  bar.style.width = '0%';
  label.textContent = '准备上传…';

  try {
    await uploadItem({
      text,
      files: state.pending,
      onProgress(loaded, total) {
        const pct = Math.round((loaded / total) * 100);
        bar.style.width = pct + '%';
        label.textContent = `上传中 ${pct}%（${fmtBytes(loaded)} / ${fmtBytes(total)}）`;
      },
    });
    $('composer-text').value = '';
    state.pending = [];
    refreshPending();
    refreshPreview();
    autoGrow();
    label.textContent = '上传完成';
    await loadFirstPage();
  } catch (err) {
    bar.style.width = '100%';
    label.textContent = '上传失败：' + (err.message || err);
  } finally {
    btn.disabled = false;
    setTimeout(() => {
      box.hidden = true;
      composer.classList.remove('has-progress');
    }, 1500);
  }
}

// ---------- 卡片操作 ----------

const handlers = {
  async onToggleTaken(item) {
    try {
      const updated = (await api.setTaken(item.id, !item.taken)) || (await api.getItem(item.id));
      if (!updated || !updated.id) return;
      if (state.filter === 'todo' && updated.taken) {
        removeItem(updated.id);
        toast('已标记取走');
      } else {
        upsert(updated);
      }
    } catch (err) {
      toast(err.message, 'error');
    }
  },
  async onDelete(item) {
    const extra = item.files && item.files.length ? `（含 ${item.files.length} 个附件）` : '';
    const ok = await confirmDialog({
      title: '删除这条内容？',
      message: `删除后无法恢复。${extra}`,
      confirmText: '删除',
      danger: true,
    });
    if (!ok) return;
    try {
      await api.remove(item.id);
      removeItem(item.id);
      toast('已删除');
    } catch (err) {
      toast(err.message, 'error');
    }
  },
  async onDeleteFile(item, file) {
    const ok = await confirmDialog({
      title: '删除附件？',
      message: `「${file.name}」删除后无法恢复。`,
      confirmText: '删除',
      danger: true,
    });
    if (!ok) return;
    try {
      await api.removeFile(item.id, file.id);
      const fresh = await api.getItem(item.id);
      upsert(fresh);
      toast('附件已删除');
    } catch (err) {
      toast(err.message, 'error');
    }
  },
};

// ---------- 事件绑定 ----------

function bind() {
  const loginForm = $('login-form');
  const usernameInput = $('login-username');
  const passwordInput = $('login-password');
  const submitBtn = $('login-submit');

  loginForm.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    if (submitBtn.disabled) return; // 连点 / 回车重入时忽略
    const username = usernameInput.value.trim();
    const password = passwordInput.value;
    if (!username) {
      showLoginError('请输入用户名', usernameInput);
      return;
    }
    if (!password) {
      showLoginError('请输入密码', passwordInput);
      return;
    }

    clearLoginError();
    submitBtn.disabled = true;
    submitBtn.classList.add('is-busy');
    submitBtn.setAttribute('aria-busy', 'true');
    submitBtn.textContent = '正在登录…';
    try {
      state.user = await api.login(username, password);
      saveRememberedUser($('login-remember').checked ? username : '');
      passwordInput.value = '';
      showApp();
      await loadFirstPage();
    } catch (err) {
      showLoginError(err.message, passwordInput);
    } finally {
      submitBtn.disabled = false;
      submitBtn.classList.remove('is-busy');
      submitBtn.removeAttribute('aria-busy');
      submitBtn.textContent = '登录';
    }
  });

  // 输入即清掉上次的错误，避免旧提示一直挂着
  for (const el of [usernameInput, passwordInput]) el.addEventListener('input', clearLoginError);

  // 取消勾选即忘掉已存用户名，不必等到下次登录成功
  $('login-remember').addEventListener('change', (ev) => {
    if (!ev.target.checked) saveRememberedUser('');
  });

  $('btn-toggle-password').addEventListener('click', () => {
    const show = passwordInput.type === 'password';
    passwordInput.type = show ? 'text' : 'password';
    const btn = $('btn-toggle-password');
    btn.setAttribute('aria-pressed', String(show));
    const label = show ? '隐藏密码' : '显示密码';
    btn.setAttribute('aria-label', label);
    btn.title = label;
    passwordInput.focus();
    const end = passwordInput.value.length;
    try {
      passwordInput.setSelectionRange(end, end);
    } catch {
      /* 部分浏览器不支持 */
    }
  });

  // 大写锁定是很常见的登录失败原因，直接在密码框下提示
  const syncCaps = (ev) => {
    const on = typeof ev.getModifierState === 'function' && ev.getModifierState('CapsLock');
    $('login-caps').hidden = !on;
  };
  passwordInput.addEventListener('keydown', syncCaps);
  passwordInput.addEventListener('keyup', syncCaps);
  passwordInput.addEventListener('blur', () => {
    $('login-caps').hidden = true;
  });

  $('btn-logout').addEventListener('click', async () => {
    try {
      await api.logout();
    } catch {
      /* 忽略 */
    }
    state.user = null;
    state.items.clear();
    state.loaded = false;
    showLogin();
  });

  $('composer').addEventListener('submit', submitComposer);
  $('btn-pick-files').addEventListener('click', () => $('composer-files').click());
  $('btn-pick-dir').addEventListener('click', () => $('composer-dir').click());
  $('btn-md-preview').addEventListener('click', () => setPreviewOpen(!previewOpen));

  $('composer-files').addEventListener('change', (ev) => {
    addFiles(ev.target.files);
    ev.target.value = '';
  });

  $('composer-dir').addEventListener('change', (ev) => {
    const files = Array.from(ev.target.files || []).map((f) => {
      f.relPath = f.webkitRelativePath || f.name;
      return f;
    });
    if (files.length) addFiles(files);
    ev.target.value = '';
  });

  const text = $('composer-text');
  text.addEventListener('input', () => {
    autoGrow();
    refreshPreview();
  });
  text.addEventListener('keydown', (ev) => {
    if ((ev.ctrlKey || ev.metaKey) && ev.key === 'Enter') {
      ev.preventDefault();
      $('composer').requestSubmit();
    }
  });

  const composer = $('composer');
  let dragDepth = 0;
  composer.addEventListener('dragenter', (ev) => {
    ev.preventDefault();
    dragDepth++;
    composer.classList.add('is-drag');
  });
  composer.addEventListener('dragover', (ev) => {
    ev.preventDefault();
    if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'copy';
  });
  composer.addEventListener('dragleave', () => {
    dragDepth = Math.max(0, dragDepth - 1);
    if (!dragDepth) composer.classList.remove('is-drag');
  });
  composer.addEventListener('drop', async (ev) => {
    ev.preventDefault();
    dragDepth = 0;
    composer.classList.remove('is-drag');
    try {
      const files = await collectDroppedFiles(ev.dataTransfer);
      if (files.length) addFiles(files);
      else toast('没有可上传的文件', 'error');
    } catch (err) {
      toast('读取拖入的文件失败：' + (err.message || err), 'error');
    }
  });

  document.addEventListener('paste', (ev) => {
    if ($('app-view').hidden) return;
    const files = [...((ev.clipboardData && ev.clipboardData.files) || [])];
    if (files.length) {
      ev.preventDefault();
      addFiles(files);
    }
  });

  $('filter-all').addEventListener('click', () => setFilter('all'));
  $('filter-todo').addEventListener('click', () => setFilter('todo'));
  $('filter-status').addEventListener('click', () => {
    closeAdminDrawer();
    closeSidebar();
    setView('status');
  });
  // 扫码连接：任何页面都能打（纯 web 客户端没有这个入口，可选绑定）
  $('btn-connect-qr')?.addEventListener('click', () => {
    closeAdminDrawer();
    closeSidebar();
    openConnectPanel();
  });

  let searchTimer = null;
  $('search').addEventListener('input', (ev) => {
    closeAdminDrawer();
    if (state.view === 'status') setView('timeline');
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      state.q = ev.target.value.trim();
      loadFirstPage();
    }, 300);
  });

  $('btn-more').addEventListener('click', loadMore);

  $('btn-sidebar-open').addEventListener('click', openSidebar);
  $('btn-sidebar-close').addEventListener('click', closeSidebar);
  $('sidebar-scrim').addEventListener('click', closeSidebar);
  $('btn-sidebar-collapse').addEventListener('click', () => setSidebarRail(!sidebarRail));
  $('btn-rail-search').addEventListener('click', () => {
    setSidebarRail(false);
    setTimeout(() => $('search').focus(), 0);
  });
  window.addEventListener('resize', syncSidebarForViewport);
}

async function setFilter(f) {
  closeAdminDrawer();
  state.filter = f;
  setView('timeline');
  closeSidebar();
  await loadFirstPage();
}

// setView 切换时间线与状态页：状态页藏起发布区/列表/分页，只留统计卡片。
function setView(view) {
  state.view = view === 'status' ? 'status' : 'timeline';
  const status = state.view === 'status';
  $('status-view').hidden = !status;
  $('composer').hidden = status;
  $('items').hidden = status;
  $('empty').hidden = status || state.items.size > 0;
  $('btn-more').hidden = status || !state.cursor;
  $('view-title').textContent = status ? '状态' : state.q ? '搜索结果' : state.filter === 'todo' ? '未取走' : '';
  $('view-title').hidden = !$('view-title').textContent;
  $('filter-all').classList.toggle('is-active', !status && state.filter === 'all');
  $('filter-todo').classList.toggle('is-active', !status && state.filter === 'todo');
  $('filter-status').classList.toggle('is-active', status);
  if (status) {
    openStatusPage();
  } else {
    closeStatusPage();
    updateEmptyState();
  }
}

// collectDroppedFiles 把拖入的 DataTransfer 展开成带 relPath 的 File 列表。
// 目录递归用 webkitGetAsEntry；不支持时退回 dataTransfer.files。
async function collectDroppedFiles(dataTransfer) {
  const out = [];
  const walk = async (entry, prefix) => {
    if (entry.isFile) {
      const file = await new Promise((res, rej) => entry.file(res, rej));
      file.relPath = prefix + entry.name;
      out.push(file);
      return;
    }
    if (!entry.isDirectory) return;
    const reader = entry.createReader();
    for (;;) {
      const batch = await new Promise((res, rej) => reader.readEntries(res, rej));
      if (!batch.length) break;
      for (const child of batch) await walk(child, prefix + entry.name + '/');
    }
  };
  const items = Array.from(dataTransfer.items || []);
  for (const item of items) {
    const entry = item.webkitGetAsEntry ? item.webkitGetAsEntry() : null;
    if (entry) {
      await walk(entry, '');
      continue;
    }
    const file = item.getAsFile();
    if (file) {
      file.relPath = file.name;
      out.push(file);
    }
  }
  if (!items.length) {
    for (const file of Array.from(dataTransfer.files || [])) {
      file.relPath = file.webkitRelativePath || file.name;
      out.push(file);
    }
  }
  return out;
}

// ---------- 启动 ----------

async function main() {
  fillIcons();
  applySidebar();
  initTheme();
  bind();
  initAdmin(() => state.user);
  initAccountMenu();
  initStatusPage();
  try {
    state.user = await api.me();
    showApp();
    await loadFirstPage();
  } catch (err) {
    showLogin();
    if (!(err instanceof ApiError) || err.status !== 401) {
      showLoginError(err.message);
    }
  }
}

// ---------- 启动 ----------

// boot 供纯 web 客户端接管启动时机（它要先挂好 hooks 再让 main() 探测会话）。
export function boot() {
  return main();
}

if (!window.__LANBOX_DEFER_BOOT__) main();
