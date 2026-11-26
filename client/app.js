// 纯 web 客户端的连接层。
//
// UI 主体（时间线、发布、Markdown 预览、媒体、主题、账号菜单）直接复用
// ../web/main.js —— 只有「连到哪台服务器、用什么凭据」是新代码。服务端 Web UI
// 从不设置 __LANBOX_BASE__/__LANBOX_TOKEN__，所以那份代码两边共用且行为不变。
import { api, ApiError } from '../web/api.js';
import { apiBase, apiToken, setToken } from '../web/env.js';
import {
  SERVER_KEY,
  TOKEN_KEY,
  applyConnection,
  hostOf,
  normalizeServer,
  setAuthStatus,
  storage,
  syncLoginHost,
} from './state.js';
import { initDiscovery, noteCurrentServer, openServerPanel } from './discovery.js';

const $ = (id) => document.getElementById(id);

function unreachable(origin) {
  const target = origin || '（未填写地址）';
  return `连不上 ${target}。请确认地址与端口正确、服务已启动；` +
    '若服务器用的是自签证书，请先在新标签直接打开该地址，选择「继续访问」后重试。';
}

// ---------- 顶栏徽标 ----------

function paintConnState(user) {
  const btn = $('conn-state');
  if (!btn) return;
  const text = btn.querySelector('.conn-text');
  const origin = apiBase();
  const on = Boolean(origin && user);
  btn.classList.toggle('is-on', on);
  btn.classList.toggle('is-off', !on);
  if (on) {
    text.textContent = hostOf(origin);
    btn.title = `${origin} · ${user.username} · 点击查看二维码/切换服务器`;
  } else {
    text.textContent = '未连接';
    btn.title = '切换服务器';
  }
}

// ---------- 启动顺序 ----------
// web/main.js 在被导入时就会执行 main()；客户端要先装好 hooks 再让它探测会话，
// 所以这里设置延后启动标志，并在装好 hooks 后显式调用 boot()。
window.__LANBOX_DEFER_BOOT__ = true;

const { hooks, boot } = await import('../web/main.js');

// sessionEnded 区分「会话真的结束了（401 / 主动退出）」与「只是暂时连不上」：
// 只有前者需要清掉落盘的令牌，否则服务器短暂离线一次就要重新输密码。
let sessionEnded = false;

hooks.showLogin.push(() => {
  // 内存里的令牌一律清掉（避免带着失效令牌发请求），保留地址与用户名方便重登。
  setToken('');
  if (sessionEnded) storage.set(TOKEN_KEY, '');
  paintConnState(null);
  syncLoginHost();
  if (!apiBase()) {
    // 首次打开还没有地址，探测会话的失败提示没有意义，直接引导去填地址。
    const err = $('login-error');
    if (err) {
      err.hidden = true;
      err.textContent = '';
    }
    for (const id of ['login-username', 'login-password']) $(id)?.removeAttribute('aria-invalid');
    $('server-input')?.focus();
  }
});

hooks.showApp.push((user) => {
  const origin = apiBase();
  const token = apiToken();
  sessionEnded = false;
  if (origin) storage.set(SERVER_KEY, origin);
  if (token) storage.set(TOKEN_KEY, token);
  const input = $('server-input');
  if (input) input.value = origin;
  paintConnState(user);
  noteCurrentServer(origin);
});

// ---------- 包一层 login/me：把跨域失败翻译成可操作的提示 ----------

const rawLogin = api.login;
api.login = async (username, password) => {
  const res = normalizeServer($('server-input')?.value);
  if (res.error) throw new ApiError(0, 'bad_server', res.error);
  // 换了地址就不能带着旧令牌过去（令牌只对签发它的服务器有效）
  if (res.origin !== apiBase()) applyConnection(res.origin, '');
  storage.set(SERVER_KEY, res.origin);
  const input = $('server-input');
  if (input) input.value = res.origin;
  try {
    return await rawLogin(username, password);
  } catch (err) {
    if (err instanceof ApiError && err.status) throw err; // 服务端给过明确原因（401/403/…）
    throw new ApiError(0, 'network', unreachable(res.origin));
  } finally {
    syncLoginHost();
  }
};

const rawMe = api.me;
api.me = async () => {
  // 还没填地址时不必真发请求：让 main() 直接停在连接页，不要弹出无意义的错误。
  if (!apiBase()) throw new ApiError(401, 'unauthorized', '请先填写服务器地址');
  try {
    return await rawMe();
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      sessionEnded = true;
      throw err;
    }
    if (err instanceof ApiError && err.status) {
      throw new ApiError(err.status, err.code, `${apiBase()} 返回的不是 lanbox 接口（HTTP ${err.status}）`);
    }
    throw new ApiError(0, 'network', unreachable(apiBase()));
  }
};

// 主动退出（账号菜单里的「退出登录」）也要把落盘的令牌清掉
const rawLogout = api.logout;
api.logout = async () => {
  sessionEnded = true;
  return rawLogout();
};

// ---------- 事件绑定 ----------

function bindClientEvents() {
  // 改地址时同步注入点，这样登录表单（web/main.js 绑定的）直接用新地址
  $('server-input')?.addEventListener('input', () => {
    const res = normalizeServer($('server-input').value);
    applyConnection(res.origin || '', '');
    syncLoginHost();
    setAuthStatus('');
  });

  // 徽标：未连接时把焦点送回地址栏；已连接时打开服务器面板（二维码/发现/断开）
  $('conn-state')?.addEventListener('click', () => {
    if ($('app-view').hidden) {
      $('server-input')?.focus();
      return;
    }
    openServerPanel();
  });
}

// ---------- 启动 ----------

async function start() {
  const savedServer = storage.get(SERVER_KEY);
  const savedToken = storage.get(TOKEN_KEY);
  const input = $('server-input');
  if (input) input.value = savedServer;
  applyConnection(savedServer, savedServer ? savedToken : '');
  paintConnState(null);
  syncLoginHost();
  initDiscovery();
  bindClientEvents();
  // main() 会用 GET /api/me 探测会话：令牌有效直接进主界面，否则回到连接页
  await boot();
}

start();
