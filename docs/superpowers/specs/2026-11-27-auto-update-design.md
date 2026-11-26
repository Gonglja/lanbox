# lanbox 自动更新（检查 + 手动升级 + 回滚）设计

日期：2026-11-27
状态：已实现
范围：新增零依赖包 `internal/updater`；服务端 `lanbox` 增加更新配置、四个管理接口、状态页入口与 `lanbox-cli update`；Windows `lanbox-agent` 增加设置页「关于」与托盘入口。**只替换可执行文件**，不碰 `config.json`、`data/`、`~/.config/lanbox`、`agent.json`。不改存储格式、不引入第三方依赖。

## 1. 目标

1. **知道有没有新版**：服务端与 agent 启动时查一次，之后每天一次，有新版只提示、不打扰。
2. **升级由人触发**：下载 → 校验 → 替换 → 重启，全流程可解释、失败不留残局。
3. **回滚成本低**：旧二进制留作 `.old`，一条命令换回去。
4. **更新源可换**：默认 GitHub Release（不限流、零 API），也能指向自建 Gitea、内网静态站或镜像。

## 2. 更新源

### 2.1 `github` 模式（默认，`update.url` 为空）

不调 GitHub API（未认证 60 次/小时，且本机 IP 常被限流），只走静态下载路径：

```
GET {base}/releases/latest/download/SHA256SUMS
   → 302 {base}/releases/download/v1.2.4/SHA256SUMS
     ↑ 从最终 URL 解析出版本 v1.2.4
GET {base}/releases/download/v1.2.4/{asset}   → 新二进制
```

`base` 默认 `https://github.com/Gonglja/lanbox`。校验值取自同一 Release 的 `SHA256SUMS`（格式：`<hex>  <文件名>`，与现有 `make checksums` 一致）。

- 该模式**没有更新说明、没有发布时间**（静态路径拿不到 Release body）。UI 只显示版本号与体积。
- 私有仓库：`LANBOX_UPDATE_TOKEN` 环境变量注入 `Authorization: Bearer`，走 302 之后仍带着（同域）。

### 2.2 `manifest` 模式（`update.url` 以 `.json` 结尾，或非 GitHub 主机）

```
GET {update.url} → latest.json
{
  "version": "1.2.4",
  "published": "2026-11-27T10:00:00Z",
  "notes": "- 修了什么",
  "assets": [
    { "name": "lanbox-linux-amd64", "url": "https://…", "size": 12345678, "sha256": "…" }
  ]
}
```

`assets` 里找不到当前平台的 `Asset` → 视为「该版本不提供本平台」，不报错，UI 显示「暂无本平台产物」。`sha256` 必填。

### 2.3 产物名（与 `make dist` / `make windows` 完全一致）

| 运行目标 | Asset |
|---|---|
| 服务端 | `lanbox-linux-amd64` / `lanbox-linux-arm64` / `lanbox-linux-armv7` / `lanbox-linux-386` / `lanbox-windows-amd64.exe` / `lanbox-windows-arm64.exe` |
| agent | `lanbox-agent-windows-amd64.exe` / `lanbox-agent-windows-arm64.exe` |

`runtime.GOARCH == "arm"` 映射到 `armv7`（与 `dist` 命名规则一致；GOARM=6 的构建不存在）。

## 3. `internal/updater`

纯标准库，服务端与 agent 共用。

```go
type Options struct {
    Current string        // 与 -X main.version 同值；"dev" 视为最低版本
    Asset   string
    BaseURL string        // github 模式基地址
    Manifest string       // 非空则进 manifest 模式
    Token   string
    Client  *http.Client  // 检查 15s 超时；下载另建（不设总超时，靠 ctx）
    Log     *log.Logger
}

type Release struct {
    Version   string    // 规范化后 "1.2.4"（无 v 前缀）
    Tag       string    // "v1.2.4"
    Notes     string
    Published time.Time
    AssetName string
    AssetURL  string
    Size      int64
    SHA256    string
}

var ErrUpToDate = errors.New("已是最新版本")

func New(o Options) (*Updater, error)
func (u *Updater) Check(ctx context.Context) (Release, error)
func (u *Updater) Download(ctx context.Context, dir string, r Release) (path string, err error)
func (u *Updater) Stage(exePath string, r Release) (backup string, err error) // 校验后替换
func (u *Updater) Rollback(exePath string) (string, error)
```

### 3.1 版本比较

`parseVersion`：去 `v`/`V` 前缀 → 核心 `MAJOR[.MINOR[.PATCH]]`（缺省补 0）→ 可选 `-pre`（按 `.` 分段，数字段数值比较、字母段字典序，数字段 < 字母段）→ 忽略 `+build`。

| 比较 | 结果 |
|---|---|
| `1.9.9` vs `1.10.0` | 后者新 |
| `1.2.3` vs `v1.2.3` | 相等 |
| `1.2.3` vs `1.2.3-rc1` | 正式版新 |
| `1.2.3-rc1` vs `1.2.3-rc2` | rc2 新 |
| `dev` / 空串 / 非法 | 视为 `0.0.0-dev`（比任何正式版旧）→ 永远提示可升级 |

无法解析**远端**版本 → 报错，不升级（宁可不动）。

### 3.2 校验

1. 大小与 `Release.Size` 一致（manifest 给出时）。
2. SHA256 与清单一致。
3. 魔数：Linux `\x7fELF`；Windows `MZ`。防下到登录页 HTML / 302 落地页。
4. 清单缺失或清单里没有本平台条目 → **拒绝升级**，不降级为「不校验」。

### 3.3 替换与回滚（平台特化，`replace_unix.go` / `replace_windows.go`）

临时文件落在**目标 exe 同目录**（`.lanbox-update-<ver>.tmp`），避免跨卷 `rename` 失败；这同时是最早的写权限探测——写不进去就在下载阶段失败，绝不会出现「旧文件已改名、新文件写不进」的半残状态。

```
Stage:
  写 tmp（同目录）→ 校验 → chmod 0755
  rename(exe, exe+".old")     // Windows 上允许对运行中的 exe 改名（同目录 MoveFile）
  rename(tmp, exe)
  失败：rename(exe+".old", exe) 还原，删 tmp，报具体阶段
Rollback:
  remove(exe+".failed") → rename(exe, exe+".failed") → rename(exe+".old", exe)
```

`exe+".old"` 只保留最近一次升级的备份（下次升级覆盖）。启动时清理遗留 `.tmp`；`.failed` 留作诊断。

## 4. 服务端集成

### 4.1 配置

```json
"update": { "check": "daily", "url": "" }
```

| 键 | 默认 | 说明 |
|---|---|---|
| `update.check` | `daily` | `off` / `startup` / `daily` |
| `update.url` | `""` | 空 → `github` 模式；`.json` 结尾 → `manifest` 模式取该地址；其他 → 视为基地址（`{url}/releases/latest/download/…`） |

环境变量：`LANBOX_UPDATE_CHECK`、`LANBOX_UPDATE_URL`、`LANBOX_UPDATE_TOKEN`（token 只认环境变量，不落盘、不回传前端、不进 `Origins`）。

两个键加入 `editableKeys`，设置页「更新」分组可视化；热更生效（不进 `restartFields`）。

### 4.2 宿主注入

`app.Options` 新增 `Updater *updater.Updater`（nil = 不支持更新）。Android/mobile 宿主不传 → 所有 `/api/update*` 返回 `501`，等价于现有 `RestartDisabled` 语义。

### 4.3 接口

| 接口 | 鉴权 | 行为 |
|---|---|---|
| `GET /api/update` | 登录 | `{version, latest, notes, published, asset_size, checked_at, error, updating}`，**不联网**，读内存缓存 |
| `POST /api/update/check` | admin | 立刻检查；成功写缓存并返回同结构；失败返回 `502` + 原因，保留上次缓存 |
| `POST /api/update/apply` | admin | body `{"version":"1.2.4"}`，必须等于缓存里的 `latest`，否则 `409 版本不一致，请重新检查`；流程：Download → Stage → `OnRestart()`；返回 `202` |
| `POST /api/update/rollback` | admin | `.old` 不存在 → `409`；否则 Rollback → `OnRestart()`，返回 `202` |

- apply 全程持 `sync.Mutex`，进行中再来一个请求 → `409 升级进行中`。
- 阶段错误映射成可读文本：`下载失败` / `校验失败` / `替换失败` / `写权限不足`（含 `EACCES`/`EPERM` 特判，提示「程序所在目录不可写，请用 install 到 /usr/local/bin 或管理员运行」）。
- 升级期间 SSE 广播一条 `update` 事件，前端提示「即将重启」。

### 4.4 后台检查

`app.Start()` 成功后起一个 goroutine：

- `off`：不做任何事。
- `startup`：启动后 10 秒查一次。
- `daily`：启动后 10 秒查一次，之后每 24 小时一次（`time.Ticker`）；`Shutdown` 时停。

失败只写日志一行（`warn: 检查更新失败: …`），不重试、不累积计数。结果只进内存缓存（重启即忘）。

### 4.5 Web

状态页「服务」卡片增加一行「最新版本」：

- 非管理员：只显示当前版本，无网络请求（`GET /api/update` 已有缓存，反正只是一个数字）。
- 管理员：显示「最新版本 / 上次检查」+「检查更新」按钮；有新版时追加「升级到 v1.2.4 并重启」（二次确认对话框，展示体积与更新说明）+「回滚到 vX.Y.Z」。

升级/回滚成功后用户主动点确认，页面显示「正在重启，刷新后生效」，并在 `visibilitychange` 时重新 `GET /api/stats` 判断是否已回来。

### 4.6 CLI

```
lanbox-cli update            # 显示当前/最新版本与上次检查时间
lanbox-cli update --check    # 立即检查（无需管理员也能看结果？——需要 admin 才触发网络检查）
lanbox-cli update --apply [--yes]
lanbox-cli update --rollback [--yes]
```

`--apply` / `--rollback` 在 TTY 下要求输入 `yes` 确认（`--yes` 跳过）；非 TTY 直接报用法错误（沿用现有 `args` 风格与退出码 `2`）。输出沿用 `render.go` 的表格风格。

## 5. agent 集成

### 5.1 后端（Wails 绑定）

```go
type UpdateState struct {
    Current, Latest, Notes string
    AssetSize              int64
    CheckedAt              int64
    CheckError             string
    Available, Updating, CanRollback bool
    Progress               int // 0-100，仅 Updating 时有意义
}

func (a *App) UpdateState() UpdateState
func (a *App) CheckUpdate() (UpdateState, error)
func (a *App) ApplyUpdate(version string) error
func (a *App) RollbackUpdate() error
```

状态变化用 `runtime.EventsEmit(ctx, "update:state", st)` 推送；下载进度 200ms 节流。

### 5.2 界面

- 设置页新增「关于」分组：版本号、检查时间、「检查更新」按钮；有新版时显示体积、更新说明（可折叠）、「升级并重启」、「回滚到 vX.Y.Z」（`.old` 存在才显示）。
- 托盘菜单在「立即上传剪贴板」之后插入：无新版 →「检查更新」；有新版 →「升级到 v1.2.4」（点击即装）；有 `.old` →「回滚到 vX.Y.Z」。
- 静默检查：`app.check_update`（`agent.json`，默认 `true`）开启时，启动 30 秒后查一次、之后每 24h；有新版只在 tooltip 尾部加 `· 有新版`，不弹窗。

### 5.3 Windows 两个坑

1. **单实例互斥**：新进程启动时旧进程仍握着 `Global\lanbox-agent`，`acquireSingleInstance()` 会返回 false 导致新进程静默退出，升级等于白做。新增隐藏参数 `-wait-pid <pid>`：旧进程启动新进程时带上自己的 PID，新进程在抢锁前 `OpenProcess` + `WaitForSingleObject` 等旧进程退出（最多 15 秒），再正常抢锁。参数不进 `help`、不写文档，只在日志里出现。
2. **写权限**：`Program Files` 需要管理员。替换前先在 exe 同目录建临时文件（写不进去立刻失败，报「请以管理员身份运行，或把程序放到可写目录」），绝不先改名再发现写不了。

### 5.4 升级流程

暂停剪贴板监听 → 下载并校验到同目录 `.tmp` → `lanbox-agent.exe` 改名为 `lanbox-agent.exe.old` → 新文件搬正 → 带 `-wait-pid` 启动新进程 → `wailsruntime.Quit` 优雅退出 → 新实例接管。设置、会话、历史全部保留（都落 `%AppData%\lanbox`）。

## 6. 失败与边界

| 场景 | 行为 |
|---|---|
| 后台检查失败（断网/超时/403/限流） | 一行日志 + 缓存里留 `error`，状态页注明「上次检查失败」；不打扰 |
| 手动检查失败 | 返回可读原因（含 HTTP 状态码与最终 URL） |
| 代理 | `http.Transport{Proxy: http.ProxyFromEnvironment}`（尊重 `HTTPS_PROXY`/`NO_PROXY`） |
| 302 环路/超过 10 跳 | 报错，不升级 |
| 无 `SHA256SUMS` | 拒绝升级 |
| 磁盘不足 | 下载前比对剩余空间（≥ 2× 资源大小，且尊重 `min_free_disk_mb`） |
| 校验失败 / 魔数不对 | 删临时文件，原 exe 完好，报阶段 |
| 重复 apply | 第二个请求 `409` |
| 已是最高版本 | `ErrUpToDate`，UI 显示「已是最新版本」 |
| 远端版本无法解析 | 报错，不升级 |
| `dev` 版本 | 视为最低，永远提示可升级（自编译版本想安静就把 `update.check` 设 `off`） |
| Android 宿主 | 无 Updater → 所有 `/api/update*` 返回 501 |

## 7. 测试

- `internal/updater/version_test.go`：表驱动（见 §3.1 表格 + 空串/非法/三位以上）。
- `internal/updater/updater_test.go`：`httptest` 假源——302 解析版本、`SHA256SUMS` 解析（含 `*` 前缀与多空格）、正常下载校验、改一字节后校验失败、HTML 错误页被魔数拦下、清单缺失拒绝、manifest 模式资产缺失、`ErrUpToDate`。
- `internal/updater/replace_test.go`（unix 构建）：临时目录放假 exe，替换后内容正确、`.old` 存在、`Rollback` 复原、目录只读时提前失败且原文件不变。
- `internal/httpapi/update_handlers_test.go`：注入假 `Updater`，覆盖鉴权、版本不一致 409、进行中 409、回滚无备份 409、`RestartFunc` 被调用、Updater 为 nil 时 501。
- `internal/config`：新键的默认值、环境变量、`Origins` 标注。
- 不做「真的重启进程」的端到端测试（CI 上不可控），用注入的 `RestartFunc` 断言。

## 8. 不做（YAGNI）

GPG/Ed25519 签名验证、增量/差分更新、Android 与 `lanbox-cli` 自更新、多版本并存、无人值守全自动升级、CDN/镜像测速、更新成功率统计上报。
