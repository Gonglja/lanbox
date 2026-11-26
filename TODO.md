# TODO: mDNS 发现 + Web 配置

## Brainstorming checklist

- [x] 1. 探索项目上下文（README、specs/plans、config/httpapi/store、deploy unit）
- [x] 2. 澄清问题（mDNS 范围、覆盖文件、重启方式、CLI 发现、入口位置）
- [x] 3. 提出方案与取舍（spec §3）
- [x] 4. 呈现设计并逐节确认
- [x] 5. 写 spec 到 docs/superpowers/specs/2026-11-26-mdns-and-web-config-design.md
- [x] 6. spec 自检（占位符/矛盾/歧义/范围）
- [x] 7. 用户审阅 spec
- [x] 8. 写实现计划 docs/superpowers/plans/2026-11-26-mdns-and-web-config.md
- [x] 9. 执行实现计划（13 个任务全部完成并合并回 main）

# TODO: 在线更新（检查 + 手动升级 + 回滚）

## Brainstorming checklist

- [x] 1. 探索项目上下文（版本注入、release.yml、restart_*.go、config/httpapi、agent）
- [x] 2. 澄清问题（覆盖哪些程序、更新源、触发方式、回滚策略）
- [x] 3. 提出方案与取舍（GitHub 静态路径 vs API、替换与交接、备份策略）
- [x] 4. 逐节确认设计（updater 包 / 服务端 / agent / 错误与测试）
- [x] 5. 写 spec 到 docs/superpowers/specs/2026-11-27-auto-update-design.md
- [x] 6. 写实现计划 docs/superpowers/plans/2026-11-27-auto-update.md
- [x] 7. 执行实现（internal/updater → config → app → httpapi → cli → web → agent）
- [x] 8. README / TODO / make check
