// state.js：服务器地址、本机存储与「注入点」的小工具。
// app.js（连接层）与 discovery.js（服务器簿/扫码/发现）共用，避免互相 import。
import { apiBase, setToken } from '../web/env.js';

export const SERVER_KEY = 'lanbox.client.server';
export const TOKEN_KEY = 'lanbox.client.token';
export const DEFAULT_PORT = '10725';

const $ = (id) => document.getElementById(id);

// storage 包装 localStorage：隐私模式或 file:// 下可能不可用，那就只在本次会话里生效。
export const storage = {
  get(key) {
    try {
      return localStorage.getItem(key) || '';
    } catch {
      return '';
    }
  },
  set(key, value) {
    try {
      if (value) localStorage.setItem(key, value);
      else localStorage.removeItem(key);
    } catch {
      /* 存不下就只在内存里生效 */
    }
  },
};

// normalizeServer 把用户输入收拾成 origin：允许省略协议、带端口、带尾部斜杠，
// 但不接受路径/参数（客户端只对着服务根地址工作）。返回 { origin } 或 { error }。
export function normalizeServer(input) {
  const raw = String(input || '').trim();
  if (!raw) return { error: '请填写服务器地址，例如 https://192.168.1.9:10725' };
  let text = raw;
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(text)) text = 'https://' + text;
  let url;
  try {
    url = new URL(text);
  } catch {
    return { error: '地址格式不正确，例如 https://192.168.1.9 或 https://192.168.1.9:10725' };
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') {
    return { error: '只支持 https:// 或 http:// 地址' };
  }
  if ((url.pathname && url.pathname !== '/') || url.search || url.hash) {
    return { error: '只填服务器地址，不要带路径或参数' };
  }
  // 服务端默认监听 10725；省略端口时按默认端口连接。
  if (url.protocol === 'https:' && !url.port) url.port = DEFAULT_PORT;
  return { origin: url.origin };
}

export function hostOf(origin) {
  try {
    return new URL(origin).host;
  } catch {
    return origin;
  }
}

// applyConnection 写入共享模块（web/env.js）读取的两个「注入点」。
export function applyConnection(origin, token) {
  window.__LANBOX_BASE__ = origin || '';
  setToken(token || '');
}

export function syncLoginHost() {
  const el = $('login-host');
  if (el) el.textContent = apiBase() || '未设置服务器地址';
}

// setAuthStatus 显示连接页底部的状态行（发现结果、扫码提示等）。
export function setAuthStatus(text, kind = '') {
  const el = $('auth-status');
  if (!el) return;
  el.textContent = text || '';
  el.hidden = !text;
  el.classList.toggle('is-warn', kind === 'warn');
}
