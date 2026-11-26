# CLI `status` 子命令设计

日期：2026-11-26
状态：已确认（B 方案：会话 + 服务器健康 + 内容统计；一部分字段仅 admin）

## 目标

`lanbox-cli status` 一条命令看清"我在哪、服务还在不在、盒子装了多少东西"。
不新增登录步骤、不轮询、不改动既有接口语义。

非目标：

- 不做持续刷新/`watch`（提醒用 `ls --todo` 或网页 SSE）。
- 不替代 `whoami`（`status` 需要登录且有服务端统计，`whoami` 只验会话）。
- 不做运维仪表盘（配置编辑仍在网页设置页）。

## API：`GET /api/stats`

- 鉴权：`withAuth`，所有已登录用户可调。
- 缓存：`Cache-Control: no-store`（与 `healthz` 一致，数据实时）。
- 口径：**全站**内容，不分发布者（与 `ls` 相同）。

响应：

```json
{
  "version": "v0.1.0",
  "uptime_s": 12340,
  "items": { "total": 12, "todo": 3, "taken": 9 },
  "files": { "count": 40, "bytes": 12345678 },
  "admin": {
    "users": 2,
    "disk": { "free_bytes": 34359738368 },
    "items_dir": "/var/lib/lanbox/items",
    "listen": ":8443",
    "mdns": {
      "running": true,
      "instance": "lanbox",
      "host": "lanbox.local",
      "port": 8443,
      "addrs": ["192.168.1.9"],
      "error": ""
    }
  }
}
```

字段规则：

| 字段 | 可见性 | 说明 |
|---|---|---|
| `version` / `uptime_s` | 所有人 | 与 `/api/healthz` 同源（同一 `Server` 字段） |
| `items.total/todo/taken` | 所有人 | 未取走 = `!taken`，与 `ls --todo` 一致 |
| `files.count/bytes` | 所有人 | 附件个数与 `File.Size` 之和 |
| `admin` | **仅管理员** | 非管理员的响应里**没有** `admin` 键（不是空对象） |
| `admin.users` | 仅管理员 | 用户总数（`auth.List()` 长度） |
| `admin.disk.free_bytes` | 仅管理员 | items 目录所在文件系统可用字节 |
| `admin.items_dir` | 仅管理员 | 实际生效的附件存储路径 `<data_dir>/items` |
| `admin.listen` | 仅管理员 | 当前生效监听地址 |
| `admin.mdns` | 仅管理员 | 复用 `httpapi.MDNSStatus`；未配置 mDNS 回调时省略 |

错误处理：

- 未登录：401（`unauthorized`）。
- 取磁盘剩余失败：不使接口失败，`disk.free_bytes` 省略、`disk.error` 给文本。
- `mdns` 回调为空（测试/裁剪构建）：`admin.mdns` 省略。

## store 侧

- `(*Store).Stats() Stats`：一次 `RLock` 遍历内存索引，O(条数)，无磁盘 IO。
- `(*Store).FreeBytes() (int64, error)`：复用 `Options.FreeSpace`（默认 `statfs`），
  对 `itemsRoot` 求值；热更限额不影响它。

## client 侧

- `(*Client).Stats(ctx) (*Stats, error)`，DTO 字段与上表同名同义；
  `Admin *StatsAdmin` 用指针 + `omitempty` 表示"缺席"。

## CLI 输出

```
服务   https://192.168.1.9:8443   可达 · v0.1.0 · 运行 3h12m
身份   admin（管理员）
内容   12 条（未取走 3 / 已取走 9）
附件   40 个 / 11.8 MiB
磁盘   剩余 32.4 GiB
用户   2 个 · 监听 :8443 · 存储 /var/lib/lanbox/items
mDNS   lanbox.local:8443（已运行）
```

- 非管理员只输出前四行；磁盘/用户/mDNS 三行整行不出现。
- 管理员用 `admin != nil` 判定，不额外请求 `/api/me`。
- `uptime_s` 渲染为 `NdNh` / `NhNm` / `Nm` / `Ns`；
  磁盘、附件字节用既有 `fmtBytes`。
- mDNS 行：运行中且有 host 时 `host:port（已运行）`；未运行但有错误时
  `未运行：<error>`；其余 `未运行`。
- 磁盘取失败时该行输出 `磁盘  剩余未知（<error>）`。
- `-json`：输出服务端结构 + 顶层 `url`（便于脚本拼接），非管理员没有 `admin` 键。
- 退出码沿用约定：未登录 4、网络/证书 3、用法 2、其它 1。

## 测试

- store：空库全 0；文本条/带附件条/已取走条的总数、todo、附件数与字节；
  `FreeBytes` 使用注入的 `FreeSpace`。
- httpapi：未登录 401；普通用户无 `admin` 键；管理员含 `users`/`disk`/`items_dir`/`listen`；
  磁盘错误时 `free_bytes` 缺席且 `error` 非空。
- client：解析管理员与非管理员响应（`Admin == nil`）。
- CLI：管理员文本含磁盘/用户/mDNS；非管理员不含；`-json` 含 `url` 与统计；
  未登录返回 `errAuth`。

## 改动清单

- `internal/store/stats.go`、`internal/store/stats_test.go`
- `internal/httpapi/stats_handlers.go`、`internal/httpapi/server.go`、`internal/httpapi/api_test.go`
- `internal/client/stats.go`、`internal/client/client_test.go`
- `cmd/lanbox-cli/cmd_status.go`、`cmd/lanbox-cli/cmd_status_test.go`、`cmd/lanbox-cli/main.go`
- `README.md`（CLI 命令示例与接口说明）
