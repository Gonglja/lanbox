// env.js：把「服务端同源 Web UI」与「异地纯 web 客户端」的差异收敛到一处。
//
// 服务端 Web UI 从不设置这两个全局量，所有函数退化为原行为（相对路径 + sid cookie）；
// 纯 web 客户端（client/）在启动时写入服务器地址与 Bearer 令牌，于是同一套模块
// 可以跨域对着任意 lanbox 服务端工作。

// apiBase 返回服务端根地址（末尾无斜杠），同源时为空串。
export function apiBase() {
  const b = window.__LANBOX_BASE__;
  return typeof b === 'string' ? b.replace(/\/+$/, '') : '';
}

// apiToken 返回当前 Bearer 令牌，同源时不使用。
export function apiToken() {
  const t = window.__LANBOX_TOKEN__;
  return typeof t === 'string' ? t : '';
}

// setToken 写入/清除内存中的令牌（持久化由调用方负责）。
export function setToken(token) {
  window.__LANBOX_TOKEN__ = token || '';
}

// apiURL 把以 / 开头的接口路径拼到服务端根地址上。
export function apiURL(path) {
  return apiBase() + path;
}

// authedURL 供 <img>、<video>、<a download>、EventSource 使用：
// 这些请求无法设置请求头，只能把令牌放进查询串（服务端仅对 GET/HEAD 接受 ?token=）。
export function authedURL(path) {
  const token = apiToken();
  if (!token) return apiURL(path);
  const sep = path.includes('?') ? '&' : '?';
  return apiURL(path) + sep + 'token=' + encodeURIComponent(token);
}
