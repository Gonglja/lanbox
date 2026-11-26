// ui.js：图标、提示消息、对话框与主题。不引入任何第三方库（CSP 只允许 'self'）。

// ---------- 图标 ----------

const SVG_NS = 'http://www.w3.org/2000/svg';

// 每个图标是一组 path 的 d 属性，统一 24×24 视口、描边绘制。
const ICONS = {
  timeline: ['M4 6h16', 'M4 12h10', 'M4 18h13'],
  'circle-dashed': ['M12 3.5a8.5 8.5 0 1 0 8.5 8.5', 'M12 7.5a4.5 4.5 0 1 1-4.5 4.5'],
  search: ['M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14Z', 'M20 20l-4.2-4.2'],
  auto: ['M4 5.5h16v11H4z', 'M9 20h6', 'M12 16.5V20'],
  sun: [
    'M12 8.2a3.8 3.8 0 1 0 0 7.6 3.8 3.8 0 0 0 0-7.6Z',
    'M12 2.6v2.2',
    'M12 19.2v2.2',
    'M4.6 4.6l1.5 1.5',
    'M17.9 17.9l1.5 1.5',
    'M2.6 12h2.2',
    'M19.2 12h2.2',
    'M4.6 19.4l1.5-1.5',
    'M17.9 6.1l1.5-1.5',
  ],
  moon: ['M20 13.4A8.2 8.2 0 0 1 10.6 4a8.4 8.4 0 1 0 9.4 9.4Z'],
  paper: ['M6.5 3.5h7.4L18.5 8v12.5h-12Z', 'M13.7 3.7V8h4.4', 'M9.2 12.2h6', 'M9.2 15.4h4.2'],
  settings: [
    'M12 15.4a3.4 3.4 0 1 0 0-6.8 3.4 3.4 0 0 0 0 6.8Z',
    'M19.2 14.4a1.5 1.5 0 0 0 .3 1.6l.1.1a1.8 1.8 0 1 1-2.6 2.6l-.1-.1a1.5 1.5 0 0 0-2.6 1.1v.3a1.8 1.8 0 1 1-3.6 0v-.2a1.5 1.5 0 0 0-2.6-1.1l-.1.1a1.8 1.8 0 1 1-2.6-2.6l.1-.1a1.5 1.5 0 0 0-1.1-2.6h-.3a1.8 1.8 0 1 1 0-3.6h.2a1.5 1.5 0 0 0 1.1-2.6l-.1-.1a1.8 1.8 0 1 1 2.6-2.6l.1.1a1.5 1.5 0 0 0 2.6-1.1v-.3a1.8 1.8 0 1 1 3.6 0v.2a1.5 1.5 0 0 0 2.6 1.1l.1-.1a1.8 1.8 0 1 1 2.6 2.6l-.1.1a1.5 1.5 0 0 0 1.1 2.6h.3a1.8 1.8 0 1 1 0 3.6h-.2a1.5 1.5 0 0 0-1.3.9Z',
  ],
  logout: ['M15 8V5.5A1.5 1.5 0 0 0 13.5 4h-7A1.5 1.5 0 0 0 5 5.5v13A1.5 1.5 0 0 0 6.5 20h7a1.5 1.5 0 0 0 1.5-1.5V16', 'M11 12h9', 'M17 9l3 3-3 3'],
  menu: ['M4 7h16', 'M4 12h16', 'M4 17h16'],
  close: ['M6 6l12 12', 'M18 6L6 18'],
  'chevron-left': ['M14.5 6l-6 6 6 6'],
  'chevron-right': ['M9.5 6l6 6-6 6'],
  'chevron-up': ['M6 14.5l6-6 6 6'],
  palette: [
    'M12 20.4a8.4 8.4 0 1 1 8.4-8.4c0 2.1-1.7 3.3-3.6 3.3h-1.3a2 2 0 0 0-1.4 3.4 1.6 1.6 0 0 1-1.2 2.7Z',
    'M7.6 12.4v.1',
    'M9.6 8.4v.1',
    'M14.2 8v.1',
  ],
  paperclip: ['M20 11.5 12.3 19a4.6 4.6 0 0 1-6.6-6.6l7.8-7.8a3.1 3.1 0 0 1 4.4 4.4l-7.8 7.8a1.5 1.5 0 0 1-2.2-2.2l7.2-7.2'],
  folder: ['M3.5 7.5A1.5 1.5 0 0 1 5 6h4l2 2.2h8A1.5 1.5 0 0 1 20.5 9.7v8.8A1.5 1.5 0 0 1 19 20H5a1.5 1.5 0 0 1-1.5-1.5Z'],
  plus: ['M12 5v14', 'M5 12h14'],
  download: ['M12 4v11', 'M7.5 11l4.5 4.5 4.5-4.5', 'M5 19.5h14'],
  eye: ['M2.6 12S6.2 5.8 12 5.8 21.4 12 21.4 12 17.8 18.2 12 18.2 2.6 12 2.6 12Z', 'M12 9.4a2.6 2.6 0 1 0 0 5.2 2.6 2.6 0 0 0 0-5.2Z'],
  play: ['M9 6.4 18.2 12 9 17.6Z'],
  expand: ['M14 4h6v6', 'M20 4l-6.5 6.5', 'M10 20H4v-6', 'M4 20l6.5-6.5'],
  trash: ['M4.5 7h15', 'M9.5 7V5.2A1.2 1.2 0 0 1 10.7 4h2.6a1.2 1.2 0 0 1 1.2 1.2V7', 'M6.5 7l.8 11.8A1.4 1.4 0 0 0 8.7 20h6.6a1.4 1.4 0 0 0 1.4-1.2L17.5 7'],
  check: ['M5 12.5l4.5 4.5L19 7'],
  undo: ['M9 8H5V4', 'M5 8a8 8 0 1 1 2.5 8.5'],
  sparkles: ['M12 5l1.6 4.4L18 11l-4.4 1.6L12 17l-1.6-4.4L6 11l4.4-1.6Z', 'M18.5 4l.8 2.2 2.2.8-2.2.8-.8 2.2-.8-2.2-2.2-.8 2.2-.8Z'],
  archive: ['M3.5 6.5h17v3h-17z', 'M5 9.5V19a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V9.5', 'M10 13h4'],
  alert: ['M12 4.5 3.5 19.5h17Z', 'M12 9.5v4.2', 'M12 16.6v.1'],
  key: ['M14.5 4a5 5 0 1 0-3.9 8.1L4 18.7V21h3.5l1.6-1.6v-2h2v-2h1.4l1.9-1.9A5 5 0 0 0 19.5 9 4.9 4.9 0 0 0 14.5 4Z', 'M16.6 7.4v.1'],
  user: ['M12 12.4a3.9 3.9 0 1 0 0-7.8 3.9 3.9 0 0 0 0 7.8Z', 'M4.8 20c.9-3.6 3.8-5.6 7.2-5.6s6.3 2 7.2 5.6'],
  shield: ['M12 3.6 5 6.2v5.3c0 4.1 2.8 7.7 7 8.9 4.2-1.2 7-4.8 7-8.9V6.2Z', 'M9 12l2.2 2.2L15.4 10'],
  activity: ['M3 12h4l2.5-7 5 14 2.5-7h4'],
  qr: [
    'M4 4h5v5H4z',
    'M15 4h5v5h-5z',
    'M4 15h5v5H4z',
    'M14.5 14.5h2v2h-2z',
    'M19 14.5h1v1h-1z',
    'M14.5 19.5h1v1h-1z',
    'M18 18h2v2h-2z',
  ],
  wifi: [
    'M4.5 10a11 11 0 0 1 15 0',
    'M8 13.2a6.6 6.6 0 0 1 8 0',
    'M10.4 16.2a2.8 2.8 0 0 1 3.2 0',
    'M12 19.4v.2',
  ],
  refresh: ['M20 12a8 8 0 1 1-2.3-5.7', 'M20.5 4.5V9H16'],
};

export function iconEl(name, extraClass) {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  for (const d of ICONS[name] || []) {
    const p = document.createElementNS(SVG_NS, 'path');
    p.setAttribute('d', d);
    svg.append(p);
  }
  const span = document.createElement('span');
  span.className = 'icon' + (extraClass ? ' ' + extraClass : '');
  span.append(svg);
  return span;
}

// fillIcons 把 HTML 里 <span class="icon" data-icon="x"></span> 换成真实 SVG。
export function fillIcons(root = document) {
  for (const el of root.querySelectorAll('[data-icon]')) {
    const name = el.dataset.icon;
    const svg = iconEl(name).firstChild;
    el.replaceChildren(svg);
  }
}

// setIcon 替换某个元素内部图标（先 removeAttribute 避免被 fillIcons 重复处理）。
export function setIcon(el, name) {
  if (!el) return;
  el.removeAttribute('data-icon');
  el.replaceChildren(iconEl(name).firstChild);
}

// ---------- 头像 ----------

// avatarFor 取用户名首字母（CJK 取首字），并给一个稳定的柔和色相。
function avatarFor(name) {
  const label = (name || '?').trim().slice(0, 2);
  return { label: label.toUpperCase(), hue: hashHue(name || '') };
}

function hashHue(s) {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
  return h;
}

// paintAvatar 只写文字与色相变量，取色交给 CSS，主题切换时自动跟随。
export function paintAvatar(el, name) {
  if (!el) return;
  const { label, hue } = avatarFor(name);
  el.textContent = label;
  el.style.setProperty('--av-h', String(hue));
}

// ---------- 提示消息 ----------

const TOAST_MS = { ok: 3200, error: 6000, info: 4000 };

export function toast(message, kind = 'info') {
  const root = document.getElementById('toasts');
  if (!root) return;
  const box = document.createElement('div');
  box.className = 'toast toast-' + kind;

  const glyph = iconEl(kind === 'error' ? 'alert' : kind === 'ok' ? 'check' : 'sparkles');
  const text = document.createElement('span');
  text.className = 'toast-text';
  text.textContent = message;

  const close = document.createElement('button');
  close.type = 'button';
  close.className = 'toast-x';
  close.setAttribute('aria-label', '关闭提示');
  close.append(iconEl('close').firstChild);

  box.append(glyph, text, close);
  root.append(box);

  let timer = 0;
  const drop = () => {
    clearTimeout(timer);
    box.remove();
  };
  close.addEventListener('click', drop);
  timer = setTimeout(drop, TOAST_MS[kind] || TOAST_MS.info);
  return drop;
}

// ---------- 对话框 ----------

let activeDialog = null;

function closeTopDialog(result) {
  if (!activeDialog) return;
  const { root, resolve } = activeDialog;
  activeDialog = null;
  root.hidden = true;
  root.replaceChildren();
  document.removeEventListener('keydown', onDialogKey, true);
  resolve(result);
}

function onDialogKey(ev) {
  if (!activeDialog) return;
  if (ev.key === 'Escape') {
    ev.preventDefault();
    ev.stopPropagation();
    closeTopDialog(null);
  }
}

// openDialog 是 confirm/prompt 的共用外壳；返回 Promise。
function openDialog({ title, message, body, confirmText, cancelText, danger, value }) {
  const root = document.getElementById('modal-root');
  if (!root) return Promise.resolve(null);
  if (activeDialog) closeTopDialog(null);

  const dialog = document.createElement('div');
  dialog.className = 'dialog';
  dialog.setAttribute('role', 'dialog');
  dialog.setAttribute('aria-modal', 'true');

  const h = document.createElement('h2');
  h.className = 'dialog-title';
  h.textContent = title;
  dialog.append(h);

  if (message) {
    const p = document.createElement('p');
    p.className = 'dialog-text';
    p.textContent = message;
    dialog.append(p);
  }

  let input = null;
  if (body === 'prompt') {
    const field = document.createElement('label');
    field.className = 'field';
    input = document.createElement('input');
    input.type = 'password';
    input.autocomplete = 'new-password';
    input.value = value || '';
    field.append(input);
    dialog.append(field);
  }

  const actions = document.createElement('div');
  actions.className = 'dialog-actions';

  const cancel = document.createElement('button');
  cancel.type = 'button';
  cancel.className = 'btn';
  cancel.textContent = cancelText || '取消';
  cancel.addEventListener('click', () => closeTopDialog(null));

  const ok = document.createElement('button');
  ok.type = 'button';
  ok.className = 'btn ' + (danger ? 'btn-danger' : 'btn-primary');
  ok.textContent = confirmText || '确定';

  actions.append(cancel, ok);
  dialog.append(actions);

  const finish = () => {
    if (input) {
      const v = input.value;
      if (!v) return;
      closeTopDialog(v);
      return;
    }
    closeTopDialog(true);
  };
  ok.addEventListener('click', finish);
  if (input) {
    input.addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter') {
        ev.preventDefault();
        finish();
      }
    });
  }

  root.replaceChildren(dialog);
  root.hidden = false;
  root.onclick = (ev) => {
    if (ev.target === root) closeTopDialog(null);
  };
  document.addEventListener('keydown', onDialogKey, true);

  const focusTarget = input || cancel;
  requestAnimationFrame(() => focusTarget.focus());

  return new Promise((resolve) => {
    activeDialog = { root, resolve };
  });
}

// confirmDialog 替代 window.confirm()。
export function confirmDialog({ title, message, confirmText = '确定', cancelText = '取消', danger = false }) {
  return openDialog({ title, message, confirmText, cancelText, danger }).then((v) => v === true);
}

// promptPassword 替代 window.prompt()：返回新密码或 null。
export function promptPassword({ title, message, confirmText = '保存' }) {
  return openDialog({ title, message, body: 'prompt', confirmText, cancelText: '取消' });
}

// ---------- 主题 ----------

const THEME_KEY = 'lanbox.theme';
const THEME_COLORS = { light: '#fafafa', dark: '#0f0f0f', paper: '#f5ede4' };
const validPrefs = ['system', 'light', 'dark', 'paper'];
const media = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;
let themePref = readPref();

function readPref() {
  try {
    const v = localStorage.getItem(THEME_KEY);
    if (validPrefs.includes(v)) return v;
  } catch {
    /* 隐私模式忽略 */
  }
  return 'system';
}

// resolvePref 把偏好落成 data-theme 的取值：light / dark / paper。
function resolvePref(pref) {
  if (pref === 'system') return media && media.matches ? 'dark' : 'light';
  return pref;
}

function applyTheme() {
  const theme = resolvePref(themePref);
  const root = document.documentElement;
  root.dataset.theme = theme;
  root.dataset.themePref = themePref;
  root.style.colorScheme = theme === 'dark' ? 'dark' : 'light';
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', THEME_COLORS[theme] || THEME_COLORS.light);
  for (const btn of document.querySelectorAll('#theme-picker [data-theme-value]')) {
    btn.setAttribute('aria-checked', String(btn.dataset.themeValue === themePref));
  }
  document.dispatchEvent(new CustomEvent('lanbox:theme'));
}

export function initTheme() {
  applyTheme();
  const picker = document.getElementById('theme-picker');
  if (picker) {
    picker.addEventListener('click', (ev) => {
      const btn = ev.target.closest('[data-theme-value]');
      if (!btn) return;
      themePref = btn.dataset.themeValue;
      try {
        localStorage.setItem(THEME_KEY, themePref);
      } catch {
        /* 存不下就只在本次生效 */
      }
      applyTheme();
    });
  }
  if (media) {
    const onChange = () => {
      if (themePref === 'system') applyTheme();
    };
    if (media.addEventListener) media.addEventListener('change', onChange);
    else if (media.addListener) media.addListener(onChange);
  }
}

// ---------- 自定义内容面板 ----------

let activePanel = null;

// openPanel 用与对话框一致的样式承载任意 DOM（扫码取景、服务器列表等）。
// 返回 { root, close, setBusy }；ESC、点遮罩、点右上角都会关闭并回调 onClose。
export function openPanel({ title, content, onClose }) {
  const host = document.getElementById('modal-root');
  if (!host) return { root: null, close() {}, setBusy() {} };
  closePanel();

  const dialog = document.createElement('div');
  dialog.className = 'dialog dialog-wide';
  dialog.setAttribute('role', 'dialog');
  dialog.setAttribute('aria-modal', 'true');

  const head = document.createElement('div');
  head.className = 'dialog-head';
  const h = document.createElement('h2');
  h.className = 'dialog-title';
  h.textContent = title || '';
  const close = document.createElement('button');
  close.type = 'button';
  close.className = 'btn btn-icon';
  close.setAttribute('aria-label', '关闭');
  close.title = '关闭（Esc）';
  close.append(iconEl('close'));
  head.append(h, close);
  dialog.append(head);

  const body = document.createElement('div');
  body.className = 'dialog-body';
  if (content) body.append(content);
  dialog.append(body);

  host.replaceChildren(dialog);
  host.hidden = false;

  const finish = () => {
    if (!activePanel) return;
    activePanel = null;
    host.hidden = true;
    host.replaceChildren();
    host.onclick = null;
    document.removeEventListener('keydown', onKey, true);
    if (typeof onClose === 'function') {
      try {
        onClose();
      } catch {
        /* 关闭回调不应影响界面 */
      }
    }
  };
  const onKey = (ev) => {
    if (ev.key === 'Escape' && activePanel) {
      ev.preventDefault();
      ev.stopPropagation();
      finish();
    }
  };

  close.addEventListener('click', finish);
  host.onclick = (ev) => {
    if (ev.target === host) finish();
  };
  document.addEventListener('keydown', onKey, true);
  activePanel = { root: dialog, finish };
  requestAnimationFrame(() => close.focus());

  return {
    root: dialog,
    close: finish,
    setBusy(on) {
      dialog.setAttribute('aria-busy', on ? 'true' : 'false');
    },
  };
}

// closePanel 关闭当前面板（若有）。
export function closePanel() {
  if (activePanel) activePanel.finish();
}
