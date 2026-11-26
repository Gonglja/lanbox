// 统一的 API 封装：把服务端错误体转换成带 code 的 ApiError。
import { apiURL, apiToken, setToken } from './env.js';

export class ApiError extends Error {
  constructor(status, code, message) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function request(method, url, body) {
  const token = apiToken();
  // 有令牌时走 Bearer（纯 web 客户端）；没有时保持原来的同源 cookie 语义。
  const init = { method, credentials: token ? 'omit' : 'same-origin', headers: {} };
  if (token) init.headers['Authorization'] = 'Bearer ' + token;
  if (body !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(body);
  }
  const resp = await fetch(apiURL(url), init);
  if (resp.status === 204) return null;
  const text = await resp.text();
  let data = null;
  if (text) {
    try { data = JSON.parse(text); } catch { /* 非 JSON 响应 */ }
  }
  if (!resp.ok) {
    const err = data && data.error ? data.error : { code: 'unknown', message: text || resp.statusText };
    throw new ApiError(resp.status, err.code, err.message);
  }
  return data;
}

export const api = {
  me: () => request('GET', '/api/me'),
  stats: () => request('GET', '/api/stats'),
  // 连接信息（可用地址 + 二维码）与局域网实例发现
  connect: () => request('GET', '/api/connect'),
  discover: () => request('GET', '/api/discover'),
  login: async (username, password) => {
    const me = await request('POST', '/api/login', { username, password });
    // 服务端只在跨域请求的响应体里下发令牌（同源 UI 继续用 HttpOnly cookie）。
    if (me && me.token) setToken(me.token);
    return me;
  },
  logout: async () => {
    try {
      await request('POST', '/api/logout');
    } finally {
      setToken('');
    }
  },

  list: ({ limit = 50, before = '', q = '', filter = 'all' } = {}) => {
    const p = new URLSearchParams({ limit: String(limit), filter });
    if (before) p.set('before', before);
    if (q) p.set('q', q);
    return request('GET', '/api/items?' + p.toString());
  },
  getItem: (id) => request('GET', '/api/items/' + encodeURIComponent(id)),
  setTaken: (id, taken) => request('PATCH', '/api/items/' + encodeURIComponent(id), { taken }),
  remove: (id) => request('DELETE', '/api/items/' + encodeURIComponent(id)),
  removeFile: (id, fid) =>
    request('DELETE', '/api/items/' + encodeURIComponent(id) + '/files/' + encodeURIComponent(fid)),

  users: () => request('GET', '/api/users'),
  createUser: (username, password, isAdmin) =>
    request('POST', '/api/users', { username, password, is_admin: isAdmin }),
  setUserPassword: (name, password) =>
    request('POST', '/api/users/' + encodeURIComponent(name) + '/password', { password }),
  deleteUser: (name) => request('DELETE', '/api/users/' + encodeURIComponent(name)),

  config: () => request('GET', '/api/config'),
  saveConfig: (values, restart = false) => request('PUT', '/api/config', { values, restart }),
  restart: () => request('POST', '/api/config/restart'),
  healthz: async () => {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), 1000);
    try {
      const resp = await fetch(apiURL('/api/healthz'), { signal: ctrl.signal, cache: 'no-store' });
      return resp.ok ? await resp.json() : null;
    } finally {
      clearTimeout(timer);
    }
  },
};

// uploadItem 用 XHR 上传以获得真实进度（fetch 没有上传进度）。
export function uploadItem({ text, files, onProgress }) {
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    if (text) fd.append('text', text);
    // Go 的 Part.FileName() 会丢弃目录信息，相对路径只能放在字段名里。
    for (const f of files) fd.append('files:' + (f.relPath || f.name), f, f.name);

    const token = apiToken();
    const xhr = new XMLHttpRequest();
    xhr.open('POST', apiURL('/api/items'));
    xhr.withCredentials = !token;
    if (token) xhr.setRequestHeader('Authorization', 'Bearer ' + token);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && onProgress) onProgress(e.loaded, e.total);
    };
    xhr.onload = () => {
      let data = null;
      try { data = JSON.parse(xhr.responseText); } catch { /* 忽略非 JSON */ }
      if (xhr.status >= 200 && xhr.status < 300 && data) {
        resolve(data);
        return;
      }
      const err = data && data.error ? data.error : { code: 'unknown', message: xhr.statusText };
      reject(new ApiError(xhr.status, err.code, err.message));
    };
    xhr.onerror = () => reject(new ApiError(0, 'network', '网络错误，上传中断'));
    xhr.onabort = () => reject(new ApiError(0, 'aborted', '上传已取消'));
    xhr.send(fd);
  });
}
