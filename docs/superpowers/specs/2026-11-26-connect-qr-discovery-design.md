# lanbox 连接二维码与局域网发现设计

日期：2026-11-26
状态：已实现
范围：服务端新增 `internal/qr`（零依赖二维码编码）、`GET /api/connect`、`GET /api/connect/qr.svg`、`GET /api/discover`；服务端状态页新增「手机扫码连接」卡片；纯 web 客户端新增服务器簿、`.local` 候选探测、扫码添加与「发现其他实例」。不改存储格式、不引入第三方依赖、不放宽 CSP。

## 1. 目标

1. **手机不用手输地址**：服务端给出二维码，扫码即得 `https://host[:port]`。
2. **客户端能「发现」**：首次靠 `.local` 候选探测；已连上一台后可列出全网实例。
3. **零依赖、零构建**：二维码编码自写（`internal/qr`），SVG 输出，不用 PNG/第三方库。

## 2. 浏览器能做什么、不能做什么

| 能力 | 结论 |
|---|---|
| 原始 mDNS 组播（UDP 5353）/ DNS-SD 浏览 | **做不到**，只能靠系统解析器解析 `<host>.local`（macOS / iOS / Android / Linux 原生支持；Windows 视版本） |
| 枚举局域网 IP 扫描 | 做不到，也不该做 |
| 摄像头 + 二维码识别 | `BarcodeDetector`（Android Chrome、ChromeOS 等）；桌面 Chrome 与 iOS Safari 没有 → 退回「粘贴地址」 |
| 记住扫到的地址 | `localStorage`（`file://` 与隐私模式下可能只在本会话生效） |

因此「发现」分两层：**本地 `.local` 探测**（零依赖、任何浏览器都行）+ **`/api/discover` 真浏览**（服务端有完整 mDNS 能力，但需先连上一台）。

## 3. 服务端设计

### 3.1 `internal/qr`

自写编码器：字节模式、纠错等级 M、版本 1–10（最多 213 字节，够放地址）、8 种掩码 + 标准罚分选优、格式信息与（版本 ≥7 的）版本信息。输出 SVG（1 模块 = 1 单位 + `shape-rendering="crispEdges"`）。

正确性用两份交叉参照数据保证（`testdata/`）：

| 文件 | 来源 | 覆盖 |
|---|---|---|
| `golden.json` | Python `qrcode`，强制掩码 0–7 | 逐模块比对数据段、RS 分块与交织、掩码、格式/版本信息（v2 与 v8） |
| `golden_auto.json` | Nayuki `qrcodegen`（规范级罚分实现），自动掩码 | 校验罚分选优结果（版本 + 掩码 + 矩阵全部一致） |

### 3.2 接口

- `GET /api/connect`（需登录）→ `{name, version, urls:[{url,label,kind}], preferred}`
  地址顺序：mDNS 名 → 当前访问地址（非回环）→ 各网卡地址 → 当前访问地址（回环，标注「仅本机」）。
- `GET /api/connect/qr.svg?url=…`（需登录）→ SVG。**只接受上一步列表里出现过的地址**，
  避免被当成任意内容的二维码生成器；响应带 `Content-Security-Policy: … sandbox`。
- `GET /api/discover`（需登录）→ `{servers:[{name,host,port,urls,proto,fingerprint,current}]}`。
  由 `main` 注入 `DiscoverFunc`（内部调 `internal/mdns.Browse`，1.5s 超时），结果缓存 3 秒，
  避免连点按钮每次重扫。`httpapi` 依旧不依赖 `internal/mdns`。

## 4. 前端设计

- `web/connect.js`（共享）：`qrSrc()`/`renderQR()`/`fillURLOptions()`。
  二维码用 `<img>` 渲染（图片上下文里 SVG 不执行脚本）。
- 服务端状态页新增「手机扫码连接」卡片：二维码 + 地址下拉（多网卡时切换）+ 提示。
- 纯 web 客户端：
  - `client/state.js`：地址规范化、`localStorage` 包装、注入点设置。
  - `client/discovery.js`：服务器簿（`lanbox.client.servers`，最多 12 条、按最近使用排序）、
    `.local` 候选探测（`lanbox.local` + 簿子里出现过的 `.local` 名，443/8443 两个端口）、
    扫码面板（`BarcodeDetector` + `getUserMedia`，不支持时粘贴地址）、
    已连接时的服务器面板（当前地址二维码 / `/api/discover` 列表 / 断开）。
  - `web/ui.js` 新增 `openPanel()`（自定义内容对话框，ESC / 遮罩 / 关闭按钮）与 `qr`、`wifi` 图标。

## 5. 失败与边界

| 情况 | 表现 |
|---|---|
| 自签证书未信任 | 探测与 `healthz` 一律失败；UI 提示先在新标签打开地址并「继续访问」 |
| 改过 `mdns.hostname` | `.local` 探测找不到；提示手填地址，或连上一台后用 `/api/discover` |
| 桌面 Chrome / iOS Safari 无 `BarcodeDetector` | 扫码面板直接给「粘贴地址」输入框 |
| 服务端没有可用地址（只监听回环） | `qr.svg` 返回 404 `no_url`，卡片隐藏 |
| `/api/discover` 未注入（旧构建/测试） | 501 `discover_unavailable` |
| 令牌失效 | 与其它接口一致：401，客户端回到连接页 |

## 6. 测试

- `internal/qr`：两份 golden 数据、SVG ↔ 矩阵往返、超长内容、非法掩码。
- `internal/httpapi/connect_test.go`：`/api/connect` 需登录且地址有序去重、`qr.svg` 内容类型/CSP/拒绝列表外地址、`/api/discover` 需登录、缓存（3 秒内只浏览一次）、空结果是 `[]`、未注入时 501。
- 浏览器冒烟（headless Chrome）：状态页二维码渲染、客户端登录/发布/SSE/断开、服务器簿持久化、扫码面板在不支持时退回粘贴。
