# lanbox Android（服务端 + 客户端一体）实现计划

> **REQUIRED SUB-SKILL:** 用 executing-plans 技能按任务逐个执行本计划。

**Goal:** 产出一个 `top.glj0.lanbox` APK：前台 Service 里跑 Go 服务端（8443 自签 HTTPS），App 内 WebView 免密进入同一套 Web UI，手机既能当共享盘也能收发。

**Architecture:** 把 `cmd/lanbox` 的装配逻辑抽到 `internal/app`，再用 `mobile/`（仅标准库）包一层 gomobile 可绑定的 API → `gomobile bind` 出 AAR；Kotlin 侧只有 Service（三把锁 + 通知）、一屏 Compose 替代（XML）、WebView（证书指纹固定 + 回环引导登录）。

**Tech Stack:** Go 1.25（无第三方依赖）、gomobile、Gradle 8.13 + AGP 8.7.3 + Kotlin 2.0.21、AppCompat、minSdk 26 / targetSdk 35、仅 arm64-v8a。

**设计文档:** `docs/superpowers/specs/2026-11-26-android-app-design.md`
**分支:** `feat/android`（已从 `main` 创建，工作区干净）

---

## 约定

- 每个任务结束必须 `git commit`，提交前跑 `gofmt -l . && go vet ./... && go test ./... -count=1`（Go 侧任务）或 `make android-build`（Android 侧任务）。
- 命令都在仓库根目录 `D:/03.Project/lanbox` 执行；Windows 上 bash（Git Bash）可用。
- `ANDROID_SDK=/c/Users/glj0/AppData/Local/Android/Sdk`，`ADB=$ANDROID_SDK/platform-tools/adb.exe`。
- 真机：`10.0.0.122:39655`（Xiaomi-13），连不上时先在手机「开发者选项 → 无线调试」重新开并重新配对。

---

## Task 0: 环境准备（一次性）

**Files:** 无代码改动

**Step 1: 安装 compileSdk 平台与 NDK**

```bash
cd /c/1/Android/Android\ Studio/bin && ./studio64.exe   # 或用下面的 sdkmanager（需先装 cmdline-tools）
# 推荐：Android Studio → SDK Manager → SDK Platforms 勾 Android 15 (API 35)，
#       SDK Tools 勾 NDK (Side by side) 27.0.12077973、CMake
```

无 GUI 时：

```bash
mkdir -p "$ANDROID_SDK/cmdline-tools" && cd "$ANDROID_SDK/cmdline-tools"
# 下载 commandlinetools-win-13114758_latest.zip 并解压为 latest/
./latest/bin/sdkmanager.bat --sdk_root="$ANDROID_SDK" "platforms;android-35" "ndk;27.0.12077973" "build-tools;35.0.0"
```

**Step 2: 安装 JDK 21（Gradle 用；JBR 25 不能跑 Gradle 8.x）**

```bash
winget install --id EclipseAdoptium.Temurin.21.JDK -e
ls "/c/Program Files/Eclipse Adoptium/"   # 期望看到 jdk-21.x.x-hotspot
```

**Step 3: 验证**

```bash
export JAVA_HOME="/c/Program Files/Eclipse Adoptium/jdk-21.0.5.11-hotspot"
"$JAVA_HOME/bin/java" -version        # 期望 openjdk 21.x
ls "$ANDROID_SDK/platforms/android-35/android.jar"   # 期望存在
ls "$ANDROID_SDK/ndk/27.0.12077973"                  # 期望存在
export PATH="$ANDROID_SDK/platform-tools:$PATH"
adb connect 10.0.0.122:39655 && adb devices          # 期望 "<ip>:<port>  device"
```

**Step 4: 记录到 README 的「Android 构建前置」小节（本任务内只加占位，Task 13 补全）**

```bash
git commit -am "chore(android): 环境准备说明" --allow-empty
```

---

## Task 1: config.Live 支持「App 固定字段」

**Files:**
- Modify: `internal/config/live.go`
- Test: `internal/config/live_test.go`（新增用例）

**Step 1: 写失败测试**

```go
func TestLiveLockedKeys(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	live, err := OpenLive(cfgPath, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	live.Lock(map[string]string{"listen": ":8443", "data_dir": filepath.Join(dir, "data")})

	if live.Current().Listen != ":8443" {
		t.Fatalf("Lock 后当前配置未生效: %q", live.Current().Listen)
	}
	// 网页改被锁字段 → 明确报错，而不是静默忽略
	desired := live.Current()
	desired.Listen = ":9443"
	if _, err := live.Update(desired); !errors.Is(err, ErrLocked) {
		t.Fatalf("期望 ErrLocked，得到 %v", err)
	}
	if live.Current().Listen != ":8443" {
		t.Fatalf("被拒的更新不应生效")
	}
	// 未锁字段仍可改
	desired = live.Current()
	desired.MaxFilesPerItem = 42
	if _, err := live.Update(desired); err != nil {
		t.Fatalf("未锁字段应可更新: %v", err)
	}
	// 配置文件里写了别的值也会被锁覆盖
	if got := live.BaselineConfig().Listen; got != ":8443" {
		t.Fatalf("基线也应被锁覆盖，得到 %q", got)
	}
}
```

**Step 2: 运行，确认失败**

```bash
go test ./internal/config -run TestLiveLockedKeys -count=1
```
期望：编译失败 `live.Lock undefined`。

**Step 3: 实现**

```go
// ErrLocked 表示字段被宿主程序固定（Android App 强制 listen/data_dir）。
var ErrLocked = errors.New("字段由 App 固定")

// Lock 固定若干字段：文件值与网页覆盖都无法改变它们。
func (l *Live) Lock(pairs map[string]string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.locked == nil {
		l.locked = map[string]string{}
	}
	for k, v := range pairs {
		l.locked[k] = v
	}
	applyLocked(&l.cur, l.locked)
	applyLocked(&l.base, l.locked)
}

func applyLocked(c *Config, locked map[string]string) {
	for k, v := range locked {
		switch k {
		case "listen":
			c.Listen = v
		case "data_dir":
			c.DataDir = v
		case "min_free_disk_mb":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil { c.MinFreeDiskMB = n }
		case "max_file_size_mb":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil { c.MaxFileSizeMB = n }
		case "mdns.hostname":
			c.MDNS.Hostname = v
		case "tls.cert_file", "tls.key_file":
			c.TLS.CertFile, c.TLS.KeyFile = "", ""
		}
	}
}

func lockedValue(c *Config, key string) string { /* 与 applyLocked 对称的读值 */ }
```

在 `Live` 结构体加 `locked map[string]string`；`Update` 里 `desired.Validate()` 之后插入：

```go
for k, want := range l.lockedValueSnapshot() {
	if cur := lockedValue(&desired, k); cur != want {
		return res, fmt.Errorf("%w：%s", ErrLocked, k)
	}
}
applyLocked(&desired, l.locked)
```

`OpenLive` 返回前 `applyLocked(&cur, locked)`（此时 locked 为空，故无害）；`DropWebOverride` 开头 `if _, ok := l.locked[key]; ok { return res, nil }`。

**Step 4: 运行全部配置测试**

```bash
go test ./internal/config -count=1
```
期望：`ok lanbox/internal/config`。

**Step 5: 提交**

```bash
gofmt -l internal/config && go vet ./internal/config
git add internal/config && git commit -m "feat(config): 支持 App 固定字段（Lock）"
```

---

## Task 2: 抽出 internal/app 装配层

**Files:**
- Create: `internal/app/app.go`, `internal/app/app_test.go`
- Move: `cmd/lanbox/mdnsglue.go` → `internal/app/mdns.go`（类型改导出 `MDNSRunner`，函数名不变）
- Move: `cmd/lanbox/discover.go` → `internal/app/discover.go`（同上）
- Modify: `cmd/lanbox/main.go`

**Step 1: 写失败测试**

```go
func TestStartStopReleasesPort(t *testing.T) {
	dir := t.TempDir()
	a, err := New(Options{
		CfgPath: filepath.Join(dir, "config.json"),
		Web:     fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>ok</html>")}},
		Version: "test",
		Log:     log.New(io.Discard, "", 0),
		Locked:  map[string]string{"listen": "127.0.0.1:0", "data_dir": dir},
	})
	if err != nil { t.Fatal(err) }
	if err := a.Start(); err != nil { t.Fatal(err) }
	addr := a.Info().Addrs[0]          // "https://127.0.0.1:<port>"
	if err := a.Shutdown(); err != nil { t.Fatal(err) }
	if _, err := net.Listen("tcp", strings.TrimPrefix(addr, "https://")); err != nil {
		t.Fatalf("Shutdown 后端口仍被占用: %v", err)
	}
}

func TestStartTwiceIsIdempotent(t *testing.T) { /* Start/Start/Shutdown，无 error，无 panic */ }

func TestLockedFieldsWin(t *testing.T) {
	// 配置文件写 listen=:9443，Options.Locked 写 :0 → Current() 是 :0
}
```

**Step 2: 运行确认失败**

```bash
go test ./internal/app -count=1     # 期望 package 不存在
```

**Step 3: 实现 `internal/app/app.go`**

```go
package app

type Options struct {
	CfgPath            string
	Web                fs.FS
	Version            string
	Log                *log.Logger
	Locked             map[string]string
	AllowLoopbackLogin bool
	OnRestart          func()   // nil = 不支持进程自重启（手机）
}

type Info struct {
	Addrs  []string // 每行 https://ip:port，首行 127.0.0.1
	Listen string
	CertPin string
}

type App struct {
	opts Options
	live *config.Live
	st   *store.Store
	am   *auth.Manager
	srv  *httpapi.Server
	http *http.Server
	ln   net.Listener
	mdns *MDNSRunner
	logs *RingWriter
	loopbackToken string
	mu   sync.Mutex
	running bool
}
```

- `New`：`config.OpenLive(opts.CfgPath, hooks)` → `live.Lock(opts.Locked)` → `os.MkdirAll(cfg.DataDir)` → `auth.Open` → `BootstrapAdmin`（密码写日志，手机端日志进环形缓冲）→ `store.Open` → `tlsutil.Ensure`（hosts = `tlsutil.Hosts(cfg.Listen)` + `cfg.MDNS.Hostname+".local"`）→ `httpapi.New`。热更 `hooks.Apply` 里 `st.SetLimits` / `am.SetSessionTTL` / `mdns.Apply`；`hooks.Restart` 仅在 `OnRestart != nil` 时转发。
- `Start`：`mdns.Apply` → `net.Listen("tcp", cfg.Listen)` → `httpSrv.ServeTLS`，`running=true`；失败返回 `fmt.Errorf("监听 %s 失败: %w", ...)`。
- `Shutdown`：`srv.Close()`（断 SSE）→ `httpShutdown(5s)` → `mdns.Close()` → `running=false`，可重复调用。
- `Info`：从 `ln.Addr()` 拿真实端口（兼容 `:0`），枚举 `tlsutil.LocalIPs()` 生成 `https://ip:port`，首行固定 `https://127.0.0.1:port`；`CertPin` 复用 `certFingerprint(certPath)`。
- `RingWriter`：`io.Writer`，保留最近 200 行，`Logs() string`；`New` 里 `opts.Log = log.New(io.MultiWriter(os.Stdout?, ring), ...)`——手机端传 `nil` 时只进环形缓冲。

**Step 4: 改 `cmd/lanbox/main.go`**（行为不变）

```go
a, err := app.New(app.Options{
	CfgPath: *cfgPath, Web: lanbox.WebFS(), Version: version, Log: logger,
	OnRestart: func() { select { case restartCh <- struct{}{}: default: } },
})
if *resetAdmin { ... }        // 仍走 a.ResetAdmin()（在 app 里暴露）
if err := a.Start(); err != nil { return err }
select { case sig := <-sigCh: a.Shutdown() ; case <-restartCh: a.Shutdown(); restartProcess() }
```

`printPasswordBox` / `printAccessURLs` 留在 `cmd/lanbox`（桌面输出），`app` 只提供 `a.NewAdminPassword()` 与 `a.Info()`。

**Step 5: 回归**

```bash
gofmt -l . && go vet ./... && go test ./... -count=1
./lanbox -version >/dev/null && echo OK     # 先 make build
make build && go run ./cmd/lanbox-cli status -s https://127.0.0.1 2>&1 | head -3   # 可选冒烟
```
期望：全部 ok；`cmd/lanbox` 的 flag（`-config/-reset-admin-password/-version`）行为与改动前一致。

**Step 6: 提交**

```bash
git add internal/app cmd/lanbox && git commit -m "refactor(app): 抽出服务装配层供桌面与 Android 共用"
```

---

## Task 3: 回环引导登录 `/__app/login`

**Files:**
- Modify: `internal/httpapi/server.go`（Options + 路由）、`internal/httpapi/auth_handlers.go`
- Test: `internal/httpapi/loopback_test.go`（新增）

**Step 1: 写失败测试**

```go
func TestLoopbackLogin(t *testing.T) {
	s, _ := newTestServer(t)                     // 用现有测试辅助
	s.allowLoopback = true
	s.loopbackToken = "s3cret"
	// 1) 回环 + 正确令牌 → 302 + Set-Cookie
	req := httptest.NewRequest("GET", "/__app/login?t=s3cret", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	w := httptest.NewRecorder(); s.ServeHTTP(w, req)
	if w.Code != 302 || !strings.Contains(fmt.Sprint(w.Header()), "sid=") { t.Fatal(...) }
	// 2) 非回环 → 404
	// 3) 错令牌 → 404
	// 4) allowLoopback=false → 404
	// 5) 连续 11 次错令牌 → 第 11 次 429
}
```

**Step 2: 运行确认失败**

```bash
go test ./internal/httpapi -run TestLoopbackLogin -count=1   # 期望 404/字段缺失
```

**Step 3: 实现**

```go
// Options 增加：
AllowLoopbackLogin bool
LoopbackToken      string

// routes() 增加（无条件注册，处理器内部判断开关）：
s.mux.HandleFunc("GET /__app/login", s.handleLoopbackLogin)

func (s *Server) handleLoopbackLogin(w http.ResponseWriter, r *http.Request) {
	if !s.allowLoopback || s.loopbackToken == "" { http.NotFound(w, r); return }
	ip := net.ParseIP(clientIP(r))
	if ip == nil || !ip.IsLoopback() { http.NotFound(w, r); return }
	if !s.loopbackLimiter.Allowed("loopback") { writeError(w, 429, "too_many", "请求过于频繁"); return }
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(s.loopbackToken)) != 1 {
		s.loopbackLimiter.Fail("loopback"); http.NotFound(w, r); return
	}
	token, expires, err := s.auth.NewSession("admin")
	if err != nil { s.writeStoreErr(w, err); return }
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: expires})
	w.Header().Set("Cache-Control", "no-store")
	s.log.Printf("info: 回环引导登录成功")
	http.Redirect(w, r, "/", http.StatusFound)
}
```

限速器：`auth.NewLimiter(...)` 或复用 `internal/auth` 里已有的 `Limiter`（看 `internal/auth/limiter.go` 的构造函数签名，按需构造一个独立实例，10 次/分钟）。

**Step 4: 通过**

```bash
go test ./internal/httpapi -count=1 && go vet ./internal/httpapi
```
期望：`ok lanbox/internal/httpapi`（含原有 40+ 用例）。

**Step 5: 提交**

```bash
git add internal/httpapi && git commit -m "feat(httpapi): 回环一次性引导登录（仅移动端启用）"
```

---

## Task 4: `mobile/` 包（gomobile 可绑定 API）

**Files:**
- Create: `mobile/lanbox.go`, `mobile/lanbox_test.go`

**Step 1: 写失败测试**

```go
func TestCertPinAndLogs(t *testing.T) {
	dir := t.TempDir()
	Configure(dir, "127.0.0.1:0", "lanbox-test", "test")
	if err := Start(); err != nil { t.Fatal(err) }
	defer Stop()
	if !Running() { t.Fatal("Running 应为 true") }
	if len(Addrs()) == 0 || !strings.HasPrefix(Addrs()[0], "https://127.0.0.1") { t.Fatal(Addrs()) }
	if len(CertPin()) != 64 { t.Fatalf("CertPin 应为 64 位 hex: %q", CertPin()) }
	if len(LoopbackToken()) != 64 { t.Fatalf("引导令牌应为 64 位 hex") }
	if !strings.Contains(Logs(), "监听") { t.Fatalf("日志应包含启动信息: %q", Logs()) }
	Stop()
	if Running() { t.Fatal("Stop 后 Running 应为 false") }
}

func TestDoubleStartIsSafe(t *testing.T) { /* Start(); Start(); Stop(); + 无 panic */ }
```

**Step 2: 运行确认失败**

```bash
go test ./mobile -count=1     # 期望 package 不存在
```

**Step 3: 实现**

```go
// Package mobile 是 Android 端（gomobile bind）的唯一入口。
// 约束：只用标准库；导出函数只出现 string/bool/error（gomobile 绑定限制）。
package mobile

func Configure(dataDir, listen, mdnsHostname, version string)
func Start() error
func Stop()
func Running() bool
func Addrs() string      // 每行一个 URL
func CertPin() string
func LoopbackToken() string
func Logs() string
func AdminPassword() string  // 仅首次创建 admin 时有值，供 UI 展示
```

实现要点：
- 用 `var cur *app.App` + `sync.Mutex`；`Start` 时构造 `app.Options{Locked: map[string]string{"listen": listen, "data_dir": dataDir, "min_free_disk_mb": "512", "max_file_size_mb": "512", "mdns.hostname": mdnsHostname}, AllowLoopbackLogin: true, OnRestart: nil}`。
- 环形日志：`internal/app` 的 `RingWriter` 传进 `Options.Log`。
- `LoopbackToken()` 返回 `app.Info().LoopbackToken`（在 `app.New` 里 `crypto/rand` 32 字节 hex，仅内存）。
- `AdminPassword()` 保存 `BootstrapAdmin` 首次返回的密码。

**Step 4: 通过 + 全量回归**

```bash
go test ./mobile ./internal/app -count=1 && go test ./... -count=1
```
期望：全 ok；`go.mod` 未被改动（无新增依赖）。

**Step 5: 提交**

```bash
git add mobile && git commit -m "feat(mobile): gomobile 可绑定的服务端入口"
```

---

## Task 5: gomobile 产出 AAR（工具链验证）

**Files:**
- Modify: `.gitignore`
- 产物（不入库）：`android/app/libs/lanbox.aar`、`android/app/src/main/jniLibs/arm64-v8a/libgojni.so`

**Step 1: 安装 gomobile**

```bash
export JAVA_HOME="/c/Program Files/Eclipse Adoptium/jdk-21.0.5.11-hotspot"
export ANDROID_HOME="$ANDROID_SDK"
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
export PATH="$PATH:$(go env GOPATH)/bin"
gomobile init            # 首次会编译 x/mobile 工具
gomobile version
```
期望：`gomobile version +<commit> ...`。

**Step 2: 生成 AAR**

```bash
mkdir -p android/app/libs
gomobile bind -target=android/arm64 -androidapi 26 \
  -javapkg=top.glj0.lanboxcore \
  -o android/app/libs/lanbox.aar ./mobile
ls -la android/app/libs/lanbox.aar
```
期望：文件存在，约 10–15MB。失败时常见原因：`ANDROID_HOME`/`ANDROID_NDK_HOME` 未导、NDK 版本与 gomobile 不匹配（改用 `ANDROID_NDK_HOME=$ANDROID_SDK/ndk/27.0.12077973`）。

**Step 3: 检查生成的 Java 签名**

```bash
unzip -o -q android/app/libs/lanbox.aar -d /tmp/aar && find /tmp/aar -name "*.jar" | head
unzip -l /tmp/aar/classes.jar | grep -i mobile
```
期望：`top/glj0/lanboxcore/Mobile.class`（静态方法 `Start/Stop/Configure/...`）。

**Step 4: .gitignore**

```gitignore
android/app/libs/
android/app/src/main/jniLibs/
android/.gradle/
android/build/
android/app/build/
android/local.properties
android/keystore.properties
android/*.jks
```

**Step 5: 提交**

```bash
git add .gitignore && git commit -m "chore(android): 忽略 gomobile 产物与签名文件"
```

---

## Task 6: Gradle 工程骨架（能编出空 APK）

**Files:**
- Create: `android/settings.gradle.kts`、`android/build.gradle.kts`、`android/gradle.properties`、`android/app/build.gradle.kts`、`android/app/src/main/AndroidManifest.xml`、`android/app/src/main/res/values/strings.xml`、`android/app/src/main/res/values/themes.xml`、`android/app/src/main/java/top/glj0/lanbox/MainActivity.kt`
- Create: `android/gradlew`（用现有 Gradle 生成或手写 wrapper）

**Step 1: 建工程文件**

`android/settings.gradle.kts`
```kotlin
pluginManagement { repositories { google(); mavenCentral(); gradlePluginPortal() } }
dependencyResolutionManagement { repositories { google(); mavenCentral() } }
rootProject.name = "lanbox"
include(":app")
```

`android/build.gradle.kts`
```kotlin
plugins {
    id("com.android.application") version "8.7.3" apply false
    id("org.jetbrains.kotlin.android") version "2.0.21" apply false
}
```

`android/gradle.properties`
```properties
org.gradle.jvmargs=-Xmx2g
android.useAndroidX=true
org.gradle.java.home=C:/Program Files/Eclipse Adoptium/jdk-21.0.5.11-hotspot
```

`android/app/build.gradle.kts`
```kotlin
plugins { id("com.android.application"); id("org.jetbrains.kotlin.android") }

android {
    namespace = "top.glj0.lanbox"
    compileSdk = 35
    defaultConfig {
        applicationId = "top.glj0.lanbox"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0"
        ndk { abiFilters += "arm64-v8a" }
    }
    buildFeatures { buildConfig = true }
    compileOptions { sourceCompatibility = JavaVersion.VERSION_17; targetCompatibility = JavaVersion.VERSION_17 }
    kotlinOptions { jvmTarget = "17" }
    buildTypes { release { isMinifyEnabled = false } }
}

dependencies {
    implementation(files("libs/lanbox.aar"))
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("androidx.core:core-ktx:1.13.1")
}
```

`AndroidManifest.xml`（Task 7-10 再补 service/receiver）
```xml
<manifest xmlns:android="http://schemas.android.com/apk/res/android">
    <uses-permission android:name="android.permission.INTERNET"/>
    <uses-permission android:name="android.permission.ACCESS_NETWORK_STATE"/>
    <uses-permission android:name="android.permission.ACCESS_WIFI_STATE"/>
    <uses-permission android:name="android.permission.CHANGE_WIFI_MULTICAST_STATE"/>
    <uses-permission android:name="android.permission.WAKE_LOCK"/>
    <uses-permission android:name="android.permission.FOREGROUND_SERVICE"/>
    <uses-permission android:name="android.permission.FOREGROUND_SERVICE_DATA_SYNC"/>
    <uses-permission android:name="android.permission.POST_NOTIFICATIONS"/>
    <uses-permission android:name="android.permission.RECEIVE_BOOT_COMPLETED"/>
    <uses-permission android:name="android.permission.REQUEST_IGNORE_BATTERY_OPTIMIZATIONS"/>
    <application android:label="lanbox" android:theme="@style/Theme.Lanbox" android:usesCleartextTraffic="false">
        <activity android:name=".MainActivity" android:exported="true">
            <intent-filter>
                <action android:name="android.intent.action.MAIN"/>
                <category android:name="android.intent.category.LAUNCHER"/>
            </intent-filter>
        </activity>
    </application>
</manifest>
```

**Step 2: 生成 wrapper 并构建**

```bash
cd android && gradle wrapper --gradle-version 8.13 && ./gradlew :app:assembleDebug
```
无系统 gradle 时：先 `cd /tmp && curl -L -o g.zip https://services.gradle.org/distributions/gradle-8.13-bin.zip && unzip -q g.zip && ./gradle-8.13/bin/gradle -p android wrapper --gradle-version 8.13`。

期望：`BUILD SUCCESSFUL`，产物 `android/app/build/outputs/apk/debug/app-debug.apk`。

**Step 3: 安装到真机**

```bash
export PATH="$ANDROID_SDK/platform-tools:$PATH"
adb install -r android/app/build/outputs/apk/debug/app-debug.apk
adb shell am start -n top.glj0.lanbox/.MainActivity
```
期望：App 启动，显示空页面，`adb logcat -s lanbox` 无崩溃。

**Step 4: 提交**

```bash
git add android/settings.gradle.kts android/build.gradle.kts android/gradle.properties \
        android/app/build.gradle.kts android/app/src android/gradlew android/gradlew.bat \
        android/gradle/wrapper && git commit -m "feat(android): Gradle 工程骨架"
```

---

## Task 7: ServerService（前台服务 + 三把锁 + 通知）

**Files:**
- Create: `android/app/src/main/java/top/glj0/lanbox/Prefs.kt`、`ServerService.kt`、`AndroidLogSink.kt`
- Modify: `AndroidManifest.xml`（service 声明）

**Step 1: 写不动点（编译期测试）**

先写 `ServerService` 的公共 API 与 `Prefs`，用 `./gradlew :app:assembleDebug` 当红灯：此时 `Lanbox.start()` 调用会因 AAR 类不存在而失败（若 Task 5 已生成 AAR 则编译通过——那就在 Task 8 里用真机验证代替红灯）。

**Step 2: 实现（关键代码）**

```kotlin
// Prefs.kt
object Prefs {
    private const val NAME = "lanbox"
    fun enabled(c: Context) = c.getSharedPreferences(NAME, 0).getBoolean("server_enabled", false)
    fun setEnabled(c: Context, v: Boolean) = c.getSharedPreferences(NAME, 0).edit().putBoolean("server_enabled", v).apply()
    fun autostart(c: Context) = c.getSharedPreferences(NAME, 0).getBoolean("autostart", false)
    fun setAutostart(c: Context, v: Boolean) = c.getSharedPreferences(NAME, 0).edit().putBoolean("autostart", v).apply()
    fun port(c: Context) = 8443
}

// ServerService.kt
class ServerService : Service() {
    private var wake: PowerManager.WakeLock? = null
    private var wifi: WifiManager.WifiLock? = null
    private var multi: WifiManager.MulticastLock? = null

    override fun onCreate() {
        super.onCreate()
        startForeground(NOTIF_ID, buildNotification("正在启动…"),
            ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) { stopSelf(); return START_NOT_STICKY }
        if (Lanbox.Running()) return START_STICKY
        val pm = getSystemService(POWER_SERVICE) as PowerManager
        wake = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "lanbox:cpu").apply { setReferenceCounted(false); acquire() }
        val wm = applicationContext.getSystemService(WIFI_SERVICE) as WifiManager
        wifi = wm.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "lanbox:wifi").apply { setReferenceCounted(false); acquire() }
        multi = wm.createMulticastLock("lanbox:mdns").apply { setReferenceCounted(false); acquire() }

        Lanbox.Configure(filesDir.absolutePath, ":${Prefs.port(this)}", "lanbox-" + androidIdSuffix(), BuildConfig.VERSION_NAME)
        val err = try { Lanbox.Start(); null } catch (t: Throwable) { t.message ?: t.toString() }
        if (err != null) {
            notify(buildNotification("启动失败：$err", failed = true))
            Prefs.setEnabled(this, false); stopSelf(); return START_NOT_STICKY
        }
        notify(buildNotification("lanbox 正在运行 · ${firstAddr()}"))
        return START_STICKY
    }

    override fun onDestroy() {
        try { Lanbox.Stop() } catch (_: Throwable) {}
        multi?.release(); wifi?.release(); wake?.release()
        super.onDestroy()
    }
    // onTaskRemoved 不实现 → 不自杀
}
```

`androidIdSuffix()`：`Settings.Secure.getString(contentResolver, Settings.Secure.ANDROID_ID).takeLast(4).lowercase()`。

通知：channel `lanbox_server`（IMPORTANCE_LOW）、标题 `lanbox`、正文地址、`setOngoing(true)`、加「停止」`PendingIntent`（`ACTION_STOP` → 同时 `Prefs.setEnabled(false)`，再 `startService(intent)`）。

**Step 3: 构建 + 真机验证**

```bash
cd android && ./gradlew :app:assembleDebug && adb install -r app/build/outputs/apk/debug/app-debug.apk
adb shell am start-foreground-service -n top.glj0.lanbox/.ServerService
adb logcat -d -s lanbox | tail -20
curl -k https://10.0.0.6:8443/api/healthz     # 从 PC 验证（手机 IP 用 adb shell ip a 查）
```
期望：通知出现；`{"ok":true,...}`；日志有「mDNS 已通告」「监听」。

**Step 4: 提交**

```bash
git add android/app/src/main/java/top/glj0/lanbox android/app/src/main/AndroidManifest.xml
git commit -m "feat(android): 前台服务与保活三锁"
```

---

## Task 8: MainActivity（开关 / 地址 / 日志 / 设置）

**Files:**
- Create: `android/app/src/main/res/layout/activity_main.xml`
- Modify: `MainActivity.kt`

**Step 1: 布局**（LinearLayout 竖向：标题、Switch「启动服务」、地址 TextView + 复制按钮、「打开界面」按钮、日志 `ScrollView/TextView`、设置区：开机自启 Switch、电池优化按钮、清空数据按钮、错误提示 TextView）

**Step 2: 逻辑**

- 进入页面：`switch.isChecked = Lanbox.Running() || Prefs.enabled(this)`；若 `Prefs.enabled && !Lanbox.Running()` → 自动 `startForegroundService`
- 开关 ON → `Prefs.setEnabled(true)` + `startForegroundService`；OFF → `Prefs.setEnabled(false)` + `stopService` + `Lanbox.Stop()`
- 地址：`Lanbox.Addrs()` 每行一个；显示去掉 `https://` 的「IP:端口」；复制到剪贴板
- 每 2 秒刷新一次地址与日志（`Handler` 循环，`onPause` 停）
- 未连 WiFi 提示：`ConnectivityManager` 检查 `TRANSPORT_WIFI`
- 电池优化：`ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS`（`package:top.glj0.lanbox`）
- 清空数据：`confirmDialog` → `stopService` + `filesDir/lanbox` 递归删除 + `Prefs.setEnabled(false)`
- 首次启动若 `Lanbox.AdminPassword()` 非空，用一次性对话框展示并提示「可稍后在网页里改」

**Step 3: 构建 + 真机**

```bash
cd android && ./gradlew :app:assembleDebug && adb install -r app/build/outputs/apk/debug/app-debug.apk
adb shell am start -n top.glj0.lanbox/.MainActivity
```
期望：开关可用、地址显示、日志滚动、强杀再开自动恢复 ON。

**Step 4: 提交**

```bash
git add android/app/src/main && git commit -m "feat(android): 服务开关、地址、日志与设置界面"
```

---

## Task 9: WebViewActivity（指纹固定 + 引导登录）

**Files:**
- Create: `WebViewActivity.kt`
- Modify: `AndroidManifest.xml`

**Step 1: 实现**

```kotlin
class WebViewActivity : AppCompatActivity() {
    private lateinit var web: WebView
    private var pin: String = ""

    override fun onCreate(s: Bundle?) {
        super.onCreate(s)
        pin = Lanbox.CertPin().lowercase()
        web = WebView(this)
        setContentView(web)
        with(web.settings) {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = false
            allowContentAccess = false
            cacheMode = WebSettings.LOAD_NO_CACHE
        }
        web.webViewClient = object : WebViewClient() {
            override fun onReceivedSslError(view: WebView, handler: SslErrorHandler, error: SslError) {
                val host = error.url?.host
                val der = SslCertificate.saveState(error.certificate).getByteArray("x509-certificate")
                val ok = host == "127.0.0.1" &&
                    der != null && sha256Hex(der) == pin
                if (ok) handler.proceed() else handler.cancel()
            }
        }
        web.loadUrl(bootstrapUrl())
    }

    override fun onResume() {
        super.onResume()
        // 会话过期时重新引导（/api/me 401）
        web.evaluateJavascript(
            "fetch('/api/me',{credentials:'include'}).then(r=>r.status).catch(()=>0)"
        ) { status -> if (status == "401") web.loadUrl(bootstrapUrl()) }
    }

    private fun bootstrapUrl() =
        "https://127.0.0.1:${Prefs.port(this)}/__app/login?t=${Lanbox.LoopbackToken()}"
}
```

说明：`SslCertificate.saveState(...)["x509-certificate"]` 是拿 DER 的公开途径；若某 ROM 返回 null，退化为「仅校验 host==127.0.0.1 且运行中」并在日志记一条警告。

**Step 2: 构建 + 真机**

```bash
cd android && ./gradlew :app:assembleDebug && adb install -r app/build/outputs/apk/debug/app-debug.apk
adb shell am start -n top.glj0.lanbox/.MainActivity   # 点「打开界面」
```
期望：直接进入时间线，无登录页；`adb logcat | grep -i ssl` 无 `cancel`。

**Step 3: 提交**

```bash
git add android/app/src/main && git commit -m "feat(android): WebView 客户端（指纹固定 + 免密引导）"
```

---

## Task 10: 开机自启、Preference 收口与 manifest 完善

**Files:**
- Create: `BootReceiver.kt`
- Modify: `AndroidManifest.xml`（receiver + `foregroundServiceType`）

**Step 1: 实现**

```kotlin
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(ctx: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_BOOT_COMPLETED) return
        if (!Prefs.autostart(ctx) || !Prefs.enabled(ctx)) return
        try {
            ContextCompat.startForegroundService(ctx, Intent(ctx, ServerService::class.java))
        } catch (t: Throwable) {
            // Android 15 起 dataSync 类型禁止从 BOOT_COMPLETED 拉起；
            // 退化为「用户下次打开 App 时自动恢复」
            Log.w("lanbox", "开机自启被系统拒绝: ${t.message}")
        }
    }
}
```

```xml
<service android:name=".ServerService" android:exported="false"
         android:foregroundServiceType="dataSync"/>
<receiver android:name=".BootReceiver" android:exported="true">
    <intent-filter><action android:name="android.intent.action.BOOT_COMPLETED"/></intent-filter>
</receiver>
```

**Step 2: 构建 + 重启真机验证**

```bash
cd android && ./gradlew :app:assembleDebug && adb install -r app/build/outputs/apk/debug/app-debug.apk
adb reboot && sleep 60 && adb wait-for-device && adb shell dumpsys activity services top.glj0.lanbox | head
```
期望：Android 14 及以下自动起来；Android 15+ 若被拒，日志出现「开机自启被系统拒绝」，打开 App 后立即恢复——这是设计允许的降级。

**Step 3: 提交**

```bash
git add android/app/src/main && git commit -m "feat(android): 开机自启（含 A15 降级路径）"
```

---

## Task 11: `make android` / 签名 / 文档入口

**Files:**
- Modify: `Makefile`、`build-windows.ps1`、`.gitignore`
- Create: `android/keystore.properties.example`

**Step 1: Makefile 目标**

```make
ANDROID_SDK ?= $(HOME)/AppData/Local/Android/Sdk
ANDROID_AAR := android/app/libs/lanbox.aar

## android: 生成 AAR 并打 release APK 到 dist/lanbox-android.apk
android: $(ANDROID_AAR)
	cd android && ./gradlew :app:assembleRelease
	cp android/app/build/outputs/apk/release/app-release.apk dist/lanbox-android.apk
	@echo "已生成 dist/lanbox-android.apk"

$(ANDROID_AAR): $(shell find mobile internal -name '*.go') go.mod
	@mkdir -p android/app/libs
	gomobile bind -target=android/arm64 -androidapi 26 -javapkg=top.glj0.lanboxcore -o $@ ./mobile

## android-keystore: 生成侧载用签名（只需一次，务必备份）
android-keystore:
	keytool -genkeypair -v -keystore android/lanbox-release.jks -alias lanbox \
	  -keyalg RSA -keysize 2048 -validity 10000 -storepass lanbox123 -keypass lanbox123 \
	  -dname "CN=lanbox, O=lanbox"
	@printf 'storeFile=lanbox-release.jks\nstorePassword=lanbox123\nkeyAlias=lanbox\nkeyPassword=lanbox123\n' > android/keystore.properties
```

`app/build.gradle.kts` 追加签名（`keystore.properties` 不存在时走 debug 签名）：

```kotlin
val ksFile = rootProject.file("keystore.properties")
signingConfigs {
    if (ksFile.exists()) {
        create("release") {
            val p = java.util.Properties().apply { ksFile.inputStream().use { load(it) } }
            storeFile = rootProject.file(p.getProperty("storeFile"))
            storePassword = p.getProperty("storePassword")
            keyAlias = p.getProperty("keyAlias")
            keyPassword = p.getProperty("keyPassword")
        }
    }
}
buildTypes {
    release {
        isMinifyEnabled = false
        signingConfig = if (ksFile.exists()) signingConfigs.getByName("release")
                        else signingConfigs.getByName("debug")
    }
}
```

**Step 2: 构建验证**

```bash
make android-keystore
make android
ls -la dist/lanbox-android.apk && sha256sum dist/lanbox-android.apk
```
期望：`dist/lanbox-android.apk` 存在（约 12–20MB）。

**Step 3: 提交**

```bash
git add Makefile build-windows.ps1 android/keystore.properties.example .gitignore
git commit -m "build(android): make android 与签名目标"
```

---

## Task 12: 真机验收（8 项）

**Files:** 无（只记录结果）

逐条执行设计文档 §7 的清单，把结果写进 `docs/superpowers/plans/2026-11-26-android-app.md` 末尾的「验收记录」小节：

1. 首次安装 → 开关 ON → 通知 → 地址 → 免密进入
2. PC 浏览器信任证书 → 上传/下载/预览/Markdown
3. 状态页二维码 → 另一台手机扫码
4. 息屏 10 分钟 → PC 仍可访问
5. 强杀 → 重开自动 ON
6. 开机自启（Android 14 真机）/ A15 降级行为
7. 网页改配置生效；改 `listen` → 400 明确报错
8. 8443 被占（先 `adb shell nc -l -p 8443` 或装个占位 App）→ 开关回弹 + 错误可读

**提交**

```bash
git add docs && git commit -m "docs(android): 真机验收记录"
```

---

## Task 13: README 补 Android 章节

**Files:**
- Modify: `README.md`（在「纯 web 客户端」之后加「Android App」一节）

写清：能力、构建（`make android-keystore` → `make android`）、前置（SDK 35/NDK 27/JDK 21）、端口 8443、证书警告怎么处理、限制（仅 arm64、无自动清理、A15 开机自启降级、私有目录）。

```bash
git add README.md && git commit -m "docs(android): README 增补 Android App 章节"
```

---

## 验收标准（整个分支）

- `gofmt -l .` 空输出、`go vet ./...` 无输出、`go test ./... -count=1` 全 ok
- `make build`、`make client`、`make dist` 仍可用（桌面目标零回归）
- `make android` 产出可安装的 `dist/lanbox-android.apk`
- Task 12 的 8 项真机验收全部通过或明确记录降级行为
