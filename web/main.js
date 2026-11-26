import { api, ApiError, uploadItem } from './api.js';
import { renderItem, fmtBytes } from './render.js';
import { connectEvents } from './sse.js';
import { initAdmin } from './admin.js';

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

function showError(el, err) {
  if (!el) return;
  el.textContent = err && err.message ? err.message : String(err);
  el.hidden = false;
}

function showLogin() {
  $('login-view').hidden = false;
  $('app-view').hidden = true;
  $('admin-drawer').hidden = true;
  if (state.events) {
    state.events.close();
    state.events = null;
  }
  $('login-username').focus();
}

function showApp() {
  $('login-view').hidden = true;
  $('app-view').hidden = false;
  $('who').textContent = `${state.user.username} · ${state.user.is_admin ? '管理员' : '成员'}`;
  $('btn-admin').hidden = !state.user.is_admin;
  startEvents();
}

// ---------- 列表 ----------

function renderList() {
  const box = $('items');
  const nodes = [];
  for (const item of state.items.values()) {
    nodes.push(renderItem(item, handlers));
  }
  box.replaceChildren(...nodes);
  $('btn-more').hidden = !state.cursor;
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
  const res = await api.list({ filter: state.filter, q: state.q });
  state.items = new Map(res.items.map((it) => [it.id, it]));
  state.cursor = res.next_cursor || '';
  state.loaded = true;
  renderList();
}

async function loadMore() {
  if (!state.cursor) return;
  const res = await api.list({ filter: state.filter, q: state.q, before: state.cursor });
  for (const it of res.items) state.items.set(it.id, it);
  state.cursor = res.next_cursor || '';
  renderList();
}

// ---------- 实时 ----------

function startEvents() {
  if (state.events) state.events.close();
  const badge = $('sse-state');
  state.events = connectEvents({
    onState(s) {
      badge.classList.remove('on', 'off');
      if (s === 'open') {
        badge.classList.add('on');
        badge.textContent = '实时已连接';
      } else if (s === 'connecting') {
        badge.textContent = '连接中…';
      } else {
        badge.classList.add('off');
        badge.textContent = '连接断开，重试中';
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
          loadFirstPage().catch(() => {});
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
    name.textContent = f.relPath || f.name;
    const size = document.createElement('span');
    size.className = 'fmeta';
    size.textContent = fmtBytes(f.size);
    const sp = document.createElement('span');
    sp.className = 'spacer';
    const rm = document.createElement('button');
    rm.type = 'button';
    rm.textContent = '移除';
    rm.addEventListener('click', () => {
      state.pending.splice(idx, 1);
      refreshPending();
    });
    li.append(name, size, sp, rm);
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
}

function addFiles(files) {
  for (const f of files) state.pending.push(f);
  refreshPending();
}

async function submitComposer(ev) {
  ev.preventDefault();
  const text = $('composer-text').value.trim();
  if (!text && state.pending.length === 0) return;

  const btn = $('btn-send');
  btn.disabled = true;
  const box = $('progress');
  const bar = $('progress-bar');
  const label = $('progress-text');
  box.hidden = false;
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
    label.textContent = '上传完成';
    await loadFirstPage();
  } catch (err) {
    bar.style.width = '100%';
    label.textContent = '上传失败：' + (err.message || err);
  } finally {
    btn.disabled = false;
    setTimeout(() => {
      box.hidden = true;
    }, 1500);
  }
}

// ---------- 卡片操作 ----------

const handlers = {
  async onToggleTaken(item) {
    try {
      const updated = await api.setTaken(item.id, !item.taken);
      if (state.filter === 'todo' && updated.taken) {
        removeItem(updated.id);
      } else {
        upsert(updated);
      }
    } catch (err) {
      alert(err.message);
    }
  },
  async onDelete(item) {
    const extra = item.files && item.files.length ? `（含 ${item.files.length} 个附件）` : '';
    if (!confirm(`确定删除这条内容？${extra}\n删除后无法恢复。`)) return;
    try {
      await api.remove(item.id);
      removeItem(item.id);
    } catch (err) {
      alert(err.message);
    }
  },
  async onDeleteFile(item, file) {
    if (!confirm(`确定删除附件「${file.name}」？删除后无法恢复。`)) return;
    try {
      await api.removeFile(item.id, file.id);
      const fresh = await api.getItem(item.id);
      upsert(fresh);
    } catch (err) {
      alert(err.message);
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
      showError($('login-error'), err);
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

  const composer = $('composer');
  composer.addEventListener('dragover', (ev) => {
    ev.preventDefault();
    composer.classList.add('drag');
  });
  composer.addEventListener('dragleave', () => composer.classList.remove('drag'));
  composer.addEventListener('drop', async (ev) => {
    ev.preventDefault();
    composer.classList.remove('drag');
    try {
      const files = await collectDroppedFiles(ev.dataTransfer);
      if (files.length) addFiles(files);
    } catch (err) {
      $('limit-hint').textContent = '读取拖入的文件失败：' + (err.message || err);
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
      loadFirstPage().catch((err) => alert(err.message));
    }, 300);
  });

  $('btn-more').addEventListener('click', () => loadMore().catch((err) => alert(err.message)));
}

async function setFilter(f) {
  state.filter = f;
  $('filter-all').classList.toggle('active', f === 'all');
  $('filter-todo').classList.toggle('active', f === 'todo');
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
  bind();
  initAdmin();
  try {
    state.user = await api.me();
    showApp();
    await loadFirstPage();
  } catch (err) {
    if (!(err instanceof ApiError) || err.status !== 401) {
      showError($('login-error'), err);
    }
    showLogin();
  }
}

main();
