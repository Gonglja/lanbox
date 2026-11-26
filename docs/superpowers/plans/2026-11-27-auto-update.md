# 自动更新实现计划

对应设计：`docs/superpowers/specs/2026-11-27-auto-update-design.md`
分支：`feat/auto-update`（worktree `D:/03.Project/lanbox-auto-update`）

每个任务都用 `go test ./<包> -run <名字> -count=1` 自证；全部完成后跑 `make check`。

## 1. `internal/updater`：版本比较

- [ ] `version.go`：`parseVersion`（去 v、核心 1-3 段补零、`-pre` 分段数值/字典序、忽略 `+build`）、`compare`、`normalize`（输出展示用 `1.2.4`）。
- [ ] `version_test.go` 表驱动：`1.9.9<1.10.0`、`1.2.3==v1.2.3`、`1.2.3-rc1<1.2.3`、`rc1<rc2`、`dev`/空/非法 < 一切。
- 验证：`go test ./internal/updater -run Version`

## 2. `internal/updater`：检查更新

- [ ] `updater.go`：`Options`/`Release`/`ErrUpToDate`/`New`。
- [ ] github 模式：`GET {base}/releases/latest/download/SHA256SUMS`，`CheckRedirect` 里记最终 URL，解析 `releases/download/v<x.y.z>/`；按 asset 名从清单取 sha256。
- [ ] manifest 模式：`GET {url}` 解析 `latest.json`，缺本平台资产返回 `ErrNoAsset`。
- [ ] token → `Authorization: Bearer`；`Proxy: http.ProxyFromEnvironment`。
- [ ] 比较版本，`<=` 返回 `ErrUpToDate`（附远端 Release 供 UI 显示）。
- [ ] `updater_test.go`：httptest 假源（302 → SHA256SUMS → 资产）；表驱动覆盖：版本解析、清单缺条目、坏版本号、token 头、ErrUpToDate。
- 验证：`go test ./internal/updater -run Check`

## 3. `internal/updater`：下载与校验

- [ ] `Download(ctx, dir, r)`：临时文件 `.lanbox-update-<ver>.tmp` 落在 `dir`；边下边算 sha256；比对 size；校验 SHA256；魔数（ELF/MZ）；失败删临时文件。
- [ ] 磁盘空间预检（`statfs`/`GetDiskFreeSpaceEx` 已有工具？没有就 `syscall.Statfs` + `windows.GetDiskFreeSpaceEx`），要求 ≥ 2× 大小。
- [ ] 测试：正常、改一字节、size 不符、HTML 伪装、ctx 取消。

## 4. `internal/updater`：替换与回滚

- [ ] `replace_unix.go` / `replace_windows.go`：`Stage`（rename exe→exe.old、tmp→exe、失败回滚）、`Rollback`（exe→exe.failed、exe.old→exe）、`CleanupTemps(exeDir)`、`BackupExists`。
- [ ] 测试（unix）：替换内容正确、`.old` 存在、回滚复原、只读目录提前失败原文件不变。

## 5. `internal/config`：`update` 段

- [ ] `Update{Check string; URL string}`、默认 `daily`、环境变量 `LANBOX_UPDATE_CHECK/URL/TOKEN`、`editableKeys` 增加两项、`Origins`、校验取值（off/startup/daily）。
- [ ] 测试：默认值、环境变量、非法值报错、热更不要求重启。

## 6. `internal/app`：注入 Updater 与后台检查

- [ ] `Options.Updater *updater.Updater`、`Options.RestartProcess func()`（服务端传 `OnRestart`）。
- [ ] 启动后按 `update.check` 起 goroutine（startup 10s 一次；daily +24h ticker），`Shutdown` 停止。
- [ ] 测试：注入假 Updater 断言调用次数（用短超时钩子或直接测 `schedule` 纯函数）。

## 7. `internal/httpapi`：四个接口

- [ ] `Options.Updater UpdateService`（接口：`Check/Apply/Rollback/State`），nil → 501。
- [ ] `update_handlers.go` + 路由 + SSE `update` 事件。
- [ ] 测试：鉴权、版本不一致 409、进行中 409、无备份 409、Restart 被调、nil 501。

## 8. `lanbox-cli update`

- [ ] `cmd_update.go`：`update [--check] [--apply] [--rollback] [--yes]`，TTY 确认，`client` 新增三个方法。
- [ ] `internal/client`：`UpdateState/CheckUpdate/ApplyUpdate/RollbackUpdate`。
- [ ] 测试：参数解析、假服务器（httptest）端到端命令。

## 9. Web 状态页

- [ ] `web/status.js`：「最新版本 / 上次检查」行 + 管理员按钮（检查、升级并重启、回滚）；二次确认；重启后自动刷新。
- [ ] `web/style.css`：必要样式（复用现有 button/dialog）。
- [ ] 纯 web 客户端重建：`make client`，确认 `dist/lanbox-client.html` 同步（不提交 dist，但验证能构建）。

## 10. `lanbox-agent`

- [ ] `-wait-pid` 参数（`waitpid_windows.go`，`OpenProcess`+`WaitForSingleObject`，15s 上限）。
- [ ] `app.go`：`Updater` 字段、`UpdateState/CheckUpdate/ApplyUpdate/RollbackUpdate`、启动 30s 后静默检查 + 24h、`update:state` 事件。
- [ ] `settings.go`：`App.CheckUpdate`（默认 true）+ 迁移。
- [ ] `tray.go`：菜单项「检查更新 / 升级到 vX / 回滚到 vX」。
- [ ] 前端（`frontend/dist/app.js` + `index.html`）：设置页「关于」分组。
- [ ] 测试：设置迁移、`-wait-pid` 解析、状态机（不真下载）。

## 11. 收尾

- [ ] `README.md`：更新章节（服务端配置、CLI 命令、agent 菜单、manifest 格式、回滚）。
- [ ] `TODO.md`：追加本次 brainstorming 勾选记录。
- [ ] `make check`（vet + test）、`make build`、`powershell build-windows.ps1`（如可用）。
- [ ] 设计文档状态改「已实现」。
