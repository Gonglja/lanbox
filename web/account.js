// account.js：侧栏底部账号行 → 弹出菜单（外观二级菜单 / 管理 / 退出登录）。
//
// 菜单挂载在 <body> 流里用 position: fixed 定位：侧栏有 overflow-y:auto，
// 移动端抽屉还带 transform，放在侧栏内部会被裁剪或把 fixed 变成相对侧栏定位。
// 每次打开时按账号行的 getBoundingClientRect() 计算位置（默认向上弹）。
const GAP = 6;
const EDGE = 8;

let menu = null;
let anchor = null;
let themeTrigger = null;
let themePanel = null;
let open = false;

export function initAccountMenu() {
  menu = document.getElementById('account-menu');
  anchor = document.getElementById('btn-account');
  themeTrigger = document.getElementById('btn-theme-menu');
  themePanel = document.getElementById('theme-picker');
  if (!menu || !anchor || !themeTrigger || !themePanel) return;

  anchor.addEventListener('click', () => (open ? close() : show()));
  themeTrigger.addEventListener('click', () => setThemeOpen(isThemeOpen() ? false : true));

  // 进入管理 / 退出登录时收起菜单；主题选择后保持打开，方便连续对比。
  menu.addEventListener('click', (ev) => {
    const admin = ev.target.closest('#btn-admin');
    if (!admin && !ev.target.closest('#btn-logout')) return;
    close({ focus: false });
    if (admin) closeMobileSidebar(); // 移动端抽屉别留在管理页下面
  });

  document.addEventListener('keydown', onKey, true);
  document.addEventListener('pointerdown', onPointerDown, true);
  window.addEventListener('resize', () => open && place());
  // capture: true 同时能收到侧栏等容器内部的滚动
  window.addEventListener('scroll', () => open && place(), true);
}

export function closeAccountMenu() {
  close({ focus: false });
}

function show() {
  open = true;
  menu.hidden = false;
  anchor.setAttribute('aria-expanded', 'true');
  place();
  const first = visibleItems()[0];
  if (first) first.focus({ preventScroll: true });
}

function close({ focus = true } = {}) {
  if (!open) return;
  open = false;
  setThemeOpen(false);
  menu.hidden = true;
  menu.classList.remove('is-open');
  anchor.setAttribute('aria-expanded', 'false');
  if (focus) anchor.focus({ preventScroll: true });
}

// place 把菜单贴在账号行上方；空间不够时改到下方。
function place() {
  const r = anchor.getBoundingClientRect();
  const box = menu.getBoundingClientRect();
  let left = r.left;
  if (left + box.width > window.innerWidth - EDGE) left = window.innerWidth - EDGE - box.width;
  left = Math.max(EDGE, left);

  let top = r.top - box.height - GAP;
  if (top < EDGE) {
    top = Math.min(r.bottom + GAP, Math.max(EDGE, window.innerHeight - box.height - EDGE));
  }
  menu.style.left = `${Math.round(left)}px`;
  menu.style.top = `${Math.round(top)}px`;
}

function isThemeOpen() {
  return !themePanel.hidden;
}

function setThemeOpen(next) {
  themePanel.hidden = !next;
  themeTrigger.setAttribute('aria-expanded', String(next));
  themeTrigger.classList.toggle('is-open', next);
  // 窄屏是行内展开，菜单高度会变，重新贴一次
  if (open) requestAnimationFrame(place);
}

// visibleItems 返回当前可聚焦的菜单项（隐藏的管理项、收起的外观子菜单都跳过）。
function visibleItems() {
  if (!menu) return [];
  return [...menu.querySelectorAll('.menu-row, .menu-sub > button')].filter(
    (el) => !el.closest('[hidden]'),
  );
}

function onPointerDown(ev) {
  if (!open) return;
  if (menu.contains(ev.target) || anchor.contains(ev.target)) return;
  close({ focus: false });
}

// closeMobileSidebar 关掉移动端抽屉（侧栏的开合由 main.js 管理，这里只清状态）。
function closeMobileSidebar() {
  const app = document.getElementById('app-view');
  if (app) app.classList.remove('sidebar-open');
  const scrim = document.getElementById('sidebar-scrim');
  if (scrim) scrim.hidden = true;
}

function onKey(ev) {
  if (!open) return;

  if (ev.key === 'Escape') {
    ev.preventDefault();
    ev.stopPropagation();
    if (isThemeOpen()) {
      setThemeOpen(false);
      themeTrigger.focus();
      return;
    }
    close();
    return;
  }

  const insidePanel = themePanel.contains(document.activeElement);
  const inMenu = insidePanel || menu.contains(document.activeElement) || document.activeElement === anchor;
  if (!inMenu) return;

  if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
    const items = visibleItems();
    if (!items.length) return;
    ev.preventDefault();
    const idx = items.indexOf(document.activeElement);
    const step = ev.key === 'ArrowDown' ? 1 : -1;
    const next = idx < 0 ? 0 : (idx + step + items.length) % items.length;
    items[next].focus();
    return;
  }

  if (ev.key === 'ArrowRight' && document.activeElement === themeTrigger) {
    ev.preventDefault();
    setThemeOpen(true);
    const first = themePanel.querySelector('button');
    if (first) first.focus();
    return;
  }

  if (ev.key === 'ArrowLeft' && insidePanel) {
    ev.preventDefault();
    setThemeOpen(false);
    themeTrigger.focus();
  }
}
