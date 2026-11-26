# lanbox 连接二维码与局域网发现实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** 服务端给出连接二维码，纯 web 客户端支持扫码添加、`.local` 探测与服务端代劳的 mDNS 浏览。

**Architecture:** 新增零依赖 `internal/qr`（字节模式 + 纠错 M + v1–10，SVG 输出）；`httpapi` 新增 `/api/connect`、`/api/connect/qr.svg`、`/api/discover`（后者由 `main` 注入 `internal/mdns.Browse`）；服务端状态页与客户端共用 `web/connect.js` 渲染二维码；客户端拆出 `state.js`（地址/存储）与 `discovery.js`（服务器簿、探测、扫码、面板）。

**规格来源：** `docs/superpowers/specs/2026-11-26-connect-qr-discovery-design.md`

---

## Task 1: QR 编码器

- [x] `internal/qr/qr.go`：字节模式、纠错等级 M、版本 1–10、8 掩码 + 罚分选优、格式/版本信息、SVG 输出
- [x] 交叉参照数据：`testdata/golden.json`（python-qrcode，强制掩码）、`testdata/golden_auto.json`（qrcodegen，自动掩码）
- [x] 测试：逐模块比对、SVG ↔ 矩阵往返、超长内容、非法掩码；`testdata/README.md` 记录重新生成方法

## Task 2: 服务端接口

- [x] `internal/httpapi/connect.go`：`/api/connect`（地址排序去重）、`/api/connect/qr.svg`（白名单校验 + sandbox CSP）、`/api/discover`（3 秒缓存）
- [x] `Options.Discover` + `server.go` 字段与路由（均需登录）
- [x] `cmd/lanbox/discover.go`：`internal/mdns.Browse` 适配（1.5s 超时、标出本机、按名称排序）
- [x] `internal/httpapi/connect_test.go`：鉴权、地址有序去重、SVG 头/CSP/拒绝列表外地址、缓存只浏览一次、空结果 `[]`、未注入 501

## Task 3: 共享前端

- [x] `web/connect.js`：`qrSrc` / `renderQR`（`<img>` 渲染）/ `fillURLOptions`
- [x] `web/api.js`：`connect()` / `discover()`
- [x] `web/status.js` + `web/index.html` + `web/style.css`：状态页「手机扫码连接」卡片（二维码 + 地址下拉）
- [x] `web/ui.js`：`openPanel()` 自定义面板 + `qr` / `wifi` 图标

## Task 4: 客户端

- [x] `client/state.js`：地址规范化、存储包装、注入点、连接页状态行
- [x] `client/discovery.js`：服务器簿（12 条/最近优先）、`.local` 候选探测（按簿子里的端口）、扫码面板（`BarcodeDetector` + 粘贴回退）、已连接面板（二维码 / `/api/discover` / 断开）
- [x] `client/app.js`：接入上述模块；登录后把当前地址与 mDNS 名记进簿子
- [x] `client/index.html` + `client/client.css`：扫码/发现按钮、服务器簿、面板样式

## Task 5: 文档与验证

- [x] README：新特性、`/api/connect` 与 `/api/discover`、状态页二维码、客户端三种添加方式、验收清单 39–43
- [x] `go vet ./...`、`go test ./...`、`node --check`、`make client`
- [x] 浏览器冒烟（headless Chrome）：状态页二维码截图用 OpenCV 解码回 `https://lanbox.local:8445`；服务器面板「发现其他实例」列出本机；断开/服务器簿/粘贴添加；`.local` 探测命中（用 `--host-resolver-rules` 映射）与失败时的提示
