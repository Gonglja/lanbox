'use strict';

// lanbox-agent 图标集
// 与 web/ui.js 的 ICONS 完全一致：24×24 视口、描边绘制、1.75 线宽，
// 保证客户端窗口里的图标和服务端 Web UI 长得一模一样。
// 只额外提供 fillIcons：把 HTML 里 <span class="icon" data-icon="x"></span> 换成真实 SVG。

const SVG_NS = 'http://www.w3.org/2000/svg';

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
  refresh: ['M20 12a8 8 0 1 1-2.3-5.7', 'M20.5 4.5V9H16'],
};

function iconEl(name, extraClass) {
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

// fillIcons 把 <span class="icon" data-icon="x"></span> 换成真实 SVG。
function fillIcons(root) {
  for (const el of (root || document).querySelectorAll('[data-icon]')) {
    const name = el.dataset.icon;
    const svg = iconEl(name).firstChild;
    el.replaceChildren(svg);
  }
}

window.fillIcons = fillIcons;
