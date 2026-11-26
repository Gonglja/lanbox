# lanbox 设计文档

日期：2026-11-26
状态：已与用户确认（架构 / 存储 / API / 前端 / 安全 / 测试 六节均确认）

## 1. 目标与场景

一台运行 lanbox 的 Linux 小主机，多个设备都能通过浏览器访问同一个服务：

- 一端发布文字与文件
- 另一端查看并取走

两端**完全对等**：任一方都能发布文本和附件、都能查看全部内容、都能删除。
本质是一个"共享留言板 + 文件中转站"。

设计约束（来自需求澄清）：

| 项 | 结论 |
|---|---|
| 网络拓扑 | 一套服务、一个监听端口，多个地址访问（如 `https://192.168.1.9:8443`） |
| 来源标注 | 每条内容标注**客户端 IP** |
| 认证 | 每个使用者独立账号密码，会话有过期时间 |
| 内容模型 | 一条内容 = 文本和/或附件（附件可多个）；单条时间线 |
| 取走标记 | 每条内容可一键标记"已取走"，可取消 |
| 上传 | 一次性 multipart、服务端流式落盘（不做分片/断点续传） |
| 下载 | 逐文件下载；不做 zip 打包 |
| 大小限制 | 可配置：单文件上限、最小剩余磁盘、文本上限 |
| 删除 | 直接物理删除（无回收站），前端二次确认 |
| 过期 | 不自动过期，只有手动删除 |
| 传输 | HTTPS 单端口；证书可配置，未配置则自动自签 |
| 实时性 | SSE 推送新内容/变更，含断线重连 |
| 技术栈 | Go 1.26，**仅标准库**，单二进制，前端零构建 |

## 2. 技术选型

**选定：Go + 标准库，单静态二进制。**

- `go build` 产出单个可执行文件，拷到小主机即可运行；前端用 `//go:embed` 打进二进制
- 内存约 10–20MB；1GB 上传通过 `io.Copy` 流式写盘，内存占用恒定
- 零外部依赖：`go.sum` 为空，离线可编译（密码哈希用标准库 `crypto/pbkdf2`，TLS 用 `crypto/tls` + `crypto/x509`）
- 备选方案已评估并否决：
  - Node 26 + Fastify + SQLite：需携带 `node_modules`，原生模块换机可能重编译，内存约 80MB+
  - Python 3.14 + FastAPI：需 venv 与依赖，内存约 100MB，同步阻塞模型下流式上传与 SSE 需额外处理

## 3. 整体架构

```
设备 A ──HTTPS──┐
                ├──> lanbox 进程 (:8443)
设备 B ──HTTPS──┘        │
                        └── data/
                            ├── items/<id>/{meta.json,files/…}
                            ├── users.json
                            └── tls/{cert.pem,key.pem}
```

进程内分层，每层单一职责、接口窄、可独立测试：

| 包 | 职责 | 依赖 |
|---|---|---|
| `internal/config` | 读 `config.json` + 环境变量覆盖 + 默认值 | 标准库 |
| `internal/auth` | 用户库、PBKDF2 哈希、会话表、登录限速 | 文件系统 |
| `internal/store` | 内容 CRUD、磁盘守卫、元数据原子持久化 | 文件系统 |
| `internal/httpapi` | 路由、流式上传、SSE 广播、静态资源、中间件 | 上述三者 |
| `internal/tlsutil` | 自签证书生成与复用 | 标准库 |
| `web/` | 单页前端（html/js/css），embed 进二进制 | — |

模块边界规则：

- `store` 不认识 HTTP，只接受 `io.Reader` 并返回领域结构体
- `httpapi` 不直接碰文件系统路径拼装，一律经 `store`
- `auth` 不依赖 `httpapi`，会话校验以 `(token) → (user, ok)` 形式暴露
- 前端不内联业务规则，所有校验以服务端为准

## 4. 存储设计

**一条内容 = 一个目录**，因此"直接删除"就是 `os.RemoveAll`。

```
data/
├── items/
│   └── 1739260800000-a1b2c3d4/
│       ├── meta.json
│       └── files/
│           └── f1__报告.pdf
├── users.json
└── tls/cert.pem, tls/key.pem
```

### 4.1 meta.json

```json
{
  "id": "1739260800000-a1b2c3d4",
  "created_at": "2026-11-26T10:00:00Z",
  "created_by": "alice",
  "source_ip": "203.0.113.7",
  "text": "这份图纸麻烦拷走",
  "taken": false,
  "taken_at": null,
  "taken_by": null,
  "files": [
    {
      "id": "f1",
      "name": "报告.pdf",
      "size": 1234567,
      "sha256": "9f2c…",
      "downloads": 0
    }
  ]
}
```

- `id` = 毫秒时间戳 + 4 字节随机十六进制。目录名字典序即创建时间序，排序无需解析 JSON
- 磁盘文件名 = `<文件ID>__<清洗后的原文件名>`，文件 ID 由服务端生成；**用户提交的名字只用于展示和 `Content-Disposition`，绝不参与路径拼接**（杜绝路径穿越）
- `sha256` 在上传流中同步计算（`io.MultiWriter(file, hasher)`），前端展示前 8 位，下载可校验

### 4.2 原子性

- `meta.json`、`users.json` 一律"写临时文件 → `fsync` → `rename`" 原子替换
- 整条内容为写盘失败时的清理单位：任何一步失败即 `RemoveAll` 该目录，不留半成品

### 4.3 磁盘守卫

上传前用 `statfs` 取剩余空间，若 `剩余 - 本次请求声明的总大小 < min_free_disk_mb` 直接拒绝（`507`）。
写盘途中写满同样返回 `507` 并清理已写入部分。**永不主动删除已有内容腾空间。**

### 4.4 并发与一致性

- `store` 持有内存索引（`id → meta`），`sync.RWMutex` 保护；启动时扫描 `items/` 构建
- 索引初始化时遇到损坏的 `meta.json`：记 warn 日志并跳过该条，不影响启动
- 列表查询基于索引 + 游标，无需每次读盘

## 5. 配置

`config.json`，所有字段可选，缺省即默认；环境变量 `LANBOX_<大写字段>` 覆盖（如 `LANBOX_LISTEN`）。

```json
{
  "listen": ":8443",
  "data_dir": "./data",
  "tls": { "cert_file": "", "key_file": "" },
  "max_file_size_mb": 1024,
  "min_free_disk_mb": 1024,
  "max_text_bytes": 65536,
  "session_ttl_hours": 168
}
```

| 字段 | 默认 | 说明 |
|---|---|---|
| `listen` | `:8443` | 监听地址，可写 `0.0.0.0:8443` 或 `192.168.1.9:8443` |
| `data_dir` | `./data` | 数据根目录，需可写 |
| `tls.cert_file` / `key_file` | 空 | 填写后使用你的证书；留空则在 `data/tls/` 自动生成自签证书 |
| `max_file_size_mb` | 1024 | 单文件上限，超出返回 `413` |
| `min_free_disk_mb` | 1024 | 剩余磁盘下限保护 |
| `max_text_bytes` | 65536 | 单条文本上限 |
| `session_ttl_hours` | 168 | 会话有效期（7 天） |

## 6. HTTP API

除上传（multipart）、下载（二进制流）、SSE（`text/event-stream`）外，请求与响应均为 JSON。

### 6.1 认证

```
POST   /api/login                {username,password} → 200 + Set-Cookie: sid
POST   /api/logout               → 204
GET    /api/me                   → {username,is_admin} | 401
POST   /api/password             {old,new} → 204
```

### 6.2 内容

```
GET    /api/items?limit=50&before=<id>&q=关键词&filter=all|todo
POST   /api/items                multipart: 字段 text + files[]（可多个）
GET    /api/items/<id>
PATCH  /api/items/<id>           {taken:true|false}
DELETE /api/items/<id>           → 204（直接删除目录）
DELETE /api/items/<id>/files/<fid> → 204（删单个附件）
```

- 列表按 `id` 倒序（最新在前）；`before` 为游标，返回 `{items:[…], next_cursor:"…"|null}`
- `q` 对文本与附件文件名做子串匹配（扫内存索引，不建索引结构）
- `filter=todo` 仅返回 `taken=false`

### 6.3 下载

```
GET /api/items/<id>/files/<fid>
```

- 响应 `Content-Disposition: attachment`（RFC 5987 `filename*=UTF-8''…` 支持中文名）
- 响应 `Content-Type: application/octet-stream` + `X-Content-Type-Options: nosniff`
- 使用 `http.ServeContent` → 免费获得 `Range` 支持与断点续传
- 每次成功下载 `downloads` 计数 +1

### 6.4 实时

```
GET /api/events      text/event-stream
```

- 事件：`item.created`（载荷为完整 item）、`item.updated`（完整 item）、`item.deleted`（`{id}`）
- 每 25 秒发送 `: heartbeat` 注释保活，防止中间设备断开空闲连接
- 客户端断线由浏览器 `EventSource` 自动重连；重连成功后前端拉一次全量列表兜底
- 服务端维护订阅者集合，广播时慢消费者直接断开（避免 goroutine 泄漏）

### 6.5 用户管理（admin）

```
GET    /api/users
POST   /api/users                     {username,password,is_admin}
POST   /api/users/<name>/password     {password}
DELETE /api/users/<name>
```

### 6.6 错误格式

```json
{ "error": { "code": "file_too_large", "message": "文件超过 1024MB 上限" } }
```

| 状态码 | 场景 |
|---|---|
| `400` | 参数错误 |
| `401` | 未登录 / 会话过期 |
| `403` | 无权限、CSRF 校验失败、登录锁定 |
| `404` | 内容或文件不存在 |
| `413` | 文件超上限 |
| `507` | 磁盘空间不足 |

所有消息为中文，可直接展示给用户。

## 7. 前端

单页应用，原生 JS + 单个 CSS 文件，**无框架、无 npm、无构建步骤**。

结构：

- 登录页 → 主页面（同一 HTML，按会话状态切换）
- 顶部栏：当前用户名、登出、管理员额外有"用户管理"抽屉
- 输入区：多行文本框 + 「选择文件」（多选）+ 拖拽投放 + `Ctrl+V` 粘贴图片；`XMLHttpRequest` 上传以获取真实进度条（`fetch` 无上传进度）
- 列表区：卡片式，最新在上
  - 卡片显示 `用户名 · 来源IP · 时间`
  - 正文文本（保留换行）
  - 附件行：文件名、大小、sha256 前 8 位、「下载」
  - 操作：「已取走 / 取消取走」、「删除」（二次确认）
- 已取走的卡片灰化 + 半透明
- 顶部筛选：`全部 / 未取走`；搜索框（走服务端 `q`）
- 列表底部「加载更多」按钮（游标分页，一次 50 条）
- SSE 连接状态指示（已连接 / 重连中）
- 中文界面；桌面优先，窄屏可用（手机可作第二屏）

## 8. 安全

- TLS 最低 1.2（优先 1.3）。自签证书有效期 10 年，SAN 含 `localhost`、`127.0.0.1`、`listen` 主机名及本机各网卡 IP；仅当 `data/tls/` 下缺失时才生成，绝不覆盖用户配置的证书
- 会话 Cookie：`sid`，`HttpOnly` + `Secure` + `SameSite=Lax` + `Path=/`；有效期采用**滑动过期**——每次通过校验的请求把过期时间刷新为 `now + session_ttl_hours`
- CSRF：所有 `POST/PATCH/DELETE` 校验 `Origin`/`Referer` 与 `Host` 同源，不同源返回 `403`
- 登录限速：同一 IP 15 分钟内失败 5 次 → 锁定 10 分钟（内存计数）
- 密码：最少 8 位；PBKDF2-HMAC-SHA256，16 字节随机盐，600k 轮；比对用 `crypto/subtle.ConstantTimeCompare`
- 用户管理护栏：不能删除自己；不能删除最后一个管理员
- 响应头：`Content-Security-Policy: default-src 'self'`、`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、`Referrer-Policy: no-referrer`
- 下载强制 `attachment` + `octet-stream`，防止上传 HTML/SVG 造成存储型 XSS
- 上传路径不使用任何用户可控字符串；请求体上限由 `http.MaxBytesReader` 兜底
- 访问日志写 stdout：方法、路径、状态、客户端 IP、耗时；不记录密码与会话令牌

## 9. 错误处理与健壮性

- 上传失败（断网、超限、磁盘满）→ 清理该条临时目录，返回对应错误
- 全局 panic 恢复中间件 → `500` + 栈写日志，进程存活
- `meta.json` 损坏 → 启动时 warn 并跳过，不阻塞服务
- `SIGTERM` 优雅退出：停止接受新连接 → 关闭全部 SSE 连接 → 退出
- 单体进程无外部依赖，重启即恢复（会话在内存，重启后需重新登录）

## 10. 测试策略

`go test ./...`，仅用标准库 `testing` + `httptest`。

单元测试：

- config：默认值、文件覆盖、环境变量覆盖、非法值报错
- auth：哈希/校验往返、错误密码、会话创建与过期、登录限速计数
- store：ID 时间序、游标分页、文件名清洗、磁盘守卫（注入假容量函数）、原子写入、损坏 `meta.json` 跳过
- tlsutil：证书可解析、SAN 正确、已存在证书不被覆盖

集成测试（`httptest` 真服务）：

登录 → 上传文本 + 文件 → 列表可见 → 下载并校验 sha256 一致 → 标记已取走 → 收到 SSE `item.updated` → 删除 → 404。

前端不做浏览器 E2E（不引入额外依赖），改为 README 中的手动验收清单。

## 11. 部署与运维

- 编译：`CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o lanbox ./cmd/lanbox`（静态二进制）
- 附 `lanbox.service` systemd 模板：专用低权限用户、`Restart=always`、日志进 journald
- 首次启动随机生成 `admin` 密码并打印到 stdout 一次；提供 `lanbox -reset-admin-password` 重置并打印
- README：快速开始、配置项表、防火墙提示、自签证书在浏览器/系统里的信任步骤、常见问题

规模预估：约 1500 行 Go（8 个文件）+ 约 800 行前端。

## 12. 明确不做（YAGNI）

- 分片上传与断点续传
- 多文件打包 zip 下载
- 回收站 / 软删除 / 自动过期清理
- 频道、标签、置顶、全文索引
- 外部数据库、ORM、前端框架、构建工具链
- 富文本/Markdown 渲染（纯文本，保留换行）
- 多语言（仅中文）
