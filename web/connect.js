// connect.js：连接信息与二维码（服务端状态页与纯 web 客户端共用）。
//
// 二维码由服务端生成 SVG（internal/qr，零依赖），这里只负责拼地址与画图。
// 跨域客户端由 env.js 负责拼 base 与 ?token=，所以两边的调用代码完全一样。
import { authedURL } from './env.js';

// qrSrc 返回某个连接地址的二维码图片地址。
export function qrSrc(serverURL) {
  const path = '/api/connect/qr.svg' + (serverURL ? '?url=' + encodeURIComponent(serverURL) : '');
  return authedURL(path);
}

// renderQR 把二维码画进容器。
// 用 <img> 而不是内联 SVG：图片上下文里 SVG 不会执行脚本。
export function renderQR(box, serverURL, label) {
  if (!box) return;
  const img = document.createElement('img');
  img.className = 'qr-img';
  img.src = qrSrc(serverURL);
  img.alt = label ? `连接二维码：${label}` : '连接二维码';
  img.decoding = 'async';
  box.replaceChildren(img);
}

// fillURLOptions 把服务端给的地址列表填进 <select>。
export function fillURLOptions(select, urls, preferred) {
  if (!select) return '';
  const list = Array.isArray(urls) ? urls : [];
  select.replaceChildren(
    ...list.map((u) => {
      const opt = document.createElement('option');
      opt.value = u.url;
      opt.textContent = u.label ? `${u.url} · ${u.label}` : u.url;
      return opt;
    }),
  );
  const pick = list.some((u) => u.url === preferred) ? preferred : list[0]?.url || '';
  select.value = pick;
  select.hidden = list.length === 0;
  return pick;
}
