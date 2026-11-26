# lanbox mDNS 发现 + 网页配置设计

日期：2026-11-26
状态：待实现
范围：新增 `internal/mdns`（mDNS 通告与浏览）；配置新增 `mdns` 组与"网页覆盖层"；网页管理员设置页；CLI `discover` 与自动发现。不改内容存储格式、不改认证语义、不改单文件下载接口。

## 1. 目标

1. **局域网可发现**：服务端以 mDNS 通告 `_lanbox._tcp.local` 与 `<hostname>.local`，用户可以直接访问 `https://lanbox.local:8443`，不必记 IP。
2. **CLI 零配置**：`lanbox-cli discover` 列出局域网实例；不给 `-s` 且无会话时自动发现；发现时按 mDNS 通告的证书指纹固定 TLS，避免 `-insecure`。
3. **所有配置项都能在网页改**：管理员在网页上读写全部配置项，含上传限额、会话有效期、监听地址、证书路径、数据目录、mDNS 参数。
4. 热更字段立即生效；必须重启的字段由网页显式"保存并重启"，且**不能把管理员锁在门外**。
5. 保持硬约束：单个静态二进制、**零第三方 Go 依赖**（mDNS 手写 DNS 报文）、`CGO_ENABLED=0`、前端零构建、CSP 不放宽。

## 2. 现有约束与关键发现

| 约束/事实 | 位置 | 影响 |
|---|---|---|
| 零第三方依赖 | `go.mod`、README | 不能用 `hashicorp/mdns`/`grandcat/zeroconf`，DNS 线格式自己写 |
| systemd `ProtectSystem=strict` + `ReadWritePaths=/var/lib/lanbox` | `deploy/lanbox.service` | `/etc/lanbox/config.json` **进程只读**，网页回写它会失败 → 覆盖值必须另存于 `data_dir` |
| 配置源是 `默认 → 文件 → 环境变量`，无运行时层 | `internal/config/config.go` | 需要新增第四层"网页覆盖"，并保留每字段来源 |
| `store.Open` 时固定 `Options`，`Manager.ttl` 也是构造参数 | `internal/store/store.go`、`internal/auth/auth.go` | 热更需要一个带锁的 setter，或换 `atomic` 持有 |
| `Server.cfg` 是值拷贝 | `internal/httpapi/server.go` | 热更后错误文案（限额）要读到新值 → 改成带锁的当前配置 |
| `Server`/`httpSrv` 都由 `main` 持有，`main` 已有信号 select | `cmd/lanbox/main.go` | 自重启最好由 `main` 主循环执行，而非 handler goroutine |
| 监听是 `ListenAndServeTLS(addr)` 隐式绑定 | `cmd/lanbox/main.go` | 要做"改错 listen 自救"，必须先显式 `net.Listen` |
| 会话只在内存，重启即失效 | `internal/auth/auth.go` | 网页重启后全员重新登录，UI 必须能等回到登录页 |
| 登录页/静态资源走 `GET /`，CSP `default-src 'self'` | `internal/httpapi/server.go`、`middleware.go` | 新页面只能外部 JS 文件，不能内联脚本 |
| 前端是手写 ES 模块 + 管理抽屉 | `web/*.js`、`web/index.html` | 设置页做成抽屉的第二个标签页，改动面最小 |
| `-s > LANBOX_URL > 会话文件` 是地址解析顺序 | `cmd/lanbox-cli/main.go` | 自动发现插在会话文件之后，作为最后一级兜底 |
| CLI 已有 `-insecure`/`-ca`，`client.Options.HTTP` 可注入 | `cmd/lanbox-cli/main.go`、`internal/client/client.go` | 证书指纹固定可用自定义 `http.Client` 实现，不用改 client 包 |

## 3. 选型对比

### 3.1 mDNS 实现

| 方案 | 评价 |
|---|---|
| **A（选定）手写最小 mDNS 通告器** | 满足零依赖承诺；代码量约 700–900 行（报文编解码 + 探测/播报 + 套接字）；风险集中在报文兼容性，用表驱动测试 + `avahi-browse` 人工验收兜底 |
| B 引入 `github.com/hashicorp/mdns` | 代码最少，但破坏 "零第三方依赖 / `go.sum` 为空 / 离线可编译" 这条写入 README 的核心承诺，且该库只管浏览、通告要另找 |
| C 依赖系统 avahi、只写 service 文件 | 零代码但要求目标机装 avahi（小主机/软路由常没有），与"单个静态二进制即可运行"冲突 |

### 3.2 网页配置落盘

| 方案 | 评价 |
|---|---|
| **A（选定）`<data_dir>/webconfig.json` 稀疏覆盖层，优先级最高** | 可写（systemd 下唯一可写目录）、可备份、可回滚（删文件即复位）、不碰用户手写的 config.json |
| B 回写 `-config` 指定的 config.json | 一份文件最直观，但 systemd 下只读失败；丢注释/排版；环境变量与文件的优先级关系变得无法解释 |
| C 只改内存 | 最简单，但重启即丢，与"通过 web 配置"名不符 |

### 3.3 需重启字段的处理

| 方案 | 评价 |
|---|---|
| **A（选定）可编辑 + 网页「保存并重启」（self-exec）** | 满足"所有配置项都能网页改"；PID 不变，systemd 的 `Restart=always` 还能兜底 exec 失败 |
| B 可编辑但不代重启，只提示 `systemctl restart` | 更安全，但要求管理员有 shell，弱化"全网页配置" |
| C 需重启字段只读展示 | 与需求冲突 |

### 3.4 配置入口位置

**选定**：把现有「用户管理」抽屉扩成 **用户 / 设置** 两个标签页，均 admin-only。不引入路由/视图切换。

## 4. `internal/mdns`：通告器

### 4.1 文件与职责

| 文件 | 职责 |
|---|---|
| `internal/mdns/wire.go` | DNS 报文编解码：`Parse([]byte) (*Message, error)`、`(*Message).Pack() ([]byte, error)`；类型 A/AAAA/PTR/SRV/TXT，QTYPE ANY；读支持名字压缩指针，写不压缩；忽略 OPT |
| `internal/mdns/responder.go` | 通告器：读循环、探测/冲突改名、播报、bye、已知答案抑制、单播/组播判定、网卡重扫 |
| `internal/mdns/browse.go` | `Browse(ctx, timeout)` → `[]Service`（CLI 用） |
| `internal/mdns/sock_linux.go` | `//go:build linux`：`SO_REUSEADDR`+`SO_REUSEPORT`、逐网卡 `IP_ADD_MEMBERSHIP`/`IPV6_JOIN_GROUP`、`IP_MULTICAST_IF`/`IPV6_MULTICAST_IF`、`SO_BINDTODEVICE` 不用 |
| `internal/mdns/sock_other.go` | `//go:build !linux`：`net.ListenMulticastUDP`（默认网卡，仅供开发机测试） |

对外 API（不依赖 `internal/config`，只用字符串/整数/`net.IP`，便于单测）：

```go
type Records struct {
    Instance string            // 展示名，如 "lanbox"
    Host     string            // 不含 .local，如 "lanbox"
    Port     int               // SRV 端口
    TXT      map[string]string // 已排序输出，v=1/name/tls/auth/fp
    Addrs    []net.IP          // 为空则用网卡地址自动收集
}

type Status struct {
    Running  bool
    Instance string   // 实际生效（冲突改名后可能带 -2 后缀）
    Host     string
    Addrs    []net.IP
    Err      string   // 空表示正常
}

func Start(r Records) (*Responder, error) // 绑定失败返回 error；调用方降级为"未通告"
func (r *Responder) Update(r2 Records) error // 停旧（发 bye）→ 起新（重新探测）
func (r *Responder) Status() Status
func (r *Responder) Close() error            // 发 TTL=0 goodbye 后关闭
```

`Responder` 内部持有可注入的 `packetConn` 接口（`ReadFrom`/`WriteTo`/`Join`/`Leave`），测试用假实现，不依赖真实组播。

### 4.2 记录

| 名字 | 类型 | 内容 |
|---|---|---|
| `_lanbox._tcp.local` | PTR | `<instance>._lanbox._tcp.local`（shared，不设 cache-flush） |
| `<instance>._lanbox._tcp.local` | SRV | `priority=0 weight=0 port=<Port> target=<host>.local`（unique，cache-flush） |
| `<instance>._lanbox._tcp.local` | TXT | 见下 |
| `<host>.local` | A / AAAA | 各网卡地址（unique，cache-flush） |
| `_services._dns-sd._udp.local` | PTR | `_lanbox._tcp.local`（让通用浏览器能列出服务类型） |

TXT 键值（顺序固定，便于测试与人工核对）：

| 键 | 值 |
|---|---|
| `v` | 协议版本，当前 `1` |
| `name` | 展示名（= `Records.Instance`） |
| `tls` | `1`（服务只跑 HTTPS） |
| `auth` | `1`（需要登录） |
| `fp` | 叶证书 DER 的 `sha256` 小写 hex（CLI 固定证书用；证书变更后需重启/重新通告才更新） |

### 4.3 行为

- **TTL**：120s；每 60s 重播一次（所有记录）。
- **启动**：对 host 与 instance 记录各发 **3 次探测**（间隔 250ms）；探测期间收到同名同类型但 rdata 不同的应答/播报 → 冲突 → host 加 `-2` 后缀（instance 若原本 = host 则同步），重试；最多 5 次，仍冲突则 `Status.Err` 置"名称冲突"，服务本身不受影响。
- **播报**：探测完成后连发 2 次（间隔 1s）。
- **bye**：`Close`/`Update`/关停时把 TTL 置 0 发一次。
- **应答**：匹配 PTR（含 `_services._dns-sd._udp.local`）、SRV、TXT、A、AAAA、ANY；未知名字不回。
- **单播/组播**：查询源端口 ≠ 5353（legacy）或 QU 位置位 → **单播**回源，回填查询 ID，不带 cache-flush 语义问题；否则组播，ID=0。
- **已知答案抑制**：查询 answer 段已有同 name/type 且 rdata 相同、TTL ≥ 本地 TTL 一半 → 跳过该条应答。
- **网卡**：仅加入 `UP && MULTICAST && !LOOPBACK` 的网卡；每 30s 重扫，新网卡加入后立即补播一次。地址从网卡收集（`Addrs` 非空时只用给定地址仍按网卡加组）。
- **失败降级**：5353 绑定失败（被 avahi/systemd-resolved 独占且无复用）或组播不可用 → `Start` 返回 error，`main` 记 warn 并继续提供服务，设置页显示"通告不可用：<原因>"。
- **报文大小**：单条响应不超过 1400 字节（自身记录远小于此），避免分片。
- **解码健壮性**：压缩指针最多 16 跳、名字总长 ≤ 255、报文 ≤ 9000 字节，异常直接丢弃。

### 4.4 与证书联动

`main` 在启动/重启时用 `tls.LoadX509KeyPair(certPath,keyPath)` 取 `Certificate[0].Certificate[0]`，算 `sha256` 填 `TXT["fp"]`；证书不可解析时不发 `fp`（CLI 此时退回 `-ca`/`-insecure`）。

## 5. `internal/mdns`：浏览（CLI 用）

```go
type Service struct {
    Instance string
    Host     string   // 不含 .local
    Addrs    []net.IP
    Port     int
    TXT      map[string]string
}

func Browse(ctx context.Context, timeout time.Duration) ([]Service, error)
```

行为：立即发一次 PTR `_lanbox._tcp.local`（组播），每 1s 重发一次直到超时（默认 2s）；收到 PTR 后对实例名发 `ANY` 查询解析 SRV/TXT，对 target 发 A/AAAA 查询；按 `Instance` 去重合并；结果按 `Instance` 升序返回。不实现缓存、不实现 `_services._dns-sd._udp.local` 浏览。

## 6. 配置模型

### 6.1 新增字段

```go
type MDNS struct {
    Enabled  bool   `json:"enabled"`
    Hostname string `json:"hostname"`
    Instance string `json:"instance"`
}
```

```json
"mdns": { "enabled": true, "hostname": "lanbox", "instance": "" }
```

| 字段 | 默认 | 校验 |
|---|---|---|
| `mdns.enabled` | `true` | — |
| `mdns.hostname` | `lanbox` | `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`（DNS label，不带 `.local`） |
| `mdns.instance` | 空（= hostname） | 空或长度 1..63 rune，不含 `\0`、换行 |

辅助方法：`Config.ListenPort() int` —— 从 `listen` 取端口，解析失败回落 `8443`（供 SRV 与日志使用）。

环境变量：`LANBOX_MDNS_ENABLED`（`1/true/yes/on` 与 `0/false/no/off`，其他值报错）、`LANBOX_MDNS_HOSTNAME`、`LANBOX_MDNS_INSTANCE`。

### 6.2 配置源优先级

```
默认值 → config.json → 环境变量 → <base data_dir>/webconfig.json
```

**`webconfig.json` 的位置定义**：`base data_dir` = 仅由"默认值 + config.json + 环境变量"解析出的 `data_dir`（即不含网页覆盖）。因此 `data_dir` 的网页覆盖**不会**改变覆盖文件自身的位置。

**已知语义（写进 README）**：改过 `data_dir` 后，旧目录里的 `webconfig.json` 仍是真源；若旧目录被删除或迁移，覆盖值整体失效并回落到文件/环境变量配置。

### 6.3 `webconfig.json` 格式

稀疏覆盖，只出现"与基线不同"的字段；用指针区分"未设置"：

```go
type Overrides struct {
    Listen          *string `json:"listen,omitempty"`
    DataDir         *string `json:"data_dir,omitempty"`
    TLS             *TLS    `json:"tls,omitempty"`
    MaxFileSizeMB   *int64  `json:"max_file_size_mb,omitempty"`
    MaxItemSizeMB   *int64  `json:"max_item_size_mb,omitempty"`
    MinFreeDiskMB   *int64  `json:"min_free_disk_mb,omitempty"`
    MaxFilesPerItem *int    `json:"max_files_per_item,omitempty"`
    MaxTextBytes    *int    `json:"max_text_bytes,omitempty"`
    SessionTTLHours *int    `json:"session_ttl_hours,omitempty"`
    MDNS            *MDNS   `json:"mdns,omitempty"`
}
```

- `tls`/`mdns` 指针整体覆盖（两个字段一起）；其余逐字段覆盖。
- 写入用"临时文件 → `fsync` → `rename`"，权限 `0600`，父目录 `0750`。文件为空（无任何覆盖）时**删除文件**，保持"删文件即复位"语义。
- 保存流程（`Live.Update`）：`desired` 与基线（不含网页覆盖）逐字段比对 → 求差集 → 校验合并后配置（`Config.Validate` + 证书可加载）→ 写覆盖 → 计算热更/需重启字段 → 调热更钩子。
- 结果 `Result{ Changed, Applied, RestartRequired []string }`，字段名用点号形式（`tls.cert_file`、`mdns.hostname`）。

### 6.4 来源标注

`Config` 增加 `Origins map[string]string`（`json:"-"`，值 `default|file|env|web`），在 `Load` 时按"字段是否在对应层出现"计算，键为点号路径（`listen`、`tls.cert_file`、`mdns.enabled`…）。文件层用 `map[string]json.RawMessage` 判定出现（含 `tls`/`mdns` 嵌套）。

**注意**：`Config` 因新增 map 变成不可用 `==` 比较，测试中改用字段断言或 `reflect.DeepEqual`。

### 6.5 `config.Live`（新类型，`internal/config/live.go`）

`Live.Update` 接收**完整**目标配置；部分更新由 HTTP 层先合并到 `Live.Current()` 再调用（见 §8.3）。

```go
type Hooks struct {
    Apply   func(prev, next Config) error // 热更：store/auth/mdns
    Restart func()                        // 请求主循环自重启，非阻塞
}

type Live struct{ ... }
func OpenLive(cfgPath string, hooks Hooks) (*Live, error) // 内部 Load
func (l *Live) Current() Config                            // 加锁读快照
func (l *Live) Origins() map[string]string
func (l *Live) Paths() (configFile, webConfig string)
func (l *Live) Update(desired Config) (Result, error)      // 校验→落盘→热更
func (l *Live) RequestRestart() bool                       // 冷却内返回 false
func (l *Live) DropWebOverride(key string) (Result, error) // 自救与"恢复默认"用
```

`Live` 只依赖 `config` 包内已有能力与两个钩子，可单测（钩子用假函数）。

## 7. 热更与重启

### 7.1 生效矩阵

| 字段 | 生效方式 | 实现 |
|---|---|---|
| `max_file_size_mb` `max_item_size_mb` `min_free_disk_mb` `max_files_per_item` `max_text_bytes` | 热更 | `store.SetLimits(Options)`（`s.mu.Lock` 下替换，所有读取走 `s.mu`） |
| `session_ttl_hours` | 热更 | `auth.Manager.SetSessionTTL(d)`（`s.mu.Lock` 下替换；续期/校验时读 `m.ttl` 都需持锁） |
| `mdns.enabled` `mdns.hostname` `mdns.instance` | 热更 | `Responder.Update(...)`；`fp`/端口不变时也重建（简单优先） |
| `listen` `data_dir` `tls.cert_file` `tls.key_file` | 需重启 | 只落盘，网页标"需重启生效" |

`httpapi.Server.cfg` 改为 `mu sync.RWMutex` + `cfg config.Config`，新增 `SetConfig`，错误文案用 `s.current()` 读取；`httpapi.Options` 增加 `Live *config.Live`（配置 API 用）与 `Restart func()`（handler 触发重启）。

### 7.2 保存并重启协议

1. `PUT /api/config` → `Live.Update(desired)`：校验失败 400，其它失败 500；成功则已落盘、热更字段已生效。
2. 响应 `200 {changed, applied, restart_required, restarting}`；若 `restart_required` 非空且请求带 `"restart": true` → 调 `Restart()`，`restarting=true`，先写出响应。
3. `POST /api/config/restart` → `202 {restarting:true}`；冷却 10s 内再次请求 → `429 restart_cooldown`。
4. `main` 主循环收到重启请求：`srv.Close()`（断 SSE）→ `httpSrv.Shutdown(5s)` → 刷日志 → `syscall.Exec(os.Executable(), os.Args, os.Environ())`（`//go:build unix`；失败则 `os.Exit(1)` 交给 systemd）。
5. `PUT` 时若改 `listen`，先用 `net.Listen("tcp", desired.Listen)` 试绑定并立即关闭，失败直接 400（缩小"改完就失联"的概率，不消除竞态）。

### 7.3 启动自愈（改错 listen 的兜底）

`main` 改为显式绑定再服务：

```go
ln, err := net.Listen("tcp", cfg.Listen)
if err != nil && live.Origins()["listen"] == "web" {
    log.Printf("warn: 网页设置的 listen=%s 绑定失败（%v），已回退到文件配置", cfg.Listen, err)
    live.DropWebOverride("listen")       // 同时清掉该覆盖
    cfg = live.Current()
    ln, err = net.Listen("tcp", cfg.Listen)
}
if err != nil { return fmt.Errorf("监听 %s 失败: %w", cfg.Listen, err) }
...
httpSrv.ServeTLS(ln, certPath, keyPath)
```

仍然失败才退出（systemd 会重试，但此时配置已回退到可用值，不会进入崩溃循环）。

### 7.4 重启后的用户体验

- 所有内存会话失效 → 网页遮罩"服务重启中…"，每 800ms 探 `GET /api/healthz`（单次超时 1s，总上限 30s），成功后 `location.reload()` 到登录页。
- CLI 现有 `401` → 退出码 4 提示重新登录，语义不变，不做特殊处理。

## 8. HTTP API

统一沿用 `errorBody{error:{code,message}}`。

### 8.1 `GET /api/healthz`（公开，无鉴权）

```json
{ "ok": true, "name": "lanbox", "version": "dev", "uptime_s": 128 }
```

`Cache-Control: no-store`；不记录访问日志（避免重启后每 800ms 一条日志）。

### 8.2 `GET /api/config`（admin）

```json
{
  "values": { "listen": ":8443", "data_dir": "./data", "tls": {...}, "max_file_size_mb": 1024,
              "max_item_size_mb": 2048, "min_free_disk_mb": 1024, "max_files_per_item": 500,
              "max_text_bytes": 65536, "session_ttl_hours": 168,
              "mdns": { "enabled": true, "hostname": "lanbox", "instance": "" } },
  "origins": { "listen": "file", "max_text_bytes": "web", "mdns.enabled": "default" },
  "editable": ["listen", "data_dir", "tls.cert_file", "tls.key_file", "max_file_size_mb",
               "max_item_size_mb", "min_free_disk_mb", "max_files_per_item", "max_text_bytes",
               "session_ttl_hours", "mdns.enabled", "mdns.hostname", "mdns.instance"],
  "restart_fields": ["listen", "data_dir", "tls.cert_file", "tls.key_file"],
  "paths": { "config_file": "/etc/lanbox/config.json",
             "web_config": "/var/lib/lanbox/webconfig.json" },
  "mdns": { "running": true, "instance": "lanbox", "host": "lanbox.local", "port": 8443,
            "addrs": ["192.168.1.9"], "error": "" }
}
```

`mdns` 段来自注入的状态函数（`httpapi.Options.MDNSStatus func() MDNSStatus`，DTO 定义在 `httpapi`，由 `main` 从 `Responder.Status()` 适配），避免 `httpapi` 依赖 `internal/mdns`。

### 8.3 `PUT /api/config`（admin）

请求：`{ "values": { …同 values，可只给部分字段… }, "restart": false }`

- 只接受 `editable` 里的键；未知键/缺组 → `400 bad_request`；**未给出的字段保持当前值**（服务端先合并到 `Live.Current()`，忽略请求中的 `origins`/`paths` 等非配置字段）
- 值先经过 `Config.Validate`（中文错误原样回传，`400 invalid_config`）
- 若改 `tls.*` 且可加载校验失败 → `400 invalid_tls`
- 若改 `listen` 且试绑定失败 → `400 listen_unavailable`
- 响应：`200 { "changed": ["max_text_bytes"], "applied": ["max_text_bytes"], "restart_required": [], "restarting": false }`
- `restart: true` 是给 curl/脚本的"一次保存并重启"；网页端不走它（见 §9：先保存，用户确认后再 `POST /api/config/restart`）

### 8.4 `POST /api/config/restart`（admin）

`202 {"restarting": true}`；冷却内 `429 restart_cooldown`；日志记录操作者与来源 IP。

## 9. 网页端改动

| 文件 | 改动 |
|---|---|
| `web/index.html` | 抽屉内加标签页头「用户 / 设置」，新增设置表单容器（四组）与重启遮罩节点 |
| `web/admin.js` | 改成抽屉壳 + 标签切换；用户管理逻辑原样保留 |
| `web/settings.js` | 新增：取配置、渲染四组表单、来源徽标、需重启徽标、保存、二次确认、重启遮罩轮询 `healthz` |
| `web/api.js` | 新增 `config()`、`saveConfig(values, restart)`、`restart()`、`healthz()` |
| `web/style.css` | 标签页、表单行、徽标、遮罩样式 |

设置页分组与字段：

| 组 | 字段 |
|---|---|
| 上传与存储 | `max_file_size_mb` `max_item_size_mb` `min_free_disk_mb` `max_files_per_item` `max_text_bytes` |
| 会话 | `session_ttl_hours` |
| 网络与证书 | `listen` `tls.cert_file` `tls.key_file` `data_dir` |
| 局域网发现 | `mdns.enabled` `mdns.hostname` `mdns.instance` |

行为：

- 每行：标签 + 输入框 + 来源徽标（默认/配置文件/环境变量/网页）+「需重启」徽标；只读信息（`paths`、`mdns` 当前通告名/地址/错误）放在组内灰色说明。
- 「保存」：客户端先做最小校验（正整数、hostname 正则）；提交 `restart:false`；成功后用响应里的 `applied` 刷新来源徽标。
- 响应含 `restart_required` → 提示"以下项需重启生效：…"，给出「立即重启」按钮；点按二次确认（提示"约 1–3 秒不可用，所有人需重新登录；若改了监听地址可能需在新地址访问"）。
- 重启后显示遮罩；`healthz` 恢复后 `location.reload()`。
- 「恢复默认」：把某字段改回基线值后保存即可（服务端差集自动删掉覆盖）；页面上显示基线值作为 placeholder 提示。

## 10. CLI 改动

| 文件 | 改动 |
|---|---|
| `internal/mdns/browse.go` | 新增浏览（见 §5） |
| `cmd/lanbox-cli/cmd_discover.go` | 新增 `discover` 子命令 |
| `cmd/lanbox-cli/render.go` | 新增 `renderServices`（表格）与 JSON 复用 |
| `cmd/lanbox-cli/main.go` | `resolveServer` 增加自动发现；`globals` 增加 `pinFP`；`httpClient` 支持指纹固定；`usage` 补一行 |

命令面：

```
discover [-timeout 2s] [-json]   列出局域网内的 lanbox 实例
```

表格列：`实例 / 地址 / 版本 / 主机名 / 证书指纹`（地址取 IPv4 优先，`https://<ip>:<port>`；版本取 TXT `v`；无 `fp` 显示 `-`）。

自动发现（`resolveServer` 由 `-s` → `LANBOX_URL` → 会话文件之后多一级）：

- 1 个实例 → 用它（`https://` + IPv4 优先地址 + SRV 端口），若 TXT 有 `fp` 则记入 `g.pinFP`
- 多个实例 → `errUsage`，错误信息列出 `实例 @ 地址`
- 0 个 → 保持原文错误，追加提示"（局域网内未发现 lanbox）"

证书固定（`httpClient`）：

- `-insecure` → 维持现状，跳过校验（优先）
- `-ca` 非空 → 用 CA 池（优先于指纹）
- 否则若 `g.pinFP != ""` → `InsecureSkipVerify: true` + `VerifyPeerCertificate` 里对 `rawCerts[0]` 求 `sha256`，与 `fp` 用 `subtle.ConstantTimeCompare` 比对，成功后打印 `已按 mDNS 通告固定证书指纹 sha256:<前16位>`
- 以上都没有 → 现状（系统信任）

会话文件格式不变，仍按 URL 存；自动发现得到的 URL 与已存 URL 不一致时沿用现有"会话属于 X，请先 login -s X"提示。

## 11. 依赖注入与文件清单

新增：

- `internal/mdns/{wire,responder,browse,sock_linux,sock_other}.go` + 测试
- `internal/config/{overrides,live,origins}.go` + 测试
- `internal/httpapi/config_handlers.go` + 测试
- `cmd/lanbox-cli/cmd_discover.go` + 测试
- `web/settings.js`
- `cmd/lanbox/restart_unix.go`（`//go:build unix`）+ `restart_other.go`（返回错误）

修改：

- `internal/config/config.go`（`MDNS`、`Origins`、env、校验、`ListenPort()`）
- `internal/store/{store,items,files}.go`（`SetLimits` 与读取加锁）
- `internal/auth/auth.go`（`SetSessionTTL`）
- `internal/httpapi/{server,middleware,helpers}.go`（`Live`/`Restart`/`MDNSStatus`/`SetConfig`/`healthz`；`healthz` 不进访问日志）
- `cmd/lanbox/main.go`（`config.OpenLive`、显式 `net.Listen`、`mdns.Start`、重启分支、启动自愈、`printAccessURLs` 加一行 `.local` 地址）
- `cmd/lanbox-cli/{main,render}.go`、`web/{index.html,admin.js,api.js,style.css}`
- `README.md`、`Makefile`（无新目标，`make check` 覆盖）

边界：`internal/mdns` 不 import `internal/config`/`httpapi`；`internal/httpapi` 不拼磁盘路径；`internal/config` 不 import 其它内部包（`Live` 的钩子由 `main` 注入）。

## 12. 测试策略

| 层 | 用例 |
|---|---|
| `mdns/wire` | 编解码往返（A/AAAA/PTR/SRV/TXT/ANY）；名字压缩指针（含跳转、循环、超长）；截断包；未知类型忽略；QTYPE 大小写不敏感 |
| `mdns/responder` | 假 socket：PTR/SRV/TXT/A 命中与不命中；QU 位与源端口≠5353 → 单播且回填 ID；已知答案抑制；探测冲突 → 改名（含 5 次失败）；`Update` 发 bye；`Close` 发 TTL=0；坏包不 panic |
| `mdns/browse` | 假 socket：PTR→SRV/TXT/A 聚合、去重、超时返回部分结果、按实例排序 |
| `config` | 四级优先级；`Origins` 判定（含嵌套）；覆盖差集与"恢复默认"删键；空覆盖删文件；原子写；`data_dir` 覆盖不改变覆盖文件位置；`mdns` 校验与环境变量布尔解析 |
| `config.Live` | 校验失败不落盘；热更钩子收到正确 prev/next；`RestartRequired` 计算；`DropWebOverride("listen")`；冷却期 `RequestRestart` 返回 false |
| `httpapi` | `config` 读写 admin-only（非管理员 403）；未知键/非法值 400；`restart:true` 触发注入的假 `Restart`；429 冷却；`healthz` 无需登录且 `no-store`；限额热更后错误文案用新值 |
| `store`/`auth` | `SetLimits`/`SetSessionTTL` 并发读写（`-race`）生效 |
| CLI | `discover` 表格/JSON 渲染；0/1/多实例决策；指纹固定握手成功/失败（`httptest.NewTLSServer` + 真实证书指纹） |
| 人工验收 | `avahi-browse -rt _lanbox._tcp` 与 macOS `dns-sd -B _lanbox._tcp` 能看到并解析；手机浏览器访问 `https://lanbox.local:8443`（自签证书警告照旧） |

CI 不做真实组播（沙箱常禁用），全部用假 socket 覆盖逻辑。

## 13. 明确不做

- 网页端浏览/管理其它 lanbox 实例、主备同步、多实例聚合视图
- mDNS 代理/转发、`_https._tcp` 泛注册、DNS-SD 的 TXT 更新通知
- mDNS 的 IPv6-only 链路特化（AAAA 照发，不做 `ff02::fb` 之外的策略）
- 配置项级别的并发编辑冲突检测（最后一次保存生效，`updated_at` 只写日志）
- 配置版本历史/审计表（只写 stdout 日志）
- 优雅的零丢包重启（会话与 SSE 会断，设计上接受）
- Windows 目标（`make dist` 仍只出 linux）
- 运行时切换数据目录的在线迁移

## 14. 兼容与迁移

- 旧 `config.json` 零迁移：新字段有默认值；`webconfig.json` 不存在时行为与今天完全一致。
- 端口/路径语义不变；`tls` 逻辑不变（自签证书仍自动生成）。
- 新增环境变量均带 `LANBOX_` 前缀，不与现有冲突。
- 新增公开接口 `GET /api/healthz`（不含敏感信息），便于监控与重启轮询。
- CLI 旧用法（显式 `-s`）行为不变；新增 `discover` 子命令与"最后一级兜底"。
- `Config` 增加 `Origins` 后不再可 `==` 比较（仅影响测试写法）。

## 15. 风险

| 风险 | 缓解 |
|---|---|
| 手写 DNS 报文与严格客户端（macOS/iOS）不兼容 | 表驱动线格式测试 + `dns-sd`/`avahi-browse` 人工验收列入清单；应答只发必要记录、不压缩、不超过 1400 字节 |
| 5353 被 avahi/systemd-resolved 占用 | `SO_REUSEADDR`+`SO_REUSEPORT`；绑定失败降级为"未通告"，服务不受影响，设置页可见原因 |
| 多虚拟网卡（docker0/veth）通告无关地址 | 先只过滤 loopback/down/no-multicast；若实测噪音大再加 `mdns.ifaces` 白名单（不做在本次） |
| 网页改 `listen` 后失联 | 保存前试绑定；启动时绑定失败自动回退文件配置并清覆盖；UI 明确警告；README 给救援步骤（编辑/删除 `data/webconfig.json`） |
| 自重启进入崩溃循环 | exec 前 `Shutdown` 关闭监听（避免地址占用）；listen 覆盖绑定失败已在启动时回退，不会循环 |
| 重启清空所有人会话 | UI 遮罩 + 文案说明；README 记录 |
| `SetLimits` 与上传并发读写 `opts` 竞态 | 所有 `opts` 读取纳入 `s.mu`，`-race` 测试覆盖 |
| 证书指纹变化（换证书）后 CLI 固定失败 | `fp` 只在启动/重启时计算；换证书必然重启；错误信息提示"证书已变更，请重新 discover 或使用 -ca" |

## 16. 验收清单

1. 启动后 `avahi-browse -rt _lanbox._tcp` 显示实例、端口、TXT（含 `v/name/tls/auth/fp`），并能解析到主机 IPv4。
2. 手机/另一台电脑访问 `https://lanbox.local:8443` 能打开登录页（自签证书警告属于预期）。
3. `lanbox-cli discover` 列出该实例；`-json` 输出可被 `jq` 解析。
4. 无会话、无 `-s` 时 `lanbox-cli ls` 自动发现并连通（不传 `-insecure`）；两个实例时提示候选并要求 `-s`。
5. 改 `mdns.hostname` 保存 → 无需重启，`dns-sd -B` 中旧名发 bye、新名出现；`mdns.enabled=false` → 记录消失。
6. 网页设置页显示每个字段来源；把 `max_text_bytes` 改大保存 → 立刻生效（发一条超过旧上限的文字成功）。
7. 改 `max_files_per_item` 保存 → 无需重启即按新值拒绝/接受。
8. 改 `listen` 保存 → 提示需重启 → 「立即重启」→ 遮罩自动恢复并跳登录页；在新端口能访问。
9. 把 `listen` 改成 `:1`（不可绑定）→ 保存阶段直接 400；把 `listen` 改成合法但被占用的端口 → 保存成功并重启后启动自愈回退，日志有 warn，原端口可访问。
10. 删除 `data/webconfig.json` 后重启 → 回到 config.json/环境变量配置。
11. 非管理员 `GET/PUT /api/config`、`POST /api/config/restart` → 403；连点重启 → 429。
12. `make check` 全绿（gofmt + vet + 全部测试）。

## 17. 实现注记（落地时与原设计的偏差）

两处按实际可用性做了调整，均已写入 README：

1. **自动发现只在 `login` 生效**：`-s` / `LANBOX_URL` / 会话文件都没有时，只有 `login` 会做 mDNS 发现；其它命令仍然要求先登录（否则发现到地址也会 401，且每次多 2 秒等待）。`discover` 子命令可随时列出实例。
2. **证书指纹随会话保存**：原设计写"会话文件格式不变"，但登录时固定的指纹若不持久化，下一条命令仍会因自签证书失败。因此会话文件新增可选字段 `pin_fp`（`omitempty`，旧文件不受影响），后续命令自动沿用；`-ca`/`-insecure` 优先级更高，证书更换后需重新 `login`。
3. **列表地址选择**：多网卡（含 docker0）时 `discover` 表格展示字面地址，`login` 自动发现时对候选 IPv4 做 300ms TCP 连通性探测，挑真正可达的地址。
