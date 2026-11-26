# lanbox Windows 客户端（lanbox-agent）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 交付 `lanbox-agent.exe`：Wails 窗口 + 托盘常驻，mDNS 发现并固定证书，剪贴板文本/文件/图片按配置自动上传，支持手动发消息/文件/文件夹，可暂停/关闭。

**Architecture:** 平台无关的 `internal/agent`（决策 Pipeline + 上传 Manager）与 Windows 专项的 `internal/winclip`（剪贴板/DPAPI/对话框）分离，只在 `cmd/lanbox-agent` 装配；协议复用 `internal/client`，发现复用 `internal/mdns`，会话抽到 `internal/session` 与 CLI 共用。

**Tech Stack:** Go 1.25、Wails v2.10.2（无 CGO、vanilla 前端无 Node）、`fyne.io/systray` v1.12.2、Win32（user32/kernel32/shell32/comdlg32/crypt32）、`golang.org/x/sys/windows`（仅 Wails 传递依赖，业务代码仍用 `syscall`）。

**规格来源：** `docs/superpowers/specs/2026-11-26-windows-agent-design.md`

---

## 文件结构

| 文件 | 责任 |
|---|---|
| `internal/session/session.go` | 新增：配置目录/会话读写（原子写、0600） |
| `cmd/lanbox-cli/session.go` | 修改：委托给 `internal/session`，保留原函数名与语义 |
| `internal/agent/clip.go` | 新增：`Kind`、`Clip`、`Settings`、`Job`、`Record`、错误分类 |
| `internal/agent/pipeline.go` | 新增：9 条决策规则、去重窗口、目录展开、阈值 |
| `internal/agent/manager.go` | 新增：单 worker 上传、进度、退避重试、历史、暂停、撤回 |
| `internal/agent/uploader.go` | 新增：`Uploader`/`Statter`/`Walker` 接口与默认实现 |
| `internal/winclip/api_windows.go` | 新增：Win32 绑定（user32/kernel32/shell32/comdlg32/crypt32） |
| `internal/winclip/clipboard_windows.go` | 新增：`ReadClipboard`、`Watcher`（message-only 窗口 + 监听） |
| `internal/winclip/dib_windows.go` | 新增：`decodeDIB`/`DIBToPNG`（纯函数） |
| `internal/winclip/dpapi_windows.go` | 新增：`Protect`/`Unprotect` |
| `internal/winclip/dialog_windows.go` | 新增：`PickFiles`/`PickFolder` |
| `internal/winclip/stub_other.go` | 新增：非 Windows 桩，保证 `go vet ./...` 可用 |
| `cmd/lanbox-agent/main.go` | 新增：单实例、日志、Wails 启动、托盘、装配 |
| `cmd/lanbox-agent/app.go` | 新增：绑定方法（状态/设置/登录/发现/发送/历史/暂停） |
| `cmd/lanbox-agent/settings.go` | 新增：`agent.json` 读写与默认值 |
| `cmd/lanbox-agent/auth.go` | 新增：登录、TLS 指纹固定、自动重登、会话复用 |
| `cmd/lanbox-agent/tray.go` | 新增：托盘菜单与状态图标 |
| `cmd/lanbox-agent/assets.go` | 新增：`//go:embed all:frontend/dist` |
| `cmd/lanbox-agent/frontend/dist/{index.html,app.js,app.css}` | 新增：vanilla 前端 |
| `Makefile`、`README.md`、`build-windows.ps1` | 修改：`agent` 目标与文档 |
| `docs/superpowers/specs/2026-11-26-windows-agent-design.md` | 已完成 |

**边界规则：**
- `internal/agent` 不得 import `internal/client`、任何 Win32 或 Wails 包
- `internal/winclip` 不得做业务判断，只输出 `agent.Clip`
- `cmd/lanbox-agent` 是唯一装配层；Windows-only 文件全部加 `//go:build windows`
- `internal/session` 不 import `cmd/*`

---

## Task 1: 抽出 `internal/session` ✅

**Files:** Create `internal/session/session.go`、`internal/session/session_test.go`；Modify `cmd/lanbox-cli/session.go`

**Step 1: 测试**（临时目录往返、原子写、无文件返回 nil、Windows 不校验权限位）

```go
func TestRoundTrip(t *testing.T) {
    t.Setenv("XDG_CONFIG_HOME", t.TempDir())
    want := &client.Session{URL: "https://x", Username: "u", SID: "s1", PinFP: "ab"}
    if err := session.Save(want); err != nil { t.Fatal(err) }
    got, err := session.Load()
    if err != nil || got.SID != "s1" || got.PinFP != "ab" { t.Fatalf("%v %+v", err, got) }
    if _, err := os.Stat(session.Path() + ".tmp"); !os.IsNotExist(err) { t.Fatal("残留 tmp") }
}
```

**Step 2:** 跑测试应失败（包不存在）
**Step 3:** 实现 `Path()/Save(*client.Session)/Load()`：`XDG_CONFIG_HOME/lanbox` 优先，Windows 用 `os.UserConfigDir()`，其余 `~/.config/lanbox`；`Load` 在文件缺失时返回 `(nil,nil)`；mtime 无关
**Step 4:** 改 `cmd/lanbox-cli/session.go` 为薄封装（`sessionPath`→`session.Path`、`saveSession`→`session.Save`、`loadSessionOptional`→`session.Load`、`removeSession`→`session.Remove`），`loadSession` 保留 `errAuth` 包装
**Step 5:** `gofmt -l .`、`go vet ./...`、`go test ./cmd/lanbox-cli ./internal/session` 全绿
**Step 6:** commit `refactor(session): 抽出内部会话包供 CLI 与 GUI 共用`

## Task 2: `internal/agent` 决策 Pipeline ✅

**Files:** Create `internal/agent/clip.go`、`pipeline.go`、`pipeline_test.go`

**Step 1: 测试**（表驱动，每条规则一个用例 + 组合用例）

```go
func TestDecide(t *testing.T) {
    // 忽略格式 / 空内容 / 文件优先于文本 / 只读文件 / 文本超限 / 去重窗口 / 图片超限 / 目录展开与限额
}
```

**Step 2:** 跑测试失败
**Step 3:** 实现：

```go
type Kind string // "text" | "files" | "image"

type Clip struct {
    At time.Time; Ignore bool; Text string; Paths []string; PNG []byte; Source string
}
type Settings struct {
    Enabled, Paused, Text, Files, Images bool
    DedupeSeconds, MaxTextBytes, MaxImageMB, MaxTotalMB, MaxFiles int
    TextPrefix string
}
type Decision struct { Upload bool; Reason string; Kind Kind; Text string; Files []File; Hash string }
type File struct { Path, Rel string; Size int64 }
```

规则：暂停/未登录（`Pipeline.SetAvailable`）→ 忽略格式 → 去抖（`Debounce` 由 Manager 负责）→ `Paths > PNG > Text` → 去重窗口（sha256，文件按 `path|size|mtime`）→ 文本 trim/阈值 → 文件存在性/递归/不跟随符号链接/限额（超限整条跳过）→ 图片阈值。

**Step 4:** 测试通过；`go test ./internal/agent -run TestDecide -v`
**Step 5:** commit `feat(agent): 剪贴板决策流水线`

## Task 3: `internal/agent` 上传 Manager ✅

**Files:** Create `manager.go`、`manager_test.go`

**Step 1: 测试**（假 Uploader + 假时钟）：暂停不投递、去抖合并、进度事件、成功入历史、413 不重试、网络错误退避序列 2s/10s/30s、401 触发一次重登且不无限重试、撤回调 DeleteItem、历史容量上限
**Step 2:** 失败
**Step 3:** 实现 `Manager`：

```go
type Uploader interface {
    Put(ctx context.Context, text string, files []File, onProgress func(sent, total int64)) (string, error)
    Delete(ctx context.Context, itemID string) error
}
type Manager struct{ ... }
func NewManager(opts Options) *Manager       // OnChange func(State)、HistorySize、Clock、Backoff
func (m *Manager) Submit(c Clip)             // 自动（去抖 + Pipeline）
func (m *Manager) SubmitManual(text string, files []File) (Record, error)
func (m *Manager) Pause(bool) / SetAvailable(bool)
func (m *Manager) Retry(id string) / Cancel(id string) / Withdraw(id string) error
func (m *Manager) History() []Record
```

单 worker goroutine 消费 job channel；上传用 `context.WithCancel` 支持取消；退避可注入 `sleep` 便于测试。
**Step 4:** 通过；**Step 5:** commit `feat(agent): 上传队列、退避重试与历史`

> 实施记录：真机验证（Windows 11 + WebView2 158）——文本、资源管理器复制的文件夹、截图三条链路均成功上传到真实服务端；`-tags e2e` 测试覆盖登录 + 指纹固定 + 文本/目录上传 + 撤回；关闭窗口后进程存活且剪贴板继续上传。

## Task 4: `internal/winclip` ✅

**Files:** Create `api_windows.go`、`clipboard_windows.go`、`dib_windows.go`、`dpapi_windows.go`、`dialog_windows.go`、`dib_test.go`、`stub_other.go`

**Step 1: 测试** `dib_test.go`：24/32bpp、自底向上、4 字节行填充、alpha 全 0 视为不透明、截断数据报错
**Step 2:** 失败
**Step 3:** 实现
- `ReadClipboard(ctx) (agent.Clip, error)`：`OpenClipboard` 重试 5×20ms；`RegisterClipboardFormatW("Clipboard Viewer Ignore")`；优先级 HDROP > CF_DIB > CF_UNICODETEXT；HDROP 用 `DragQueryFileW` 枚举；`kinds` 返回 `Clip{Kind 由 agent 判定}`
- `Watcher.Start(ch chan<- agent.Clip)`：`runtime.LockOSThread` + `CreateWindowExW(HWND_MESSAGE)` + `AddClipboardFormatListener`，`WM_CLIPBOARDUPDATE` 读取后投递；`WM_DESTROY` 时 `RemoveClipboardFormatListener`；`Close()` 用 `PostMessage(WM_CLOSE)`
- `DIBToPNG`/`decodeDIB` 纯函数
- `Protect/Unprotect`：`CryptProtectData`/`CryptUnprotectData` + `CRYPTPROTECT_UI_FORBIDDEN`
- `PickFiles`：`GetOpenFileNameW`（`OFN_ALLOWMULTISELECT|OFN_EXPLORER|OFN_FILEMUSTEXIST`，双 NUL 解析）；`PickFolder`：`SHBrowseForFolderW`
**Step 4:** `go test ./internal/winclip`
**Step 5:** commit

## Task 5: 装配层（设置、会话、登录、绑定）✅

**Files:** Create `cmd/lanbox-agent/{settings.go,auth.go,app.go,log.go}`

- `settings.go`：默认值、`Load/Save`（`%AppData%\lanbox\agent.json`，0600，原子写）、`Password` 字段 DPAPI 密文 base64
- `auth.go`：`pinnedTransport(fpHex)`（`crypto/tls` + `VerifyPeerCertificate`，比对叶证书 sha256）、`login(url,user,pass,remember)`、401 自动重登、写回 `internal/session`
- `app.go`：绑定方法
  `GetState()`、`GetSettings()`、`SaveSettings(Settings)`、`Discover()`、`Login(req)`、`LoginFound(instance,user,pass,remember)`、`Logout()`、`Pause(bool)`、`SendText(text)`、`SendFiles(text, paths []string)`、`UploadClipboardNow()`、`PickFiles()`、`PickFolder()`、`GetHistory()`、`Retry(id)`、`Cancel(id)`、`Withdraw(id)`、`OpenLog()`、`Version()`、`OpenDataDir()`
- 事件：`state`、`history`、`progress`
- 测试：`settings_test.go`（默认值合并、未知字段保留）、`auth_test.go`（指纹匹配/不匹配）
**Commit:** `feat(agent): 客户端装配层与绑定方法`

## Task 6: Wails 外壳 + 托盘 + 单实例 + 日志 ✅

**Files:** Create `cmd/lanbox-agent/{main.go,tray.go,assets.go,single_windows.go}`

- `main.go`：`CreateMutexW` 单实例 → 日志（2MB×2）→ `agent.NewManager` + `winclip.Watcher` → `wails.Run`（Window 980×680，`HideWindowOnClose=closeToTray`，`DragAndDrop{EnableFileDrop:true}`，`OnFileDrop` 转发到 `SendFiles`）→ 退出时 `Watcher.Close()`
- `tray.go`：`fyne.io/systray` 菜单：打开窗口 / 暂停·恢复 / 立即上传剪贴板 / 退出；状态图标三色（`iconGreen/iconYellow/iconRed`，内嵌 16×16 ICO）
- 构建：`go build -tags desktop,production -ldflags "-s -w -H=windowsgui"` 通过
**Commit:** `feat(agent): Wails 窗口、托盘与单实例`

## Task 7: 前端（vanilla，无 Node）✅

**Files:** Create `cmd/lanbox-agent/frontend/dist/{index.html,app.js,app.css}`

- `index.html`：登录视图（发现列表 / 手填地址 / 用户名密码 / 记住密码）+ 主视图（状态条 + 记录/设置两页）
- `app.js`：`window.go.main.App.*` 调用（Promise）、`window.runtime.EventsOn` 订阅、拖拽高亮、进度条、设置表单、错误 toast
- `app.css`：搬 `web/style.css` 的 `:root` 令牌与卡片/按钮/输入框样式，按窗口尺寸适配
- 验收：`go vet ./...`；`go build` 后手动启动，登录页渲染正常、无 JS 报错
**Commit:** `feat(agent): 客户端界面`

## Task 8: 构建与文档 ✅

**Files:** Modify `Makefile`、`README.md`、`build-windows.ps1`

- `Makefile`：`agent` 目标（本机）+ `dist` 追加两个 agent 产物 + `clean`
- `README.md`：新章节「Windows 客户端 lanbox-agent」：功能、配置表、限制（仅 Windows、WebView2）、构建命令、日志位置、暂停/关闭语义
- `build-windows.ps1`：可选加 `-Agent` 开关
**Commit:** `docs+chore: agent 构建目标与说明`

## Task 9: 全量验证 ✅

- `gofmt -l .` 无输出；`go vet ./...`；`go test ./...` 全绿
- `go build -tags desktop,production ./cmd/lanbox-agent` 产出 exe
- 启动 exe：单实例生效、托盘出现、窗口可交互；关闭窗口后托盘仍在且剪贴板监听继续
- 对本机 `lanbox-cli` 起一个临时服务端做一次真实上传（`/api/items` 返回 id）与撤回
- 更新 `docs/superpowers/plans/…` 勾选进度
