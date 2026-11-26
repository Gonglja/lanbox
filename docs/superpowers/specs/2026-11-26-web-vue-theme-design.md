# lanbox 前端改版设计（Vue + 主题）

日期：2026-11-26
状态：待实现（实现计划见 `docs/superpowers/plans/2026-11-26-web-vue-theme.md`）
范围：只改前端呈现层与静态资源交付方式；不改业务 API、不改存储格式、不改认证语义。

## 1. 目标

1. 用 **Vue 3** 重写 `web/` 前端（当前是 6 个手写 ES 模块 + 一次 DOM 操作）。
2. 支持**主题**：`暗色` / `亮色` / `跟随系统`，切换即时生效、刷新后保持、首帧不闪烁。
3. **优化展示风格**：更清晰的层次与留白、卡片化列表、空状态/骨架屏、移动端更好用、去掉 `alert()/confirm()` 这种打断式交互。
4. 保持原有全部能力：登录、发布文字+文件、拖拽/粘贴、上传进度、SSE 实时刷新、筛选/搜索/分页、标记已取走、删除内容/附件、用户管理抽屉。

## 2. 硬约束（决定技术选型）

| 约束 | 来源 | 影响 |
|---|---|---|
| 单个静态二进制，`go build` 不依赖 Node | README、`embed.go`、`Makefile` | 前端产物必须提交进仓库，`//go:embed all:web` 保持不变 |
| 零第三方 Go 依赖 | `go.mod`（仅标准库） | Go 侧只加静态资源缓存头和一个测试，不引入库 |
| 部署环境离线 | 小主机 | 运行时不得请求 CDN、不得有外链字体 |
| `Content-Security-Policy: default-src 'self'` | `internal/httpapi/middleware.go` | **禁止内联脚本、禁止 `unsafe-eval`、禁止 `data:` 资源** |
| 附件下载强制 `attachment`、`nosniff` | 设计文档第 8 节 | 保持不变 |

关键推论：**Vue 的运行时模板编译（`template:` 字符串 / in-DOM 模板）依赖 `new Function`，会被 CSP 拦死。** 因此本方案使用**构建期预编译**（SFC → 渲染函数），运行时只加载 Vue runtime，CSP 无需任何放宽。

## 3. 选型对比

| 方案 | 做法 | 优点 | 致命/明显缺点 |
|---|---|---|---|
| A. CDN + `vue.global.js` 运行时编译 | `<script src=...>` + `template` 字符串 | 零构建 | 需要 CDN（离线场景不可用）或把 150KB 全量包塞进 `web/`；**需要 `unsafe-eval`**；产物无哈希、缓存策略差 |
| B. 运行时包 + 手写 `h()` 渲染函数 | 引入 `vue.esm-browser.prod.js`，组件全部用 `h()` | 零构建、CSP 兼容 | 模板可读性差、写 CSS 类名靠字符串、维护成本高；仍有 150KB 全量包 |
| C. **Vite + `@vitejs/plugin-vue`，产物提交进 `web/`，`go:embed` 不变** | 源码在 `frontend/`，`npm run build` 输出到 `web/` | 标准 Vue 开发体验（SFC、scoped CSS、HMR）；产物是预编译渲染函数 + 带哈希的静态文件；**CSP 一行都不用放宽**；Go 构建与部署完全不受影响 | 改前端需要 Node（仅开发机）；`web/` 里是生成物，需要“改前端必须重新构建并提交”的纪律 |

**选定 C。** 理由：约束里“部署零依赖”是最硬的一条，而 C 只把 Node 需求转移到开发机；同时它是唯一能同时满足 CSP 严格、离线、可长期维护三个条件的方案。代价（生成物入库）用 Go 测试 + README 说明来兜底。

### 3.1 为什么不用 Pinia / Vue Router / UI 组件库

- 单页 + 一个视图切换（登录/主界面）+ 一个抽屉，路由用不上；状态用 `composable 单例` 就够（`ref` + 模块级作用域）。
- 组件库（Element Plus 等）会引入几十万行 CSS/JS 与自己的主题体系，与“小主机、慢网络、双端都比较老”的场景不匹配；本设计自己写 200 行 token CSS 更可控。
- 结论：依赖只有 `vue` + `vite` + `@vitejs/plugin-vue`（后两个仅 devDependency）。

## 4. 目录与构建管线

```
frontend/                     # 前端源码（进仓库）
├── package.json              # 依赖与脚本：dev / build
├── package-lock.json         # 锁定版本（提交）
├── vite.config.js            # outDir=../web，assetsInlineLimit=0，dev 代理到 :8443
├── index.html                # Vite 入口
├── public/
│   ├── theme-boot.js         # 首帧前应用主题（普通脚本，非 module）
│   └── favicon.svg
└── src/
    ├── main.js
    ├── App.vue
    ├── styles/tokens.css     # 主题令牌（dark / light）
    ├── styles/app.css        # reset + 基础排版 + 通用控件
    ├── lib/{api,format,events}.js
    ├── composables/{useTheme,useSession,useItems,useUpload,useUi,useToast,useConfirm}.js
    └── components/{LoginView,AppHeader,ThemePicker,ComposerCard,ItemCard,ItemList,AdminDrawer,ToastStack,ConfirmDialog}.vue

web/                          # 构建产物（进仓库，被 go:embed 打进二进制）
├── index.html
├── theme-boot.js
├── favicon.svg
└── assets/index-<hash>.js | index-<hash>.css | ...
```

- `npm run build` → Vite 以 `emptyOutDir: true` 清空并重建 `web/`，因此不会残留旧的 `main.js`/`style.css`。
- `assetsInlineLimit: 0`：禁止把图标/字体内联成 `data:` URL（CSP `default-src 'self'` 会拦）。
- 不使用外部字体，字体族保持 `system-ui` 系列，CJK 回退保留。
- 三个 `npm` 依赖的版本以 `package-lock.json` 为准（当前 registry 最新：vue 3.5.x、vite 8.x、@vitejs/plugin-vue 6.x）。

## 5. 主题系统

### 5.1 令牌（CSS 自定义属性）

唯一主题来源是 `src/styles/tokens.css`，所有组件只引用语义变量，不写死颜色。

| 变量 | 含义 |
|---|---|
| `--bg` / `--bg-soft` | 页面底色 / 次级底色 |
| `--surface` / `--surface-hover` / `--surface-sunken` | 卡片 / 悬停 / 内凹（输入框、进度槽） |
| `--line` / `--line-strong` | 分隔线 / 强调边框 |
| `--text` / `--text-muted` / `--text-faint` | 正文 / 次要 / 最弱 |
| `--accent` / `--accent-hover` / `--accent-contrast` / `--accent-soft` / `--accent-line` | 主色及其衍生 |
| `--danger` / `--danger-soft` / `--danger-line` | 危险（删除） |
| `--ok` / `--ok-soft` / `--ok-line` | 成功（实时连接、已取走） |
| `--warn` | 警告 |
| `--shadow-1` / `--shadow-2` | 卡片阴影 / 浮层阴影 |
| `--header-bg` / `--backdrop` | 顶栏半透明底 / 抽屉遮罩 |
| `--radius-sm/md/lg/pill`、`--gap-1..6`、`--dur`、`--ease`、`--content-width`、`--font-sans`、`--font-mono` | 与主题无关的尺寸/动效令牌，放在 `:root` |

两套取值挂在 `:root[data-theme='dark']` 与 `:root[data-theme='light']`，各自声明 `color-scheme`（让原生控件、滚动条、表单控件跟随）。

对比度要求：正文与 `--surface` ≥ 7:1，`--text-muted` ≥ 4.5:1（用浏览器 DevTools 核对一次即可写入验收清单）。

### 5.2 切换与持久化

- 三种选择：`system`（默认）/ `dark` / `light`，存在 `localStorage['lanbox.theme']`。
- `theme-boot.js`（`public/`，普通脚本，`<head>` 内同步执行）在**任何绘制之前**读取选择、用 `matchMedia('(prefers-color-scheme: dark)')` 解析 `system`，写入 `document.documentElement.dataset.theme` 与 `style.colorScheme`。这样不会出现“先白后黑”的闪屏。
- `useTheme()` 负责后续交互：监听系统主题变化（`system` 模式下自动跟随）、切换时写回 `localStorage` 并更新 `data-theme`。
- 存储不可用时（隐私模式）静默降级为 `system`，不报错。
- 不接受 URL 参数切换（无必要，且会污染缓存与分享链接）。

### 5.3 主题切换控件

顶栏右侧一个分段控件（`ThemePicker.vue`）：`跟随系统 / 亮色 / 暗色`，用 `role="radiogroup"` + `aria-checked`，每个选项是原生 `<button>`（Tab 聚焦、Enter/Space 选择）；移动端只显示图标不显示文字。

## 6. 界面改版

布局不变（顶栏 + 单列内容 + 抽屉），改的是层次与反馈：

1. **顶栏**：品牌 + 用户身份 + SSE 状态点（连接中/已连接/重连中，带 `aria-live="polite"`）+ 主题切换 + 用户管理 + 登出。粘性、半透明、`backdrop-filter`，滚动时只留细边框。
2. **发布区（ComposerCard）**：上方多行输入框（内部滚动，最高约 220px），下方一行「选择文件 / 待上传合计 / 发送」。拖拽整卡高亮并显示覆盖层提示；`Ctrl/⌘+Enter` 发送；待上传文件以 chip 展示（名称、大小、移除）。上传时按钮进入 loading，进度条显示百分比与字节数，失败保留内容可重试。
3. **列表（ItemList + ItemCard）**：
   - 卡片头部：作者首字母圆形头像（按用户名哈希取一个柔和底色）、作者名、来源 IP 小标签、相对时间（`刚刚 / 12 分钟前 / 昨天 18:03`，`title` 显示绝对时间）。
   - 正文：保留换行与长词断行；正文为空时不渲染该块。
   - 附件行：文件名、大小、`sha256` 前 8 位、下载次数、下载按钮、删除按钮；窄屏换行不溢出。
   - 底部操作：`标记已取走 / 取消取走`（ghost 按钮）、`删除`（danger ghost，需二次确认）。
   - 已取走：卡片降透明度 + 绿色 `已取走` 标签，不隐藏。
4. **空状态/加载**：首次加载显示 3 张骨架卡；无数据显示插画式空状态（纯 CSS/SVG，不引外部图片），区分“还没有内容”和“没有匹配结果”两种文案。
5. **反馈层**：`alert()`/`confirm()` 全部替换为 `ToastStack.vue`（右上角堆叠，4 秒自动消失，错误 6 秒，可手动关闭）与 `ConfirmDialog.vue`（居中对话框、`Escape` 取消、危险操作红色主按钮、默认焦点在取消）。删除确认文案保留“删除后无法恢复”。
6. **筛选与搜索**：`全部 / 未取走` 作为分段控件；搜索框 300ms 防抖（沿用现有逻辑），带清除按钮。
7. **管理抽屉**：右侧滑入 + 遮罩，打开时焦点落在「关闭」按钮，`Escape` 或点遮罩关闭。
8. **响应式**：`< 560px` 时内容内边距缩小、顶栏隐藏用户名只留头像、操作按钮加大到 40px 触控高度、抽屉全屏。
9. **动效**：只做 160ms 的透明度/位移过渡，全部包在 `@media (prefers-reduced-motion: reduce) { transition: none }` 里。
10. **可访问性**：所有可点元素有 `:focus-visible` 环；表单 `label` 关联；图标按钮有 `aria-label`；toast 容器 `role="status"`。

## 7. 状态管理（composable 单例，无 Pinia）

| 模块 | 状态 | 责任 |
|---|---|---|
| `useTheme` | `pref` / `resolved` | 读写 `data-theme`、localStorage、系统主题监听 |
| `useSession` | `user` / `ready` / `error` | `api.me/login/logout`，启动时探测登录态 |
| `useItems` | `items` / `cursor` / `filter` / `q` / `loading` / `error` / `sseState` | 列表拉取与分页、筛选搜索、SSE 合流、取走/删除动作 |
| `useUpload` | `files` / `text` / `uploading` / 进度 | XHR 上传（保留真实进度）、清空与重试 |
| `useUi` | `adminOpen` | 顶栏与抽屉之间共享的开关 |
| `useToast` / `useConfirm` | 队列 / 当前对话框 | 替代 `alert`/`confirm` |

约定：**模块级单例**（`const items = ref([])` 在模块作用域），`useX()` 只返回同一份引用。因此 `connect()` 只允许在 `App.vue` 的挂载处调用一次，避免重复建立 SSE 连接。

SSE 事件合流规则保持现有语义：
- `item.created` / `item.updated`：搜索框非空 → 重新拉第一页（以服务端命中为准）；`filter=todo` 且 `taken` → 从列表移除；否则 upsert 并按 id 倒序排序。
- `item.deleted`：按 id 移除。
- 断线：沿用 `EventSource` 自动重连 + `readyState===CLOSED` 时 3 秒后重建，状态映射到顶栏指示点。

## 8. Go 侧改动（很小）

1. `internal/httpapi/server.go` → `staticHandler`：
   - `/assets/` 前缀：`Cache-Control: public, max-age=31536000, immutable`（文件名带内容哈希）。
   - 其余（`index.html`、`theme-boot.js`、`favicon.svg`）：`Cache-Control: no-cache`（保持现状）。
2. `internal/httpapi/middleware.go` → CSP 显式化：

   ```
   default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self';
   font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self';
   form-action 'self'; frame-ancestors 'none'
   ```

   语义与现在等价（都回落到 `'self'`），但把“不允许内联/eval”写成明确的意图，并顺带覆盖 `object/base-uri/form-action/frame-ancestors`。**不加 `unsafe-inline` / `unsafe-eval`。**
3. 新增两个测试（Go 侧，不需要 Node）：
   - `web_test.go`（包 `lanbox_test`）：`WebFS()` 里有 `index.html`、`theme-boot.js`、非空 `assets/`；`index.html` 含挂载点且**无内联脚本**；引用的每一个 `/assets/...` 文件都存在 —— 这条能在“忘了 `npm run build` 就提交”时直接失败。
   - `internal/httpapi/static_test.go`：`GET /` 的 CSP 不含 `unsafe-eval`/`unsafe-inline` 且含 `script-src 'self'`，`Cache-Control: no-cache`；`GET /assets/<hash>.js` 返回 `immutable` 且 200。

## 9. 风险与对策

| 风险 | 对策 |
|---|---|
| 忘记重新构建就把 `frontend/` 改动提交，二进制里是旧界面 | `web_test.go` 校验引用完整性；README 与 `Makefile` 提示“改前端 → `make web` → 一起提交” |
| Vite 把小资源内联成 `data:`，被 CSP 拦 | `assetsInlineLimit: 0`；`web_test.go` 断言 `index.html` 里没有 `data:` 引用 |
| Vue 运行时模板编译触发 `unsafe-eval` | 只用 SFC（构建期预编译），禁止 `template:` 字符串；CSP 收紧后由浏览器实测验证 |
| 主题闪屏（FOUC） | `theme-boot.js` 同步执行于 `<head>`，先于渲染 |
| 亮色主题对比度不足 | 令牌表按下限设定 + 验收清单里的对比度检查 |
| 开发模式（vite dev）与生产（Go 服务）行为不一致 | dev 走 `vite.config.js` 的 `/api` 代理到 `https://127.0.0.1:8443`；README 写明本机 `localhost` 属安全上下文，`Secure` cookie 可正常写入；若个别浏览器不认，退回 `vite build --watch` + Go 服务访问 |
| 旧版 `web/*.js` 残留 | 方案删除旧文件，构建产物 `emptyOutDir` 清空目录 |

## 10. 验收标准

1. `cd frontend && npm ci && npm run build` 成功；`web/` 下只有构建产物，无旧 `main.js`/`style.css`。
2. `make check` 通过（gofmt + vet + 全部测试，含新增两个测试）。
3. `make build && ./lanbox` 后浏览器访问 `https://<ip>:8443`：
   - 登录 → 发布文字+文件 → 另一窗口 1 秒内出现（SSE）；
   - 拖拽/粘贴上传、进度条、Ctrl+Enter 发送正常；
   - 标记已取走/删除内容/删除附件均有确认对话框与 toast；
   - 主题在 `系统/亮色/暗色` 间即时切换，刷新后保持，无闪屏；
   - 手机宽度（375px）下无横向滚动，按钮可点。
4. 浏览器控制台无 CSP violation、无 JS 报错；`GET /` 响应头 CSP 不含 `unsafe-*`。
5. `make dist` 交叉编译四个平台成功，产物仍可直接拷到小主机运行（前端已嵌入）。

## 11. 明确不做

- 不做 Vue Router、Pinia、TypeScript、单元测试框架（Vitest/Playwright）、ESLint/Prettier 全套脚手架。
- 不做服务端持久化的主题偏好（`localStorage` 足够）。
- 不做主题自定义配色、暗色以外的第三套皮肤、字体大小设置。
- 不改任何 `/api/*` 契约、存储布局、会话与密码策略。
- 不做附件预览（图片缩略图/在线打开），下载仍强制 `attachment`。
- 不做分片上传、打包下载（沿用设计文档的“不做”清单）。
