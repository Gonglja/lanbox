# lanbox Android（服务端 + 客户端一体）设计

日期：2026-11-26
状态：已与用户逐节确认（§1 架构 / §2 证书与免密登录 / §3 生命周期与配置 / §4 构建 / §5 测试与验收 均确认）

## 1. 目标

把 lanbox **整套搬到手机上**：一个 APK 里既有服务端（Go）也有客户端（WebView），手机既当共享盘也当收发端。

- 手机开启后，PC/平板/别的手机用浏览器访问 `https://<手机IP>:8443` 即可收发
- 手机自己点「打开界面」进入同一套 Web UI（内嵌 `web/`，零新增前端代码）
- 服务端代码**完全复用** `internal/*`，不重写业务逻辑

不做（YAGNI）：明文 HTTP、自动清理已取走内容、SAF 自选目录、Play Store 上架、armeabi-v7a、平板/横屏专门布局、后台自动更新。

## 2. 关键决策

| # | 决策 | 取值 | 理由 |
|---|---|---|---|
| 1 | 形态 | **单 APK（服务端 + WebView 客户端）** | 与 Windows 的 `lanbox-agent` 思路一致；复用 100% 前端 |
| 2 | 传输 | **仅自签 HTTPS**，端口 **8443** | 443 需 root；HTTP 会在局域网明文传密码/文件 |
| 3 | 服务开关 | **手动开关 + 记忆上次状态 + 开机自启可选** | 省电可控；「只当客户端」也成立 |
| 4 | 数据目录 | **应用私有目录**，无自动清理 | 零权限、零 SAF 适配；空间交给现有 `min_free_disk_mb` 保护 |
| 5 | minSdk / targetSdk | **26 / 35** | 8.0 起前台 Service 模型统一；覆盖率足够 |
| 6 | ABI | **仅 arm64-v8a** | 每个 ABI +10MB；2016 年后设备基本都是 arm64 |
| 7 | 分发 | **自签 APK 侧载** + SHA256 | 免开发者账号；常驻前台服务 + 自签证书不适合上架 |
| 8 | 包名 | `top.glj0.lanbox` | 用户指定 |
| 9 | UI 技术 | **XML 布局 + AppCompat，不引 Compose** | 只有一屏表单；构建可复现、APK 小 |
| 10 | Go 集成 | **gomobile bind → AAR** | Kotlin 直接调 Go；比手写 JNI 少一个数量级的胶水代码 |

技术选型否决记录：

| 备选 | 否决理由 |
|---|---|
| 纯 Kotlin 重写服务端 | 与现有 Go 实现分叉，双份行为要同步维护 |
| Termux + linux-arm64 二进制 | 用户要求「App」，不是「装个终端」；443/8443、保活、开机自启都靠用户折腾 |
| `-buildmode=c-shared` + 手写 JNI | 等价功能，但符号表/生命周期/线程挂接全要自己写，收益仅是少一个构建工具 |
| Compose | 一屏 UI 不值一个 Compose 编译器插件 + 一票依赖 |

## 3. 架构

```
android/                        新增，Gradle 工程（不属于 Go module）
  app/src/main/java/top/glj0/lanbox/
    MainActivity.kt             Compose 替代：XML 单页。开关 / 地址+复制 / 打开界面 / 日志 / 设置
    ServerService.kt            前台 Service（dataSync）：三把锁 + Go 生命周期 + 常驻通知
    WebViewActivity.kt          WebView → https://127.0.0.1:8443，onReceivedSslError 指纹固定
    BootReceiver.kt             BOOT_COMPLETED → 按 server_enabled && autostart 拉起
    Prefs.kt                    SharedPreferences：server_enabled / autostart
  app/src/main/AndroidManifest.xml
  app/src/main/jniLibs/arm64-v8a/libgojni.so   gomobile 产物（gitignore）
  app/libs/lanbox.aar                          gomobile 产物（gitignore）
mobile/                         新增，Go 包（仅标准库；不 import x/mobile）
  lanbox.go                     Start/Stop/Running/Addrs/CertPin/LoopbackToken/Logs/Version
  lanbox_test.go
internal/app/                   新增，从 cmd/lanbox 抽出的装配层
  app.go                        New / Start / Shutdown / Info
  app_test.go
cmd/lanbox/main.go              改：只留 flag、信号、自重启，装配交给 internal/app
internal/config/                改：支持 LockedKeys（App 强制字段，网页不可改）
internal/httpapi/               改：Options.AllowLoopbackLogin + GET /__app/login
```

分层边界：`mobile/` 不认识 Android，只接受「数据目录 + 强制 listen + 回调」；`internal/app` 不认识 Android 与 gomobile；Kotlin 不认识 lanbox 的内部包，只认 AAR 暴露的 8 个函数。

### Go 侧对外 API（gomobile 可绑定）

```go
func Configure(dataDir, listen, mdnsHostname, version string) // 每次 Start 前设置
func Start() error        // 起监听，幂等；失败返回可读错误
func Stop()               // 优雅关停，释放端口
func Running() bool
func Addrs() string       // 每行一个 https://<ip>:<port>（含 127.0.0.1 首行）
func CertPin() string     // sha256(DER) 小写 hex
func LoopbackToken() string
func Logs() string        // ring buffer 最近 200 行
```

全部用 `string/bool/error`，避免 gomobile 的结构体/回调绑定限制。

### 数据流

```
Compose View 开关 ON
  → startForegroundService(ServerService)
  → onStartCommand: WakeLock + WifiLock + MulticastLock
  → Go.Start()  → internal/app: config→auth→store→tlsutil→httpapi→mdns→net.Listen(":8443")
  → 通知显示 https://192.168.1.23:8443
点「打开界面」→ WebViewActivity
  → loadUrl("https://127.0.0.1:8443/__app/login?t=" + Go.LoopbackToken())
  → server 建 admin 会话 + Set-Cookie(sid) + 302 /
  → 之后的页面/SSE/上传全部是现成 Web UI
```

## 4. 证书信任与免密登录

**① 指纹固定（只认回环）**

- `CertPin()` = `sha256(cert.Raw)` 小写 hex，取自 `dataDir/tls/cert.pem`
- `WebViewClient.onReceivedSslError`：主机必须是 `127.0.0.1`，且 `SslCertificate(error.certificate)` 的 DER 摘要与 `CertPin()` 相等，才 `proceed()`；否则 `cancel()`
- **不做**全局忽略证书：连到其他服务器时仍走系统 CA 校验
- 前提已核实：`tlsutil.Hosts(":8443")` 已包含 `127.0.0.1`/`localhost`/`::1`，不会有 hostname mismatch，只会出现「未知 CA」

**② 一次性回环引导登录**

- `Go.Start()` 生成 32 字节随机 secret，只存内存、不写日志、不落盘
- `httpapi` 新增 `GET /__app/login?t=<secret>`，仅当同时满足才有效：
  1. `Options.AllowLoopbackLogin == true`（只有 `mobile/` 设，桌面版恒 false）
  2. 请求来源 IP 是 `127.0.0.1` / `::1`
  3. `subtle.ConstantTimeCompare` 通过
  4. 独立限速（10 次/分钟，进程级）
- 成功：为 `admin` 建会话，按 `handleLogin` 同样的 Cookie 属性写 `sid`，`Cache-Control: no-store`，`302 Location: /`
- 其他一切情况：`404`（不暴露路由存在性）
- 会话过期后（`/api/me` 401），Kotlin 在 `onResume`/首次加载时用 `evaluateJavascript` 探测，401 就重新加载引导 URL

## 5. 生命周期、保活与配置来源

### 状态机

```
SharedPreferences.server_enabled
  App 打开 + server_enabled=true  → 自动 startForegroundService
  开关 ON  → startForegroundService / 开关 OFF → stopService + Go.Stop()
  通知「停止」动作 → 走同一状态机（并写回 server_enabled=false）
  BOOT_COMPLETED → autostart && server_enabled 才拉起
  onDestroy → 释放三把锁 + Go.Stop()
  onTaskRemoved → 不自杀（否则息屏后无人应答）
```

三把锁：`PARTIAL_WAKE_LOCK`（防 CPU 睡眠）、`WifiManager.WifiLock(WIFI_MODE_FULL_HIGH_PERF)`（防 WiFi 省电）、`MulticastLock`（否则息屏收不到 mDNS 组播）。

`Go.Start()` 失败（8443 被占、证书损坏）→ 通知变红 + 首页展示错误 + 开关自动回弹 OFF。

### 配置来源

| 字段 | 手机上的取值 |
|---|---|
| `listen` | 强制 `:8443` |
| `data_dir` | 强制 `filesDir/lanbox` |
| `tls.cert_file` / `tls.key_file` | 强制空（自签，落在上面目录） |
| `min_free_disk_mb` | 强制 512（手机常见剩余空间） |
| `max_file_size_mb` | 强制 512（避免一条吃光存储） |
| `mdns.hostname` | 默认 `lanbox-<androidId 后 4 位>`，证书 SAN 追加该名字 |
| 其余（限额/会话时长/主题） | 沿用默认，网页管理页可改，写回 `filesDir/lanbox/config.json` |

实现方式：`config.Live` 增加 `LockedKeys`，网页提交这些键时返回 `400 字段由 App 固定`（不是静默忽略）；`internal/app` 在每次 `Hooks.Apply` 时把强制值覆盖回去，保证热重载后不漂移。

`POST /api/config/restart` 在手机上没有「重启进程」的语义：`internal/app` 不暴露进程自重启，该路由在 mobile 模式下返回 `501 not_supported`，网页提示「请在 App 里关开服务」（桌面端行为不变）。

### 首页 UI（一屏）

开关（带状态文案）、地址列表 + 复制、`打开界面`、最近 200 行日志（可折叠）、设置区（开机自启、加入电池优化白名单、清空数据）。低电量/未连 WiFi 时给出提示文案，但不阻止启动。

## 6. 构建与签名

```
make android
  1) 检测 ANDROID_HOME / NDK / JDK / gomobile，缺什么报什么（不静默失败）
  2) gomobile bind -target=android/arm64 -androidapi 26 \
       -ldflags "-X main.version=..." -o android/app/libs/lanbox.aar ./mobile
  3) (cd android && ./gradlew :app:assembleRelease)
  4) cp android/app/build/outputs/apk/release/app-release.apk dist/lanbox-android.apk
  5) 重算 dist/SHA256SUMS
make android-keystore   用 keytool 生成 android/lanbox-release.jks + 写 keystore.properties
```

- 无 `keystore.properties` 时退回 debug 签名，产物名带 `-debug` 并提示「无法覆盖安装」（侧载必须固定签名才能升级）
- `android/`、`keystore.properties`、`jniLibs/`、`app/libs/` 加入 `.gitignore`（生成物与私钥不入库）
- Windows 上另给 `build-windows.ps1` 的等价入口，与现有 `make windows` 分工一致
- Go 侧 `go.mod` 不变（`mobile/` 只用标准库）；Kotlin 侧仅 `androidx.appcompat` + `androidx.core`

环境前置（一次性）：`platforms;android-35`、`ndk;27.x`（约 1.5GB 下载）、JDK 17–21（优先由 Gradle toolchain 自动 provision Temurin 21，失败则退回 Studio 的 JBR）、adb 加入 PATH。

已实测的环境现状（本机）：SDK 在 `C:\Users\glj0\AppData\Local\Android\Sdk`（有 `android-30`、`android-37.0`，缺 35；无 NDK）；JDK 只有 Android Studio 的 JBR 25；adb 37.0.1；调试设备 `10.0.0.122:39655`（Xiaomi-13）当前 TCP 可达但 `adb connect` 失败，需重新开无线调试/重新配对。

## 7. 测试与验收

**Go（`make test` 全绿，且不得改变现有测试语义）**

- `internal/app`：起停幂等、ctx 取消后端口释放、强制字段覆盖且热重载后不漂移、绑定失败返回可读错误
- `httpapi`：`/__app/login` 正确令牌+回环→302 且带 `sid`；错令牌/非回环/`AllowLoopbackLogin=false`→404；限速生效；日志不记录 `t` 参数
- `mobile`：`CertPin()` 与证书 DER 一致；`Start/Stop` 后 `Running()` 正确；`Logs()` 环形缓冲不越界
- 回归：`go vet ./...`、`gofmt -l` 干净

**Android**：只做纯逻辑单测（URL 拼装、状态机判定），不做仪器测试；真机手动验收清单：

1. 首次安装 → 开关 ON → 通知出现 → 首页显示地址 → 「打开界面」免密进入
2. PC 浏览器首次信任证书 → 上传/下载/预览/Markdown 正常
3. 状态页二维码 → 另一台手机扫码打开
4. 息屏 10 分钟 → PC 仍能访问（三把锁生效）
5. 强杀 App → 重开 → 开关自动回 ON
6. 勾开机自启后重启手机 → 服务自动恢复
7. 服务 ON 时改网页配置（限额/主题）→ 立即生效；改 `listen` → 400 明确报错
8. 8443 被占的注入场景 → 开关回弹 + 错误可读

## 8. 风险

| 风险 | 缓解 |
|---|---|
| 自签证书导致其他设备首次访问有警告 | 明确文档 + 状态页二维码直接指向 `https://<ip>:8443`；指纹可在状态页核对 |
| 各家 ROM 杀后台（MIUI/EMUI 尤其） | 通知常驻 + WakeLock/WifiLock + 引导加白名单 + 失败时首页给出「可能被系统清理」提示 |
| gomobile 工具链版本漂移 | `make android` 打印 `gomobile version`；README 记录验证过的组合 |
| 抽 `internal/app` 改动现有服务端 | 先加测试再搬代码；`cmd/lanbox` 行为（信号/自重启/flag）逐条比对验收 |
| 手机存储被大文件占满 | `min_free_disk_mb=512` + `max_file_size_mb=512` 双保险，超限返回服务端原有错误 |
