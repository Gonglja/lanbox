# lanbox Windows 客户端（lanbox-agent）设计

日期：2026-11-26
状态：已与用户逐节确认（架构 / 剪贴板规则 / 界面与安全 / 错误与构建 四节均确认）

## 1. 目标

给 lanbox 服务端配一个 **Windows 桌面客户端**，解决「手工打开浏览器发布」的摩擦：

- 局域网 **mDNS 自动发现**服务端，按 TXT 指纹固定自签证书
- **剪贴板内容自动上传**：文本、资源管理器复制的文件/文件夹、截图（图片）
- **手动上传**：消息文本、文件、文件夹（含拖拽）
- 以上行为**全部可配置**，可随时**暂停**或**关闭**监听

不做：多服务器 profile、内容加密、Windows Toast、自动更新。

## 2. 技术选型

**Wails v2.10.2 + vanilla 前端（无 Node）+ Go 1.25**。

| 备选 | 否决理由 |
|---|---|
| Fyne | 自绘控件、需 CGO，界面与仓库现有 Web 风格割裂，剪贴板仍要自己补 Win32 |
| lxn/walk | 原生但样式老旧、表单代码冗长 |
| Electron/Tauri | 体积/工具链过重，与「单 exe 拷过去就能用」不符 |

- Wails v2 在 Windows 上**无 CGO**，`go build -tags desktop,production` 直接产出 GUI exe（实测 v2.10.2 + Go 1.25 通过，约 10MB）
- 前端是零构建的原生 ES + CSS，可直接搬 `web/style.css` 的主题令牌；`frontend:install`/`frontend:build` 均为空
- 托盘用 `fyne.io/systray`（纯 Win32，+0.4MB）
- 协议层复用 `internal/client`，发现复用 `internal/mdns`，**不新增服务端接口**

## 3. 架构

```
cmd/lanbox-agent/       Wails 入口：单实例、托盘、窗口、依赖装配、绑定方法
internal/agent/         ★ 平台无关核心：Pipeline（决策）+ Manager（队列/重试/历史）
internal/winclip/       ★ Windows 专项：剪贴板监听与读取、CF_DIB→PNG、DPAPI、文件选择器
internal/session/       会话文件（与 lanbox-cli 共用同一份 session.json）
internal/client/        复用：Login/Put/DeleteItem
internal/mdns/          复用：Browse
frontend/dist/          嵌入的 vanilla 前端
```

边界：`internal/agent` 不 import Win32 与 `internal/client`（只吃 interface）；`internal/winclip` 不做业务判断；装配只在 `cmd/lanbox-agent`。

数据流：

```
WM_CLIPBOARDUPDATE（独立 LockOSThread 的 message-only 窗口）
  → winclip.ReadClipboard() → agent.Clip{Kind 优先级: Paths > PNG > Text}
  → agent.Pipeline.Decide() → 9 条规则短路 → agent.Job
  → agent.Manager 单 worker：client.Put（带进度）→ 历史 / 退避重试
  → Wails EventsEmit("state"/"history"/"progress") → 窗口；systray 同步状态灯
```

## 4. 剪贴板决策规则（Pipeline，按序短路）

| # | 规则 | 结果 |
|---|---|---|
| 1 | 总开关关 / 已暂停 / 未登录 | 不注册监听，完全不读剪贴板 |
| 2 | 剪贴板含 `Clipboard Viewer Ignore` 格式 | 跳过（密码管理器豁免格式） |
| 3 | 300ms 去抖（连续多次 `WM_CLIPBOARDUPDATE`） | 合并为一次 |
| 4 | 同时有 `CF_HDROP` 与文本 | 按文件处理，文本丢弃 |
| 5 | `sha256(内容)` 命中去重窗口 | 跳过 |
| 6 | 文本 trim 后为空 / 超 `max_text_bytes` | 跳过 |
| 7 | 文件不存在、文件夹递归（不跟随符号链接）、超文件数/总量 | 整条跳过并写明原因 |
| 8 | 图片 PNG 超 `max_image_mb` | 跳过 |
| 9 | 上传失败 | 重试 3 次（2s/10s/30s）；401 停手并可自动重登；413/507 不重试 |

## 5. 配置（`%AppData%\lanbox\agent.json`，DPAPI 保护密码）

| 键 | 默认 | 说明 |
|---|---|---|
| `clipboard.enabled` | `true` | 总开关 |
| `clipboard.paused` | `false` | 暂停态（持久化，重启保持） |
| `clipboard.text` | `true` | 文本自动上传 |
| `clipboard.files` | `true` | 文件/文件夹自动上传 |
| `clipboard.images` | `false` | 截图自动上传 |
| `clipboard.dedupe_seconds` | `30` | 0 = 不去重 |
| `clipboard.max_text_bytes` | `1048576` | 文本阈值 |
| `clipboard.max_image_mb` | `5` | 图片阈值 |
| `clipboard.max_total_mb` | `2048` | 单条附件总量 |
| `clipboard.max_files` | `500` | 单条附件数量 |
| `clipboard.text_prefix` | 空 | 自动上传附加说明（默认留空，不污染正文） |
| `app.autostart` | `false` | `HKCU\...\Run` |
| `app.close_to_tray` | `true` | 关闭窗口 → 隐藏到托盘 |
| `app.history_size` | `50` | 内存保留本机记录数 |
| `app.remember_password` | `true` | DPAPI 保存密码，SID 过期自动重登 |

## 6. 界面

单窗口：顶部状态条（状态灯 / 服务器 / 账号 / 暂停 / 设置入口）+「记录」「设置」两页。

- 记录页：发送消息框、选择文件、选择文件夹、拖拽落区；本机上传历史（类型、时间、大小、进度、状态、查看/撤回/重试/取消）
- 设置页：服务器（显示与「更改」）、剪贴板四类开关、阈值、去重窗口、前缀、开机自启、关闭到托盘、记住密码、打开日志
- 登录页：mDNS 发现列表（实例 / 地址 / 指纹前 16 位）+ 刷新 + 手填地址；选中 mDNS 项时按 TXT `fp` 固定证书
- 托盘：状态图标（绿/黄/红）+ 打开窗口 / 暂停·恢复 / 立即上传剪贴板 / 退出；tooltip 显示最近错误

## 7. 安全

- 复用 `%AppData%\lanbox\session.json`（与 CLI 双向兼容，含 `pin_fp`），登录后写回
- 密码用 DPAPI（`CryptProtectData`，当前用户）加密后 base64 存 `agent.json`，可关闭
- 证书：有 `pin_fp` 时严格比对叶证书 SHA-256，不符直接拒绝发送
- 单实例：`CreateMutexW`，重复启动静默退出并写日志
- 日志：`%AppData%\lanbox\agent.log`（2MB × 2 轮转），记录每次跳过原因

## 8. 错误处理

| 情况 | 行为 |
|---|---|
| 网络错误 | 退避重试 3 次，状态灯黄 |
| 401/403 | 有已存密码则自动重登一次，否则红灯 + 提示重新登录 |
| 413 `item_too_large`/`file_too_large` | 不重试，记录条目标红并显示服务端 message |
| 507 `no_space` | 不重试，红灯 + tooltip |
| 证书不受信任且无 pin | 提示重新从 mDNS 选择以固定指纹 |
| 证书指纹不符 | 拒绝发送 |
| 文件夹内个别文件权限不足 | 跳过并计数，提示「N 个文件被跳过」 |

## 9. 测试

- `internal/agent`：表驱动覆盖 9 条规则、去重窗口、优先级、阈值边界、401 停手、退避序列、暂停时不读
- `internal/winclip/dib`：`decodeDIB` 纯函数测 24/32 位、自底向上、行填充、alpha 全 0 视为不透明
- `internal/session`：临时目录往返、原子写、缺文件返回 nil
- `go vet ./...` + `go test ./...` 保持全绿；CLI 现有测试因 session 抽取需继续通过

## 10. 构建

- `make agent`：本机 Windows 构建 `lanbox-agent.exe`
- `make dist`：追加 `dist/lanbox-agent-windows-{amd64,arm64}.exe` 并进 SHA256SUMS
- `go build -tags desktop,production -ldflags "-s -w -H=windowsgui -X main.version=..."`，WebView2 用 `wv2runtime.embed`（`-webview2 embed`）内置引导器
- 依赖走 `GOPROXY=https://goproxy.cn,direct`（本机 proxy.golang.org 不通）
