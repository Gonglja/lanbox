// 统一的 API 封装：把服务端错误体转换成带 code 的 ApiError。
export class ApiError extends Error {
  constructor(status, code, message) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function request(method, url, body) {
  const init = { method, credentials: 'same-origin', headers: {} };
  if (body !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(body);
  }
  const resp = await fetch(url, init);
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
  login: (username, password) => request('POST', '/api/login', { username, password }),
  logout: () => request('POST', '/api/logout'),

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
};

// uploadItem 用 XHR 上传以获得真实进度（fetch 没有上传进度）。
export function uploadItem({ text, files, onProgress }) {
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    if (text) fd.append('text', text);
    // Go 的 Part.FileName() 会丢弃目录信息，相对路径只能放在字段名里。
    for (const f of files) fd.append('files:' + (f.relPath || f.name), f, f.name);

    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/items');
    xhr.withCredentials = true;
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
