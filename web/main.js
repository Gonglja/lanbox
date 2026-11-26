import { api, ApiError, uploadItem } from './api.js';
import { renderItem, fmtBytes } from './render.js';
import { connectEvents } from './sse.js';
import { initAdmin, closeAdminDrawer } from './admin.js';
import {
  fillIcons,
  initTheme,
  paintAvatar,
  setIcon,
  toast,
  confirmDialog,
} from './ui.js';

const $ = (id) => document.getElementById(id);

const state = {
  user: null,
  items: new Map(), // id -> item，最新在前
  cursor: '',
  filter: 'all',
  q: '',
  pending: [], // 待上传文件
  events: null,
  loaded: false,
};

// ---------- 视图切换 ----------

function showLogin() {
  $('login-view').hidden = false;
  $('app-view').hidden = true;
  closeAdminDrawer();
  closeSidebar();
  if (state.events) {
    state.events.close();
    state.events = null;
  }
  $('login-username').focus();
}

function showApp() {
  $('login-view').hidden = true;
  $('app-view').hidden = false;
  const role = state.user.is_admin ? '管理员' : '成员';
  $('me-name').textContent = state.user.username;
  $('me-role').textContent = role;
  paintAvatar($('me-avatar'), state.user.username);
  $('btn-admin').hidden = !state.user.is_admin;
  startEvents();
  syncSidebarForViewport();
}

// ---------- 移动端侧栏 ----------

function openSidebar() {
  $('app-view').classList.add('sidebar-open');
  $('sidebar-scrim').hidden = false;
}

function closeSidebar() {
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
  for (const item of state.items.values()) {
    nodes.push(renderItem(item, handlers));
  }
  box.replaceChildren(...nodes);
  $('btn-more').hidden = !state.cursor;
  updateCounts();
  updateEmptyState();
}

function updateEmptyState() {
  // 标题只跟筛选/搜索走，和是否有内容无关
  const searching = Boolean(state.q) || state.filter === 'todo';
  $('view-title').textContent = searching ? (state.q ? '搜索结果' : '未取走') : '时间线';

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
  $('login-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    $('login-error').hidden = true;
    try {
      state.user = await api.login($('login-username').value.trim(), $('login-password').value);
      $('login-password').value = '';
      showApp();
      await loadFirstPage();
    } catch (err) {
      $('login-error').textContent = err.message;
      $('login-error').hidden = false;
    }
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
  text.addEventListener('input', autoGrow);
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

  let searchTimer = null;
  $('search').addEventListener('input', (ev) => {
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
  window.addEventListener('resize', syncSidebarForViewport);
}

async function setFilter(f) {
  state.filter = f;
  $('filter-all').classList.toggle('is-active', f === 'all');
  $('filter-todo').classList.toggle('is-active', f === 'todo');
  closeSidebar();
  await loadFirstPage();
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
  initTheme();
  bind();
  initAdmin(() => state.user);
  try {
    state.user = await api.me();
    showApp();
    await loadFirstPage();
  } catch (err) {
    if (!(err instanceof ApiError) || err.status !== 401) {
      $('login-error').textContent = err.message;
      $('login-error').hidden = false;
    }
    showLogin();
  }
}

main();
