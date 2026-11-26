import { api } from './api.js';
import { initSettings, showSettings } from './settings.js';
import { confirmDialog, iconEl, paintAvatar, promptPassword, toast } from './ui.js';

const $ = (id) => document.getElementById(id);

function showAdminError(msg) {
  const el = $('admin-error');
  el.textContent = msg || '';
  el.hidden = !msg;
}

function fmtDay(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const pad = (x) => String(x).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

function userRow(u, me) {
  const li = document.createElement('li');

  const avatar = document.createElement('span');
  avatar.className = 'avatar';
  paintAvatar(avatar, u.username);

  const main = document.createElement('span');
  main.className = 'user-main';
  const name = document.createElement('span');
  name.className = 'user-name';
  name.textContent = u.username;
  if (me && me.username === u.username) {
    const you = document.createElement('span');
    you.className = 'user-you';
    you.textContent = '（你）';
    name.append(you);
  }
  const meta = document.createElement('span');
  meta.className = 'user-meta';
  const since = fmtDay(u.created_at);
  meta.textContent = (u.is_admin ? '管理员' : '成员') + (since ? ' · ' + since : '');
  main.append(name, meta);

  const sp = document.createElement('span');
  sp.className = 'spacer';

  const acts = document.createElement('span');
  acts.className = 'user-acts';

  const reset = document.createElement('button');
  reset.type = 'button';
  reset.className = 'btn btn-sm';
  reset.append(iconEl('key'), document.createTextNode('重置密码'));
  reset.addEventListener('click', async () => {
    const pw = await promptPassword({
      title: `为 ${u.username} 设置新密码`,
      message: '至少 8 位。保存后该用户所有会话立即失效。',
    });
    if (!pw) return;
    try {
      await api.setUserPassword(u.username, pw);
      showAdminError('');
      toast(`已重置 ${u.username} 的密码`, 'ok');
    } catch (err) {
      showAdminError(err.message);
    }
  });

  const del = document.createElement('button');
  del.type = 'button';
  del.className = 'btn btn-sm btn-danger';
  del.append(iconEl('trash'), document.createTextNode('删除'));
  del.addEventListener('click', async () => {
    const ok = await confirmDialog({
      title: `删除用户 ${u.username}？`,
      message: '该账号及其会话会立即失效，此操作无法恢复。',
      confirmText: '删除',
      danger: true,
    });
    if (!ok) return;
    try {
      await api.deleteUser(u.username);
      showAdminError('');
      toast(`已删除 ${u.username}`, 'ok');
      await renderUsers();
    } catch (err) {
      showAdminError(err.message);
    }
  });

  acts.append(reset, del);
  li.append(avatar, main, sp, acts);
  return li;
}

let currentUser = null;
let userSource = null;

async function renderUsers() {
  const list = $('user-list');
  let users = [];
  try {
    users = await api.users();
  } catch (err) {
    showAdminError(err.message);
    return;
  }
  const me = (userSource && userSource()) || currentUser;
  currentUser = me;
  list.replaceChildren(...users.map((u) => userRow(u, me)));
}

function switchTab(name) {
  const users = name === 'users';
  $('tab-users').classList.toggle('is-active', users);
  $('tab-users').setAttribute('aria-selected', String(users));
  $('tab-settings').classList.toggle('is-active', !users);
  $('tab-settings').setAttribute('aria-selected', String(!users));
  $('pane-users').hidden = !users;
  $('pane-settings').hidden = users;
  if (!users) {
    showSettings().catch((err) => {
      const el = $('settings-error');
      el.textContent = err.message;
      el.hidden = false;
    });
  }
}

function openAdmin(user) {
  if (user) currentUser = user;
  showAdminError('');
  $('admin-drawer').hidden = false;
  $('drawer-scrim').hidden = false;
  switchTab('users');
  renderUsers();
  $('btn-close-admin').focus();
}

export function closeAdminDrawer() {
  $('admin-drawer').hidden = true;
  $('drawer-scrim').hidden = true;
}

export function initAdmin(getUser) {
  userSource = getUser || null;
  $('btn-admin').addEventListener('click', () => {
    openAdmin((userSource && userSource()) || null);
  });
  $('btn-close-admin').addEventListener('click', closeAdminDrawer);
  $('drawer-scrim').addEventListener('click', closeAdminDrawer);
  $('tab-users').addEventListener('click', () => switchTab('users'));
  $('tab-settings').addEventListener('click', () => switchTab('settings'));

  $('user-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    showAdminError('');
    try {
      await api.createUser($('user-name').value.trim(), $('user-pass').value, $('user-admin').checked);
      $('user-name').value = '';
      $('user-pass').value = '';
      $('user-admin').checked = false;
      toast('用户已创建', 'ok');
      await renderUsers();
    } catch (err) {
      showAdminError(err.message);
    }
  });

  document.addEventListener('keydown', (ev) => {
    if (ev.key !== 'Escape') return;
    if (!$('admin-drawer').hidden) {
      ev.preventDefault();
      closeAdminDrawer();
      $('btn-admin').focus();
    }
  });

  initSettings();
}
