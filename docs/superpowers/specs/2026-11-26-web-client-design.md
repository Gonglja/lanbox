# lanbox 纯 web 客户端设计

日期：2026-11-26
状态：已确认，待实现
范围：新增 `client/`（可异地托管的静态客户端）与 `tools/buildclient`（单文件打包）；服务端新增 CORS 与 Bearer 令牌通道；`web/*.js` 抽出可注入的 base/token。不改存储格式、不改下发内容、不放宽同源 Web UI 的 CSP、不引入第三方依赖、不引入前端构建步骤。

## 1. 目标

1. **零安装**：任意设备的任意浏览器打开一个静态页面，填「服务器地址 + 账号密码」即可与现有 lanbox 服务端收发内容。
2. **可分发为单文件**：`make client` 产出 `dist/lanbox-client.html`，双击（`file://`）或托管于任意静态站点都能用。
3. **视觉与交互与服务端 Web UI 完全一致**：复用同一份 `style.css`、图标集、Markdown 渲染、媒体预览与主题系统。
4. **不削弱现有安全模型**：同源 Web UI 仍走 `HttpOnly; Secure; SameSite=Lax` 的 `sid` cookie；跨域页面拿不到该 cookie，能力完全由显式携带的 Bearer 令牌决定。

## 2. 现有约束与关键发现

| 约束/事实 | 位置 | 影响 |
|---|---|---|
| 会话只认 `sid` cookie，且 `SameSite=Lax`、`HttpOnly`、`Secure` | `internal/httpapi/middleware.go` | 跨站页面无法携带、也无法读取 → 必须新增 Bearer 通道 |
| 全局 CSRF 中间件要求不安全方法的 `Origin`/`Referer` 与 `Host` 同源 | `internal/httpapi/middleware.go` | 跨域客户端的 `POST/PATCH/DELETE` 会被 403，必须为令牌请求放行 |
| `<img>`/`<video>`/`<a download>`/`EventSource` 无法设置请求头 | 浏览器行为 | 预览、下载、SSE 必须支持 `?token=` |
| 访问日志只记 `r.URL.Path`（不记 query） | `internal/httpapi/middleware.go` | 令牌放进查询串不会落盘 |
| 前端是原生 ES 模块 + 相对路径 `/api/...` | `web/api.js`、`sse.js`、`media.js`、`render.js` | 抽 4 处硬编码即可复用，不改渲染逻辑 |
| 服务端 CSP 为 `default-src 'self'` | `internal/httpapi/middleware.go` | 服务端页面不得内联脚本；客户端页面自带 CSP，不受此限 |
| 服务端 Web UI 的 `main.js` 在导入时即 `main()` | `web/main.js` | 客户端可以用动态 `import('../web/main.js')` 复用整份 UI，但必须先设好 base/token |
| 自签证书默认开启 | `internal/tlsutil` | 跨域请求会因证书不受信失败；只能提示用户先访问一次服务器并信任 |

## 3. 选型对比

### 3.1 客户端形态

| 方案 | 评价 |
|---|---|
| **A（选定）独立静态页面 + 单文件打包** | 零安装、可托管、可 U 盘分发；代价是服务端要加 CORS 与 Bearer |
| B 由服务端托管（`/client`） | 后端零改动，但必须先连上某个 lanbox 才能打开，本质是现有 UI 换皮 |
| C 把 Windows agent 前端做成 PWA | 仍绑定当前域名，且剪贴板上传在浏览器里受限 |

### 3.2 跨域认证

| 方案 | 评价 |
|---|---|
| **A（选定）`Authorization: Bearer`，仅跨域登录响应下发令牌** | 不引入 cookie 新语义；同源响应零变化；令牌可由用户清除 |
| B `SameSite=None` 的 cookie + `Allow-Credentials` | 需要把 `SameSite` 放宽到 `None`，等于对**所有**站点放开会话 cookie，风险明显更大 |
| C 独立的 API Key/长期令牌 | 多一套凭据管理，与管理页面、CLI 会话都要对齐，超出需求 |

### 3.3 UI 复用方式

| 方案 | 评价 |
|---|---|
| **A（选定）客户端薄层 + 动态 `import('../web/main.js')`** | 一份 UI 代码；差异只在连接层（约 150 行）；客户端不需要管理/状态面板时用 CSS 隐藏 |
| B 复制 `main.js`/`render.js` 到 `client/` 并裁剪 | 需要长期同步两份几百行的逻辑，改动必然分叉 |
| C 客户端重写一套精简 UI | 同样分叉，且视觉难以保持一致 |

### 3.4 单文件打包

| 方案 | 评价 |
|---|---|
| **A（选定）Go 小程序：内联 CSS/经典脚本，模块按拓扑序转 blob URL** | 不改写模块语义（正则只替换 `from '...'` 里的说明符），`file://` 也能跑 |
| B 手写一份经典脚本大文件 | 与模块版分叉，无法复用 |
| C 引入 esbuild/rollup | 破坏「前端零构建、零依赖」 |

## 4. 设计

### 4.1 服务端：令牌通道

1. `internal/httpapi/cors.go`（新）：仅对 `/api/*` 回
   `Access-Control-Allow-Origin: *`、`Allow-Methods`、`Allow-Headers: Authorization, Content-Type`、
   `Expose-Headers: Content-Disposition`、`Max-Age: 600`；`OPTIONS` 直接 204。
   **不设 `Allow-Credentials`** —— 跨站页面永远拿不到 `sid`。
2. `withAuth` 依次尝试：
   1. `Authorization: Bearer <token>`
   2. `sid` cookie（命中时按原样滑动续期）
   3. `?token=`（仅 `GET`/`HEAD`，供媒体/下载/SSE）
   都不命中回 401。
3. `csrf` 中间件：带 `Authorization` 的请求跳过 `Origin` 同源校验（令牌本身即防伪造）。
4. `POST /api/login`：仅当请求来自跨域（`Origin` 存在且 Host 不同）时，响应体额外带
   `token` 与 `expires_at`；同源响应保持原样（`{username, is_admin}`）。

### 4.2 前端：可注入的 base/token

- `web/env.js`（新）：`apiBase()`、`apiToken()`、`setToken()`、`apiURL(path)`、`authedURL(path)`。
  两个全局量 `window.__LANBOX_BASE__` / `__LANBOX_TOKEN__` 为空时所有函数退化为原行为
  （相对路径、cookie 认证）——**服务端 Web UI 行为零变化**。
- `web/api.js`：`request()`/`uploadItem()`/`healthz` 走 `apiURL`，有令牌时加 `Authorization`
  并把 `credentials` 设为 `omit`；`login` 收到 `token` 时写入 `setToken()`。
- `web/sse.js`：URL 走 `apiURL('/api/events')`，有令牌时带 `?token=` 且 `withCredentials=false`。
- `web/media.js`：`mediaSrc`/`downloadSrc`/新增 `zipSrc` 用 `authedURL`（自动附 `?token=`）。
- `web/render.js`：文件下载链接与 zip 链接改用 `downloadSrc`/`zipSrc`。
- `web/main.js`：导出 `hooks = { showLogin: [], showApp: [] }`，在两处末尾依次调用，
  供客户端薄层挂钩（保存令牌、维护「已连接」徽标、聚焦服务器输入框）。CSP 不受影响。

### 4.3 客户端

- `client/index.html`：连接页（品牌 + 服务器地址 + 用户名 + 密码 + 记住用户名 + 证书提示）
  + 与服务端一致的 app 骨架（原样保留 `admin-drawer`/`status-view` 等节点，供共享脚本绑定，
  由 `client.css` 隐藏）；`<script type="module" src="./app.js">`。
- `client/app.js`：连接层
  - 服务器地址规范化（缺协议补 `https://`、去掉尾部 `/`、允许 `host:port`）
  - `localStorage`：`lanbox.client.server` / `.token` / `.username`
  - 启动：写入 `__LANBOX_BASE__`/`__LANBOX_TOKEN__` → 动态导入 `../web/main.js`（其 `main()`
    会用 `GET /api/me` 探测会话）
  - `hooks.showApp`：持久化令牌、显示「已连接 host · 用户名」、隐藏管理/状态入口
  - `hooks.showLogin`：清空内存与存储中的令牌、保留地址与用户名、聚焦服务器输入框
  - 「换服务器」按钮：清会话后回到连接页并聚焦地址栏
  - 登录失败/网络失败：把 `TypeError`（证书或跨域）翻译成可操作的提示
    （「先在浏览器直接打开该地址并信任证书」）
- `client/client.css`：连接页地址栏、`已连接` 徽标、隐藏管理抽屉与状态页入口。
- 主题继续复用 `lanbox.theme` 与 `web/theme-boot.js`（首帧前设主题，无闪烁）。

### 4.4 单文件打包

`tools/buildclient/main.go`（`go run ./tools/buildclient -root . -out dist/lanbox-client.html`）：

1. 读 `client/index.html`；把 `<link rel=stylesheet>` 内联为 `<style>`、`<script src>`（经典）
   内联为 `<script>`、favicon 内联为 `data:` URL。
2. 从 `<script type="module" src>` 起点遍历模块图（跟随 `from '…'`、`import '…'`、
   动态 `import('…')`），按依赖优先拓扑排序，环形依赖报错。
3. 每个模块的说明符替换为 `__MOD<i>__` 占位符；运行时按顺序用已生成的 blob URL 还原，
   最后一个模块即入口，`import(urls[last])`。
4. 所有内联文本做 `</script` / `</style` 转义，避免提前闭合。
5. `client/index.html` 里指向 `../web/...` 的相对路径同时支持开发期（仓库根目录起
   `python -m http.server` 打开 `/client/`）与打包（构建时归一化解析）。

## 5. 安全分析

| 场景 | 结论 |
|---|---|
| 恶意站点向用户局域网 lanbox 发请求 | 无令牌一律 401；登录受 `Limiter` 按 IP 限速 |
| 恶意站点读取响应 | 只有 `healthz` 与 401 错误体；无 cookie 可用（未设 `Allow-Credentials`） |
| 令牌泄漏 | 仅存于 `localStorage`（客户端自有源）与媒体/SSE 的 URL；`Referrer-Policy: no-referrer` 已生效，访问日志不记 query；退出登录即清除 |
| 令牌被当作 CSRF 跳板 | 令牌必须显式携带，第三方页面拿不到；cookie 路径的 CSRF 校验保持原样 |
| 引入 CORS 后同源 Web UI 变弱 | 不变：同源仍用 `sid`，登录响应体不给令牌 |

## 6. 测试策略

- 服务端（`internal/httpapi/cors_bearer_test.go`）：预检头、Bearer 访问全端点、`?token=` 仅
  GET/HEAD、Bearer 跳过 CSRF、跨域登录返回 `token` 而**同源登录不返回**、错误令牌 401、
  退出登录后令牌失效。
- 打包器（`tools/buildclient/main_test.go`）：拓扑序、无残留相对说明符、`</script>` 转义、
  入口为最后一个模块、缺文件报错。
- 前端：`node --check` 全量语法校验；起真实服务端 + 静态服务器跑一遍手工冒烟
  （登录、发布、预览、下载、SSE、主题、退出）。
