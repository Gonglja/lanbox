# 实现计划：CLI `status`

对应 spec：`docs/superpowers/specs/2026-11-26-cli-status-design.md`

## 任务

1. **store 统计**（`internal/store/stats.go`）
   - 新增 `Stats` 类型与 `(*Store).Stats()`；`(*Store).FreeBytes()`。
   - 测试：空库、文本条、附件条、已取走条；注入 `FreeSpace`。
2. **HTTP 接口**（`internal/httpapi/stats_handlers.go`）
   - `handleStats`：公共字段 + `admin` 块（`userFrom` 判管理员）。
   - 路由 `GET /api/stats` 走 `withAuth`；`Cache-Control: no-store`。
   - 测试：401、普通用户无 `admin`、管理员字段齐全、磁盘错误降级。
3. **client**（`internal/client/stats.go`）
   - `Client.Stats(ctx)` 与 DTO。
   - 测试：管理员/普通用户响应解析。
4. **CLI**（`cmd/lanbox-cli/cmd_status.go`）
   - `cmdStatus`：解析参数（无位置参数）、`newClient`、`Stats`、文本/JSON 输出。
   - `renderStatus`/`fmtUptime`；非管理员省略三行。
   - `main.go`：`case "status"`、usage 行。
   - 测试：管理员/非管理员文本、`-json` 含 `url`、未登录。
5. **文档与自检**
   - README：CLI 示例、`/api/stats` 说明。
   - `make check`（gofmt + vet + test）。

## 验收

- `lanbox-cli status` 在管理员下显示 7 行，普通用户 4 行。
- `lanbox-cli status -json` 输出可用 `jq` 解析，管理员含 `admin.users/items_dir`。
- `make check` 通过。
