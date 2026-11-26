# lanbox 纯 web 客户端实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** 新增可异地托管的纯 web 客户端（`client/`，可打成单文件 `dist/lanbox-client.html`），让任意浏览器填地址+账号即可与现有 lanbox 服务端收发内容，视觉与服务端 Web UI 完全一致。

**Architecture:** 服务端为 `/api/*` 开放无凭据 CORS，`withAuth` 增加 Bearer 与 `?token=` 两种凭据，跨域登录时在响应体下发令牌。前端把 4 处硬编码的同源 URL 收敛到 `web/env.js`，客户端仅做「连接层」薄层并动态导入 `web/main.js` 复用整套 UI。`tools/buildclient` 把 CSS/经典脚本内联、ES 模块按拓扑序转成 blob URL，产出单文件 HTML。

**Tech Stack:** Go 1.25（仅标准库）、原生 ES 模块、`//go:embed`。

**规格来源：** `docs/superpowers/specs/2026-11-26-web-client-design.md`

---

## 文件结构

| 文件 | 责任 |
|---|---|
| `internal/httpapi/cors.go` | 新增：`/api/*` 的 CORS 与预检 |
| `internal/httpapi/middleware.go` | 修改：`chain` 接入 CORS；`withAuth` 三凭据；`csrf` 放行 Bearer |
| `internal/httpapi/auth_handlers.go` | 修改：跨域登录响应带 `token`/`expires_at` |
| `internal/httpapi/cors_bearer_test.go` | 新增：服务端测试 |
| `web/env.js` | 新增：base/token 注入点 |
| `web/api.js`、`sse.js`、`media.js`、`render.js` | 修改：改走 `env.js` |
| `web/main.js` | 修改：导出 `hooks` |
| `client/index.html` | 新增：连接页 + app 骨架 |
| `client/app.js` | 新增：连接层 |
| `client/client.css` | 新增：客户端专属样式 |
| `tools/buildclient/main.go` + `main_test.go` | 新增：单文件打包器 |
| `Makefile` | 修改：`client` 目标与 help |
| `README.md` | 修改：纯 web 客户端章节 |

**边界规则：** 不放宽服务端 CSP；`web/` 仍是零构建、零依赖；服务端同源 UI 行为与响应体不得变化；客户端不得写 `innerHTML`。

---

## Task 1: `web/env.js` 与共享模块改造

- [ ] 新增 `web/env.js`（`apiBase`/`apiToken`/`setToken`/`apiURL`/`authedURL`）
- [ ] `api.js`：`request`/`uploadItem`/`healthz` 走 `apiURL`，条件加 `Authorization` 与 `credentials`
- [ ] `api.js`：`login` 收到 `token` 时 `setToken`
- [ ] `sse.js`：`apiURL('/api/events')` + `?token=`
- [ ] `media.js`：`mediaSrc`/`downloadSrc`/`zipSrc` 用 `authedURL`
- [ ] `render.js`：文件与 zip 链接改用 `downloadSrc`/`zipSrc`
- [ ] `main.js`：`export const hooks = { showLogin: [], showApp: [] }` 并在两处末尾调用

**验收：** 服务端 Web UI 无任何可见变化（无全局量时 `apiURL(x) === x`）。

## Task 2: 服务端 CORS 与令牌

- [ ] `cors.go`：`/api/*` 加 CORS 头；`OPTIONS` 204；不设 `Allow-Credentials`
- [ ] `middleware.go`：`chain` 接入；`withAuth` 支持 Bearer / cookie / `?token=`（仅 GET/HEAD）
- [ ] `middleware.go`：`csrf` 见 `Authorization` 直接放行
- [ ] `auth_handlers.go`：跨域登录返回 `token` + `expires_at`

## Task 3: 服务端测试

- [ ] `cors_bearer_test.go`：预检、Bearer 全流程、query token、CSRF 放行、跨域/同源登录差异、登出失效

## Task 4: 客户端

- [ ] `client/index.html`：连接页 + 骨架（复用 `web/index.html`，加服务器地址栏与「已连接」徽标）
- [ ] `client/app.js`：地址规范化、存储、动态导入 `main.js`、hooks 挂钩、错误翻译、换服务器
- [ ] `client/client.css`：地址栏、徽标、隐藏管理/状态入口

## Task 5: 单文件打包

- [ ] `tools/buildclient/main.go`：内联 CSS/经典脚本/favicon，模块拓扑排序 → blob URL
- [ ] `tools/buildclient/main_test.go`：拓扑序、无残留说明符、`</script>` 转义、缺文件报错
- [ ] `Makefile`：`client` 目标

## Task 6: 文档与验证

- [ ] `README.md`：纯 web 客户端使用、单文件分发、跨域与证书说明、安全边界
- [ ] `make check`、`node --check web/*.js client/*.js`、真服务器冒烟
