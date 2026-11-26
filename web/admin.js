import { api } from './api.js';
import { initSettings, showSettings } from './settings.js';

const $ = (id) => document.getElementById(id);

function showAdminError(msg) {
  const el = $('admin-error');
  el.textContent = msg || '';
  el.hidden = !msg;
}

function userRow(u) {
  const li = document.createElement('li');
  const name = document.createElement('span');
  name.textContent = u.username;
  const tag = document.createElement('span');
  tag.className = 'tag';
  tag.textContent = u.is_admin ? '管理员' : '成员';
  const sp = document.createElement('span');
  sp.className = 'spacer';

  const reset = document.createElement('button');
  reset.type = 'button';
  reset.textContent = '重置密码';
  reset.addEventListener('click', async () => {
    const pw = prompt(`为 ${u.username} 设置新密码（至少 8 位）：`);
    if (!pw) return;
    try {
      await api.setUserPassword(u.username, pw);
      showAdminError('');
      alert(`已重置 ${u.username} 的密码`);
    } catch (err) {
      showAdminError(err.message);
    }
  });

  const del = document.createElement('button');
  del.type = 'button';
  del.className = 'danger';
  del.textContent = '删除';
  del.addEventListener('click', async () => {
    if (!confirm(`确定删除用户 ${u.username}？`)) return;
    try {
      await api.deleteUser(u.username);
      showAdminError('');
      await renderUsers();
    } catch (err) {
      showAdminError(err.message);
    }
  });

  li.append(name, tag, sp, reset, del);
  return li;
}

async function renderUsers() {
  const list = $('user-list');
  let users = [];
  try {
    users = await api.users();
  } catch (err) {
    showAdminError(err.message);
    return;
  }
  list.replaceChildren(...users.map(userRow));
}

function switchTab(name) {
  $('tab-users').classList.toggle('active', name === 'users');
  $('tab-settings').classList.toggle('active', name === 'settings');
  $('pane-users').hidden = name !== 'users';
  $('pane-settings').hidden = name !== 'settings';
  if (name === 'settings') {
    showSettings().catch((err) => {
      const el = $('settings-error');
      el.textContent = err.message;
      el.hidden = false;
    });
  }
}

export function initAdmin() {
  $('btn-admin').addEventListener('click', async () => {
    showAdminError('');
    $('admin-drawer').hidden = false;
    switchTab('users');
    await renderUsers();
  });
  $('btn-close-admin').addEventListener('click', () => {
    $('admin-drawer').hidden = true;
  });
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
      await renderUsers();
    } catch (err) {
      showAdminError(err.message);
    }
  });
  initSettings();
}
