# lanbox

一台同时接入内网与外网的 Linux 小主机上的**文件与消息共享服务**：外网电脑打开网页发布文字和文件，内网电脑打开同一个网页取走，反之亦然。两端权限对等，都能发布、查看、下载、标记已取走、删除。

- 单个静态二进制，**零第三方依赖**，无需 Docker、数据库、Node
- HTTPS 单端口，未配置证书时自动生成自签证书
- 每条内容标注**发布者与来源 IP**
- 附件流式落盘（内存占用与文件大小无关），支持 Range 断点下载
- 实时刷新（SSE），另一端发布后 1 秒内出现
- 独立账号 + 会话过期 + 登录限速

## 快速开始

```bash
# 1. 编译本机架构（需要 Go 1.24+，仅标准库）
make build          # 等价于 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=..." -o lanbox ./cmd/lanbox

# 2. 直接运行（默认监听 :8443，数据在 ./data）
./lanbox

# 3. 首次启动会打印 admin 的随机密码，请立即保存
#    然后浏览器打开 https://<小主机IP>:8443
```

## 交叉编译（小主机常见是 ARM）

```bash
make dist     # 产出 dist/ 下四个静态二进制 + SHA256SUMS
```

| 产物 | 适用 |
|---|---|
| `dist/lanbox-linux-amd64` | x86_64（大多数软路由、N100 小主机、虚拟机） |
| `dist/lanbox-linux-arm64` | ARM64（树莓派 3/4/5 64 位系统、RK3399 等） |
| `dist/lanbox-linux-armv7` | ARMv7 32 位（树莓派 2/3 32 位系统、N1、老路由器） |
| `dist/lanbox-linux-386` | x86 32 位（老 Atom 机器） |

查目标机架构：`uname -m` → `x86_64` / `aarch64` / `armv7l` / `i686`。

其它常用目标：

| 命令 | 作用 |
|---|---|
| `make build` | 只编本机架构到 `./lanbox` |
| `make test` | 跑全部测试 |
| `make check` | 提交前自检：gofmt + go vet + test |
| `make run` | 前台运行（读 `./config.json`） |
| `make install` | 安装到 `/usr/local/bin`（`PREFIX=` 可改） |
| `make clean` | 清掉 `dist/` 与 `./lanbox` |
| `make help` | 列出全部目标 |

`make dist` 会把当前 git 提交号注入版本：`./lanbox -version` 可查看。

浏览器首次访问自签证书会警告，选择"继续访问"；需要长期免警告见下方"信任自签证书"。

## 配置

启动时读取 `-config` 指定的文件（默认 `config.json`），**所有字段可省略**：

```json
{
  "listen": ":8443",
  "data_dir": "./data",
  "tls": { "cert_file": "", "key_file": "" },
  "max_file_size_mb": 1024,
  "min_free_disk_mb": 1024,
  "max_text_bytes": 65536,
  "session_ttl_hours": 168,
  "mdns": { "enabled": true, "hostname": "lanbox", "instance": "" }
}
```

| 字段 | 默认 | 说明 |
|---|---|---|
| `listen` | `:8443` | 监听地址。只对某一网卡开放可写 `192.168.1.9:8443` |
| `data_dir` | `./data` | 数据目录（内容、用户库、证书） |
| `tls.cert_file` / `tls.key_file` | 空 | 同时填写则使用你自己的证书；留空则自动生成自签证书 |
| `max_file_size_mb` | 1024 | 单文件上限，超出返回 413 |
| `min_free_disk_mb` | 1024 | 剩余磁盘保护线，低于此值拒绝上传（507） |
| `max_text_bytes` | 65536 | 单条文字上限 |
| `session_ttl_hours` | 168 | 会话有效期（滑动续期） |
| `mdns.enabled` | `true` | 是否在局域网内以 mDNS 通告 |
| `mdns.hostname` | `lanbox` | 通告为 `https://<hostname>.local:<端口>` |
| `mdns.instance` | 空（同 hostname） | 服务实例名（`_lanbox._tcp` 的 PTR 名称） |

也可用环境变量覆盖：`LANBOX_LISTEN`、`LANBOX_DATA_DIR`、`LANBOX_TLS_CERT_FILE`、`LANBOX_TLS_KEY_FILE`、`LANBOX_MAX_FILE_SIZE_MB`、`LANBOX_MAX_ITEM_SIZE_MB`、`LANBOX_MIN_FREE_DISK_MB`、`LANBOX_MAX_FILES_PER_ITEM`、`LANBOX_MAX_TEXT_BYTES`、`LANBOX_SESSION_TTL_HOURS`、`LANBOX_MDNS_ENABLED`、`LANBOX_MDNS_HOSTNAME`、`LANBOX_MDNS_INSTANCE`。

### 网页配置与覆盖文件

管理员可在网页「管理 → 设置」里改**全部**配置项。保存后的值写入 `<data_dir>/webconfig.json`（稀疏覆盖，权限 `0600`），
优先级为：`默认值 → config.json → 环境变量 → webconfig.json`。

- 删除 `webconfig.json` 即回到 `config.json` + 环境变量
- 网页上每项都标注来源（默认 / 配置文件 / 环境变量 / 网页设置）
- `max_*`、`session_ttl_hours`、`mdns.*` 立即生效
- `listen`、`data_dir`、`tls.*` 需重启；网页会提示并提供「立即重启」（`syscall.Exec`，PID 不变，所有人需重新登录）
- 覆盖文件固定在"文件/环境变量解析出的 `data_dir`"下：改过 `data_dir` 后，旧目录里的 `webconfig.json` 仍是真源
- **救援**：把 `listen` 改成不可用的值（或端口被占用）时，服务启动会检测到并自动忽略该覆盖、回退到文件配置，日志有 `warn: 网页设置的 listen=... 绑定失败`；也可直接编辑/删除 `<data_dir>/webconfig.json`
- 运行时健康检查：`GET /api/healthz`（无需登录，返回 `ok/name/version/uptime_s`）

## 局域网发现（mDNS）

服务启动后会在局域网内通告（无需任何额外依赖，端口 UDP 5353）：

- 服务类型：`_lanbox._tcp`（实例名 = `mdns.instance`，留空时用主机名；端口 = 监听端口）
- 主机名：`<mdns.hostname>.local`
- TXT：`v=1`、`name=<实例名>`、`tls=1`、`auth=1`、`fp=<叶证书 SHA-256>`

因此可以直接访问 `https://lanbox.local:8443`（自签证书仍需首次信任）。手机、macOS、Linux 均可解析 `.local`。

CLI：

```bash
lanbox-cli discover                  # 列出局域网实例（实例/地址/版本/主机名/证书指纹）
lanbox-cli discover -json            # 机器可读
LANBOX_PASSWORD=... lanbox-cli login # 不给 -s 时自动发现（唯一实例）
```

自动发现时会按 TXT 里的证书指纹**固定 TLS**（比 `-insecure` 安全），指纹随会话文件保存，后续命令无需再发现；
`-ca` 与 `-insecure` 优先级更高。多个实例时会列出候选并要求用 `-s` 指定。

排查：

- `avahi-browse -rt _lanbox._tcp`（Linux）或 `dns-sd -B _lanbox._tcp`（macOS）应能看到实例
- 看不到：确认两台机器在同一网段、未禁用组播、防火墙未拦 UDP 5353
- 与 avahi/systemd-resolved 共存：使用 `SO_REUSEADDR` + `SO_REUSEPORT`，冲突时回落 `SO_REUSEADDR`；绑定失败只降级为"不通告"，不影响主服务，设置页会显示原因

## 账号

- 首次启动自动创建 `admin`，随机 16 位密码打印到 stdout（systemd 下见 `journalctl -u lanbox`）
- 忘记密码：`./lanbox -reset-admin-password`（会打印新的随机密码）
- 管理员可在网页"用户管理"里新增/删除用户、重置密码
- 所有用户权限对等（都能发布/下载/删除）；管理员只是多了用户管理

## 数据与备份

```
data/
├── items/<内容ID>/meta.json    # 元数据
├── items/<内容ID>/files/…       # 附件
├── users.json                  # 账号（PBKDF2 哈希）
└── tls/{cert.pem,key.pem}      # 自签证书
```

备份整个 `data/` 即可；删除某条内容 = 删除对应目录（服务运行时请通过网页删除）。

## 以 systemd 常驻

```bash
sudo useradd --system --home /opt/lanbox --shell /usr/sbin/nologin lanbox
sudo mkdir -p /opt/lanbox /etc/lanbox /var/lib/lanbox
sudo cp lanbox /opt/lanbox/
sudo cp deploy/lanbox.service /etc/systemd/system/
sudo chown -R lanbox:lanbox /opt/lanbox /var/lib/lanbox

sudo tee /etc/lanbox/config.json >/dev/null <<'EOF'
{
  "listen": ":8443",
  "data_dir": "/var/lib/lanbox"
}
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now lanbox
sudo journalctl -u lanbox -n 30      # 这里能看到首启生成的 admin 密码
```

## 信任自签证书

浏览器：访问一次 → "高级" → "继续前往"。

Linux 客户端：
```bash
sudo cp data/tls/cert.pem /usr/local/share/ca-certificates/lanbox.crt
sudo update-ca-certificates
```

Windows：把 `cert.pem` 重命名为 `cert.crt` 后"导入到受信任的根证书颁发机构"。

## 安全提示

- 只应暴露在你控制的网络内；外网侧建议用路由器端口映射到固定端口并保持系统更新
- 真实证书：`tls.cert_file` / `tls.key_file` 指向你的证书即可
- 会话令牌仅存内存，重启服务后需重新登录
- 上传的 HTML/SVG 一律以 `attachment` + `application/octet-stream` 下载，不会在浏览器中执行
- 来源 IP 取 TCP 直连地址，不信任 `X-Forwarded-For`（避免伪造来源标注）

## 开发

```bash
go test ./...      # 全部测试
go vet ./...       # 静态检查
go build ./...     # 编译检查
```

手动验收清单（前端无 E2E，改前端后逐项过一遍）：

1. 未登录访问首页 → 显示登录表单；错误密码 → 提示"用户名或密码错误"
2. 登录后右上角显示"实时已连接"
3. 只发文字 → 立刻出现在顶部，显示用户名、来源 IP、时间
4. 选多个文件发送 → 进度条走完，卡片出现对应附件行
5. 拖入文件、Ctrl+V 粘贴截图 → 进入待发送列表
6. 下载附件 → 内容与上传一致；浏览器支持断点续传（Range 206）
7. 标记已取走 → 卡片变灰并出现标签；"未取走"筛选下消失
8. 删除单个附件 / 删除整条 → 二次确认后消失
9. 两个窗口同时在线 → 一边发布，另一边 1 秒内出现；删除同样实时消失
10. 管理员可新增用户、重置密码、删除用户；非管理员看不到"用户管理"，且 `GET /api/users` 返回 403
11. 修改自己的密码 → 当前会话立即失效，需重新登录
12. `avahi-browse -rt _lanbox._tcp` 能看到实例与 TXT；`https://<hostname>.local:<端口>` 可打开登录页
13. 网页「设置」改 `mdns.hostname` 保存 → 无需重启，旧名发 goodbye、新名出现；关闭 `mdns.enabled` 后记录消失
14. 网页「设置」改 `max_text_bytes` → 立即生效（发一条超过旧上限的文字应成功/失败相应变化）
15. 网页「设置」改 `listen` → 提示需重启 →「立即重启」→ 在新端口可访问、页面自动回到登录页
16. 把 `listen` 改成被占用端口 → 重启后自动回退到文件配置，日志有 `warn`，原端口仍可访问
17. `lanbox-cli discover` 列表正确；`lanbox-cli login`（不给 `-s`）自动发现并按指纹固定证书，随后 `ls` 正常
18. 删除 `data/webconfig.json` 后重启 → 回到 `config.json` / 环境变量配置

## 不做的事（有意为之）

分片上传/断点续传、zip 打包下载、回收站与自动过期、频道与标签、全文索引、富文本渲染、多语言。
