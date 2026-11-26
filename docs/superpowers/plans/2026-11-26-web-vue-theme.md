# lanbox 前端改版（Vue + 主题）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 Vue 3 + Vite 重写 lanbox 内嵌前端，新增暗色/亮色/跟随系统三态主题，并改进展示风格，同时保持单二进制部署与严格 CSP。

**Architecture:** 源码放 `frontend/`（Vite + SFC，构建期预编译渲染函数），`npm run build` 输出到 `web/` 并提交进仓库，Go 侧 `//go:embed all:web` 完全不变。运行时只有 Vue runtime + 静态资源，不需要 Node、不需要 CDN、不需要 `unsafe-eval`。

**Tech Stack:** Vue 3.5（runtime-only）、Vite 8、`@vitejs/plugin-vue` 6、原生 CSS 自定义属性做主题、Go 1.26 标准库。

**设计依据：** `docs/superpowers/specs/2026-11-26-web-vue-theme-design.md`（先读它，特别是第 2 节硬约束与第 11 节“不做”清单）。

---

## 前置阅读（实现者必看）

1. `docs/superpowers/specs/2026-11-26-web-vue-theme-design.md` — 本计划的设计来源。
2. `web/` 现有 6 个文件（`index.html`、`main.js`、`api.js`、`render.js`、`sse.js`、`admin.js`、`style.css`）—— 现在的实现，所有行为必须等价迁移。执行 Task 1 之后它们会被删除，**执行前先读一遍**。
3. `internal/httpapi/server.go:81-102`（静态文件）与 `internal/httpapi/middleware.go:40-48`（安全头）。
4. `internal/httpapi/api_test.go:37-70`（`newEnv` 测试夹具，写 Go 测试时复用）。

## 环境检查（Task 1 之前做一次）

```bash
cd /home/u/workspace/lanbox
node -v            # 期望 v20.19+ 或 v22.12+（当前机器 v26 可用）
npm -v
go version         # 期望 go1.24+
git status         # 必须干净，避免把实现混进别的改动
```

## 文件结构（本计划全部产物）

```
frontend/
├── .gitignore                 # node_modules/
├── package.json               # vue（依赖）、vite + @vitejs/plugin-vue（dev）
├── vite.config.js             # outDir=../web，assetsInlineLimit=0，dev 代理 /api
├── index.html                 # 入口，<head> 内同步加载 theme-boot.js
├── public/
│   ├── theme-boot.js          # 首帧前应用主题（普通脚本）
│   └── favicon.svg
└── src/
    ├── main.js
    ├── App.vue
    ├── styles/tokens.css      # 主题令牌
    ├── styles/app.css         # reset + 基础排版 + .btn/.input/.card 等通用类
    ├── lib/api.js             # 从 web/api.js 平移
    ├── lib/format.js          # 字节/时间/头像
    ├── lib/events.js          # 从 web/sse.js 平移
    ├── composables/useTheme.js
    ├── composables/useSession.js
    ├── composables/useItems.js
    ├── composables/useUpload.js
    ├── composables/useUi.js
    ├── composables/useToast.js
    ├── composables/useConfirm.js
    └── components/
        ├── LoginView.vue
        ├── AppHeader.vue
        ├── ThemePicker.vue
        ├── ComposerCard.vue
        ├── ItemCard.vue
        ├── ItemList.vue
        ├── AdminDrawer.vue
        ├── ToastStack.vue
        └── ConfirmDialog.vue

web/                           # 构建产物（提交）
├── index.html
├── theme-boot.js
├── favicon.svg
└── assets/*.js|*.css

修改：
├── internal/httpapi/server.go         # /assets/ 长缓存
├── internal/httpapi/middleware.go     # CSP 显式化
├── Makefile                           # web / web-dev 目标
├── README.md                          # 前端开发与主题说明
└── .gitignore                         # frontend/node_modules

新增测试：
├── web_test.go                        # 构建产物完整性（根包 lanbox_test）
└── internal/httpapi/static_test.go    # 静态资源缓存头与 CSP
```

删除：`web/index.html`、`web/main.js`、`web/api.js`、`web/render.js`、`web/sse.js`、`web/admin.js`、`web/style.css`。

---

## Task 1：前端脚手架与构建管线跑通

**Files:**
- Create: `frontend/.gitignore`、`frontend/package.json`、`frontend/vite.config.js`、`frontend/index.html`、`frontend/public/theme-boot.js`、`frontend/public/favicon.svg`、`frontend/src/main.js`、`frontend/src/App.vue`（临时最小版）、`frontend/src/styles/tokens.css`（临时最小版）、`frontend/src/styles/app.css`（临时最小版）
- Delete: `web/index.html`、`web/main.js`、`web/api.js`、`web/render.js`、`web/sse.js`、`web/admin.js`、`web/style.css`

- [ ] **Step 1: 先读一遍旧的 6 个前端文件**

```bash
cd /home/u/workspace/lanbox && wc -l web/* && sed -n 1,120p web/main.js
```

目的：确认要迁移的行为清单（登录、SSE 状态、拖拽粘贴、上传进度、筛选搜索、分页、抽屉）。

- [ ] **Step 2: 写 `frontend/.gitignore`**

```gitignore
node_modules/
.vite/
```

- [ ] **Step 3: 写 `frontend/package.json`**

```json
{
  "name": "lanbox-web",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build"
  },
  "dependencies": {
    "vue": "^3.5.46"
  },
  "devDependencies": {
    "@vitejs/plugin-vue": "^6.0.11",
    "vite": "^8.3.8"
  }
}
```

- [ ] **Step 4: 写 `frontend/vite.config.js`**

```js
import { fileURLToPath, URL } from 'node:url';
import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';

export default defineConfig({
  plugins: [vue()],
  base: '/',
  build: {
    // 产物直接写进 Go 的 go:embed 目录
    outDir: fileURLToPath(new URL('../web', import.meta.url)),
    emptyOutDir: true,
    // 禁止内联成 data: URL —— CSP 是 default-src 'self'
    assetsInlineLimit: 0,
    target: 'es2020',
    sourcemap: false,
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    proxy: {
      // 本地开发把 /api 转发给正在运行的 lanbox（自签证书 → secure:false）
      '/api': {
        target: 'https://127.0.0.1:8443',
        changeOrigin: false,
        secure: false,
      },
    },
  },
});
```

- [ ] **Step 5: 写 `frontend/index.html`**

```html
<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta name="color-scheme" content="dark light" />
    <title>lanbox</title>
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <script src="/theme-boot.js"></script>
  </head>
  <body>
    <div id="app"></div>
    <script type="module" src="/src/main.js"></script>
  </body>
</html>
```

注意：`theme-boot.js` 必须是**普通脚本且不加 defer**，它要在首帧前跑；内联脚本会被 CSP 拦，所以放 `public/` 下用 `src` 引用。

- [ ] **Step 6: 写 `frontend/public/theme-boot.js`**

```js
// 在首帧绘制之前把主题写到 <html>，避免“先白后黑”的闪屏。
// 必须保持 ES5 语法 + 普通脚本（CSP 不允许内联脚本，也不允许它被延迟执行）。
(function () {
  var KEY = 'lanbox.theme';
  var pref = 'system';
  try {
    var saved = localStorage.getItem(KEY);
    if (saved === 'dark' || saved === 'light' || saved === 'system') pref = saved;
  } catch (e) {
    /* 隐私模式：忽略，用 system */
  }
  var dark = true;
  try {
    dark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  } catch (e) {
    /* 老浏览器：默认暗色 */
  }
  var theme = pref === 'system' ? (dark ? 'dark' : 'light') : pref;
  var root = document.documentElement;
  root.dataset.theme = theme;
  root.dataset.themePref = pref;
  root.style.colorScheme = theme;
})();
```

- [ ] **Step 7: 写 `frontend/public/favicon.svg`**

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
  <rect width="32" height="32" rx="8" fill="#2563eb" />
  <path d="M9 12h11l-2.5-2.5M23 20H12l2.5 2.5" fill="none" stroke="#fff" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" />
</svg>
```

- [ ] **Step 8: 写临时最小版 `frontend/src/main.js`**

```js
import { createApp } from 'vue';
import App from './App.vue';
import './styles/tokens.css';
import './styles/app.css';

createApp(App).mount('#app');
```

- [ ] **Step 9: 写临时最小版 `frontend/src/styles/tokens.css`**

```css
:root {
  --radius-sm: 8px;
  --radius-md: 12px;
  --radius-lg: 16px;
  --gap-1: 4px;
  --gap-2: 8px;
  --gap-3: 12px;
  --gap-4: 16px;
  --gap-5: 24px;
  --gap-6: 32px;
  --font-sans: system-ui, -apple-system, 'Segoe UI', 'Noto Sans CJK SC', 'Microsoft YaHei', sans-serif;
  --font-mono: ui-monospace, SFMono-Regular, Consolas, monospace;
  --dur: 160ms;
  --ease: cubic-bezier(0.2, 0.8, 0.2, 1);
  --content-width: 880px;
}

:root[data-theme='dark'] {
  color-scheme: dark;
  --bg: #0e1116;
  --surface: #171c23;
  --line: #252c36;
  --text: #e7ecf3;
  --text-muted: #8c97a7;
  --accent: #4c8dff;
}

:root[data-theme='light'] {
  color-scheme: light;
  --bg: #f4f6f9;
  --surface: #ffffff;
  --line: #e0e5ee;
  --text: #141a23;
  --text-muted: #5d6779;
  --accent: #2563eb;
}
```

（Task 2 会把这两个文件补全成完整令牌表与样式。）

- [ ] **Step 10: 写临时最小版 `frontend/src/styles/app.css`**

```css
*,
*::before,
*::after {
  box-sizing: border-box;
}

body {
  margin: 0;
  background: var(--bg);
  color: var(--text);
  font: 15px/1.55 var(--font-sans);
}
```

- [ ] **Step 11: 写临时最小版 `frontend/src/App.vue`**

```vue
<script setup>
// 本步只需要骨架；主题与组件在后续任务接入。
</script>

<template>
  <main style="padding: 24px">
    <h1>lanbox</h1>
    <p>前端脚手架已就绪。</p>
  </main>
</template>
```

同时创建 `frontend/src/composables/useTheme.js`（这是最终版本，Task 2 不再改它）：

```js
import { computed, ref, watch } from 'vue';

const KEY = 'lanbox.theme';
const VALID = ['system', 'dark', 'light'];

const mq = typeof window !== 'undefined' && window.matchMedia
  ? window.matchMedia('(prefers-color-scheme: dark)')
  : null;

function readPref() {
  try {
    const saved = localStorage.getItem(KEY);
    if (VALID.includes(saved)) return saved;
  } catch {
    /* 隐私模式 */
  }
  return 'system';
}

const pref = ref(readPref());
const systemDark = ref(mq ? mq.matches : true);

if (mq) {
  const onChange = (event) => {
    systemDark.value = event.matches;
  };
  if (mq.addEventListener) mq.addEventListener('change', onChange);
  else if (mq.addListener) mq.addListener(onChange);
}

const resolved = computed(() => (pref.value === 'system' ? (systemDark.value ? 'dark' : 'light') : pref.value));

watch(
  resolved,
  (theme) => {
    const root = document.documentElement;
    root.dataset.theme = theme;
    root.dataset.themePref = pref.value;
    root.style.colorScheme = theme;
  },
  { immediate: true },
);

watch(pref, (next) => {
  try {
    localStorage.setItem(KEY, next);
  } catch {
    /* 存不下就只在本次会话生效 */
  }
});

export const themeOptions = [
  { value: 'system', label: '跟随系统' },
  { value: 'light', label: '亮色' },
  { value: 'dark', label: '暗色' },
];

export function useTheme() {
  return {
    pref,
    resolved,
    setTheme(next) {
      if (VALID.includes(next)) pref.value = next;
    },
  };
}
```

- [ ] **Step 12: 删除旧前端文件（构建产物会重新生成 `web/`）**

```bash
cd /home/u/workspace/lanbox
git rm web/index.html web/main.js web/api.js web/render.js web/sse.js web/admin.js web/style.css
```

- [ ] **Step 13: 安装依赖并构建**

```bash
cd /home/u/workspace/lanbox/frontend
npm install
npm run build
```

预期：`npm install` 生成 `package-lock.json`（要提交）；`npm run build` 输出类似

```
vite v8.x building for production...
✓ built in 700ms
```

- [ ] **Step 14: 验证产物形状**

```bash
cd /home/u/workspace/lanbox
ls web web/assets
grep -c '<div id="app">' web/index.html
grep -o '/assets/[^"]*' web/index.html | sort -u
grep -n 'data:' web/index.html || echo "OK: 无 data: 内联资源"
test ! -e web/main.js && test ! -e web/style.css && echo "OK: 旧文件已清空"
```

预期：`web/` 里只有 `index.html`、`theme-boot.js`、`favicon.svg`、`assets/`；`grep 'data:'` 无命中。

- [ ] **Step 15: 确认 Go 侧仍然能编译（embed 目录变了）**

```bash
cd /home/u/workspace/lanbox && go build ./... && go test ./... -count=1
```

预期：全部通过（此时前端还是临时最小版，行为测试不涉及 HTML 内容）。

- [ ] **Step 16: 提交**

```bash
cd /home/u/workspace/lanbox
git add frontend web .gitignore
git commit -m "build(web): 引入 Vue 3 + Vite 构建管线，产物嵌入 web/"
```

---

## Task 2：主题令牌、全局样式与主题切换控件

**Files:**
- Modify: `frontend/src/styles/tokens.css`（补全）
- Modify: `frontend/src/styles/app.css`（补全）
- Create: `frontend/src/components/ThemePicker.vue`

- [ ] **Step 1: 用完整令牌表替换 `frontend/src/styles/tokens.css`**

```css
/* 主题令牌：组件只引用这里的语义变量，不写死颜色。 */
:root {
  /* 尺寸与动效（与主题无关） */
  --radius-sm: 8px;
  --radius-md: 12px;
  --radius-lg: 16px;
  --radius-pill: 999px;
  --gap-1: 4px;
  --gap-2: 8px;
  --gap-3: 12px;
  --gap-4: 16px;
  --gap-5: 24px;
  --gap-6: 32px;
  --font-sans: system-ui, -apple-system, 'Segoe UI', 'Noto Sans CJK SC', 'Microsoft YaHei', sans-serif;
  --font-mono: ui-monospace, SFMono-Regular, Consolas, monospace;
  --dur: 160ms;
  --ease: cubic-bezier(0.2, 0.8, 0.2, 1);
  --content-width: 880px;
}

:root[data-theme='dark'] {
  color-scheme: dark;
  --bg: #0e1116;
  --bg-soft: #12161c;
  --surface: #171c23;
  --surface-hover: #1d232b;
  --surface-sunken: #0b0e12;
  --line: #252c36;
  --line-strong: #36414f;
  --text: #e7ecf3;
  --text-muted: #98a3b3;
  --text-faint: #74808f;
  --accent: #4c8dff;
  --accent-hover: #6ba0ff;
  --accent-contrast: #ffffff;
  --accent-soft: rgba(76, 141, 255, 0.14);
  --accent-line: rgba(76, 141, 255, 0.38);
  --danger: #ff7b7b;
  --danger-soft: rgba(255, 107, 107, 0.12);
  --danger-line: rgba(255, 107, 107, 0.35);
  --ok: #46d39a;
  --ok-soft: rgba(70, 211, 154, 0.12);
  --ok-line: rgba(70, 211, 154, 0.32);
  --warn: #f0b429;
  --shadow-1: 0 1px 2px rgba(0, 0, 0, 0.35);
  --shadow-2: 0 16px 40px rgba(0, 0, 0, 0.5);
  --header-bg: rgba(14, 17, 22, 0.78);
  --backdrop: rgba(6, 9, 13, 0.62);
}

:root[data-theme='light'] {
  color-scheme: light;
  --bg: #f4f6f9;
  --bg-soft: #eef1f6;
  --surface: #ffffff;
  --surface-hover: #f6f8fb;
  --surface-sunken: #eef1f6;
  --line: #e0e5ee;
  --line-strong: #c6d0de;
  --text: #141a23;
  --text-muted: #586375;
  --text-faint: #7b8697;
  --accent: #2563eb;
  --accent-hover: #1d4ed8;
  --accent-contrast: #ffffff;
  --accent-soft: rgba(37, 99, 235, 0.1);
  --accent-line: rgba(37, 99, 235, 0.3);
  --danger: #cf3535;
  --danger-soft: rgba(207, 53, 53, 0.08);
  --danger-line: rgba(207, 53, 53, 0.26);
  --ok: #0f8a5b;
  --ok-soft: rgba(15, 138, 91, 0.1);
  --ok-line: rgba(15, 138, 91, 0.26);
  --warn: #9a6100;
  --shadow-1: 0 1px 2px rgba(16, 24, 40, 0.06);
  --shadow-2: 0 18px 40px rgba(16, 24, 40, 0.14);
  --header-bg: rgba(255, 255, 255, 0.82);
  --backdrop: rgba(16, 24, 40, 0.36);
}
```

- [ ] **Step 2: 用完整基础样式替换 `frontend/src/styles/app.css`**

```css
*,
*::before,
*::after {
  box-sizing: border-box;
}

html,
body {
  height: 100%;
}

body {
  margin: 0;
  background: var(--bg);
  color: var(--text);
  font: 15px/1.55 var(--font-sans);
  -webkit-font-smoothing: antialiased;
  transition: background-color var(--dur) var(--ease), color var(--dur) var(--ease);
}

h1,
h2,
h3,
p,
ul,
ol {
  margin: 0;
}

ul,
ol {
  padding: 0;
  list-style: none;
}

a {
  color: var(--accent);
  text-decoration: none;
}

a:hover {
  text-decoration: underline;
}

button,
input,
textarea,
select {
  font: inherit;
  color: inherit;
}

::selection {
  background: var(--accent-soft);
}

:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

/* ---------- 通用控件 ---------- */

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--gap-2);
  min-height: 36px;
  padding: 8px 14px;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  cursor: pointer;
  white-space: nowrap;
  transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease), transform var(--dur) var(--ease);
}

.btn:hover:not(:disabled) {
  background: var(--surface-hover);
  border-color: var(--line-strong);
}

.btn:active:not(:disabled) {
  transform: translateY(1px);
}

.btn:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.btn-primary {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--accent-contrast);
}

.btn-primary:hover:not(:disabled) {
  background: var(--accent-hover);
  border-color: var(--accent-hover);
}

.btn-ghost {
  background: transparent;
  border-color: transparent;
  color: var(--text-muted);
}

.btn-ghost:hover:not(:disabled) {
  background: var(--surface-hover);
  color: var(--text);
}

.btn-danger {
  background: transparent;
  border-color: var(--danger-line);
  color: var(--danger);
}

.btn-danger:hover:not(:disabled) {
  background: var(--danger-soft);
}

.btn-sm {
  min-height: 30px;
  padding: 4px 10px;
  font-size: 13px;
}

.field {
  display: grid;
  gap: 6px;
}

.field > span {
  font-size: 13px;
  color: var(--text-muted);
}

.input {
  width: 100%;
  padding: 9px 11px;
  background: var(--surface-sunken);
  color: var(--text);
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  transition: border-color var(--dur) var(--ease), box-shadow var(--dur) var(--ease);
}

.input:focus {
  outline: none;
  border-color: var(--accent-line);
  box-shadow: 0 0 0 3px var(--accent-soft);
}

textarea.input {
  resize: vertical;
  min-height: 72px;
  line-height: 1.6;
}

.card {
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-1);
}

.muted {
  color: var(--text-muted);
  font-size: 13px;
}

.error {
  color: var(--danger);
  font-size: 13px;
}

.spacer {
  flex: 1;
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

@media (prefers-reduced-motion: reduce) {
  *,
  *::before,
  *::after {
    transition: none !important;
    animation: none !important;
  }
}
```

- [ ] **Step 3: 写 `frontend/src/components/ThemePicker.vue`**

```vue
<script setup>
import { themeOptions, useTheme } from '../composables/useTheme.js';

const { pref, setTheme } = useTheme();
</script>

<template>
  <div class="picker" role="radiogroup" aria-label="主题">
    <button
      v-for="opt in themeOptions"
      :key="opt.value"
      type="button"
      role="radio"
      class="opt"
      :class="{ active: pref === opt.value }"
      :aria-checked="pref === opt.value"
      :title="opt.label"
      @click="setTheme(opt.value)"
    >
      <svg v-if="opt.value === 'system'" viewBox="0 0 20 20" aria-hidden="true">
        <rect x="2.5" y="3.5" width="15" height="10" rx="1.6" fill="none" stroke="currentColor" stroke-width="1.6" />
        <path d="M7 16.5h6" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
      </svg>
      <svg v-else-if="opt.value === 'light'" viewBox="0 0 20 20" aria-hidden="true">
        <circle cx="10" cy="10" r="3.6" fill="none" stroke="currentColor" stroke-width="1.6" />
        <path
          d="M10 2v2M10 16v2M2 10h2M16 10h2M4.3 4.3l1.4 1.4M14.3 14.3l1.4 1.4M15.7 4.3l-1.4 1.4M5.7 14.3l-1.4 1.4"
          stroke="currentColor"
          stroke-width="1.6"
          stroke-linecap="round"
        />
      </svg>
      <svg v-else viewBox="0 0 20 20" aria-hidden="true">
        <path
          d="M16 12.3A6.8 6.8 0 0 1 7.7 4a6.8 6.8 0 1 0 8.3 8.3Z"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          stroke-linejoin="round"
        />
      </svg>
      <span class="label">{{ opt.label }}</span>
    </button>
  </div>
</template>

<style scoped>
.picker {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  background: var(--surface-sunken);
  border: 1px solid var(--line);
  border-radius: var(--radius-pill);
}

.opt {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 4px 9px;
  border: 0;
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
  transition: background var(--dur) var(--ease), color var(--dur) var(--ease);
}

.opt svg {
  width: 15px;
  height: 15px;
  flex: none;
}

.opt:hover {
  color: var(--text);
}

.opt.active {
  background: var(--surface);
  color: var(--accent);
  box-shadow: var(--shadow-1);
}

@media (max-width: 700px) {
  .opt .label {
    display: none;
  }

  .opt {
    padding: 6px 9px;
  }
}
</style>
```

- [ ] **Step 4: 让 `App.vue` 顶栏临时挂上 ThemePicker 验证主题生效**

```vue
<script setup>
import ThemePicker from './components/ThemePicker.vue';
</script>

<template>
  <header style="display: flex; align-items: center; gap: 12px; padding: 16px">
    <strong>lanbox</strong>
    <span class="spacer" />
    <ThemePicker />
  </header>
  <main style="padding: 0 16px 24px; max-width: 880px; margin: 0 auto">
    <p>主题切换验证页。</p>
  </main>
</template>
```

- [ ] **Step 5: 构建并人工核对**

```bash
cd /home/u/workspace/lanbox/frontend && npm run build
cd .. && ./lanbox 2>/dev/null || go run ./cmd/lanbox &
```

浏览器打开 `https://127.0.0.1:8443`：

1. 点三个选项，整页立即换色，`<html>` 的 `data-theme` 跟着变；
2. 刷新后仍是刚才的选择；
3. 选「跟随系统」后切系统外观，页面自动跟随（macOS/Windows/Linux 系统设置里改）；
4. DevTools Console 无 CSP violation。

- [ ] **Step 6: 提交**

```bash
cd /home/u/workspace/lanbox
git add frontend web
git commit -m "feat(web): 主题令牌与暗色/亮色/跟随系统三态切换"
```

---

## Task 3：基础库与反馈层（toast / confirm）

**Files:**
- Create: `frontend/src/lib/api.js`、`frontend/src/lib/format.js`、`frontend/src/lib/events.js`
- Create: `frontend/src/composables/useToast.js`、`frontend/src/composables/useConfirm.js`、`frontend/src/composables/useUi.js`
- Create: `frontend/src/components/ToastStack.vue`、`frontend/src/components/ConfirmDialog.vue`

- [ ] **Step 1: 写 `frontend/src/lib/api.js`**（逻辑从 `web/api.js` 平移，只改注释与导出）

```js
// 统一的 API 封装：把服务端错误体转换成带 code 的 ApiError。
export class ApiError extends Error {
  constructor(status, code, message) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function request(method, url, body) {
  const init = { method, credentials: 'same-origin', headers: {} };
  if (body !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(body);
  }
  const resp = await fetch(url, init);
  if (resp.status === 204) return null;
  const text = await resp.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      /* 非 JSON 响应 */
    }
  }
  if (!resp.ok) {
    const err = data && data.error ? data.error : { code: 'unknown', message: text || resp.statusText };
    throw new ApiError(resp.status, err.code, err.message);
  }
  return data;
}

export const api = {
  me: () => request('GET', '/api/me'),
  login: (username, password) => request('POST', '/api/login', { username, password }),
  logout: () => request('POST', '/api/logout'),

  list: ({ limit = 50, before = '', q = '', filter = 'all' } = {}) => {
    const p = new URLSearchParams({ limit: String(limit), filter });
    if (before) p.set('before', before);
    if (q) p.set('q', q);
    return request('GET', '/api/items?' + p.toString());
  },
  getItem: (id) => request('GET', '/api/items/' + encodeURIComponent(id)),
  setTaken: (id, taken) => request('PATCH', '/api/items/' + encodeURIComponent(id), { taken }),
  remove: (id) => request('DELETE', '/api/items/' + encodeURIComponent(id)),
  removeFile: (id, fid) =>
    request('DELETE', '/api/items/' + encodeURIComponent(id) + '/files/' + encodeURIComponent(fid)),

  users: () => request('GET', '/api/users'),
  createUser: (username, password, isAdmin) =>
    request('POST', '/api/users', { username, password, is_admin: isAdmin }),
  setUserPassword: (name, password) =>
    request('POST', '/api/users/' + encodeURIComponent(name) + '/password', { password }),
  deleteUser: (name) => request('DELETE', '/api/users/' + encodeURIComponent(name)),
};

// uploadItem 用 XHR 上传以获得真实进度（fetch 没有上传进度）。
export function uploadItem({ text, files, onProgress }) {
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    if (text) fd.append('text', text);
    for (const f of files) fd.append('files', f, f.name);

    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/items');
    xhr.withCredentials = true;
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && onProgress) onProgress(e.loaded, e.total);
    };
    xhr.onload = () => {
      let data = null;
      try {
        data = JSON.parse(xhr.responseText);
      } catch {
        /* 忽略非 JSON */
      }
      if (xhr.status >= 200 && xhr.status < 300 && data) {
        resolve(data);
        return;
      }
      const err = data && data.error ? data.error : { code: 'unknown', message: xhr.statusText };
      reject(new ApiError(xhr.status, err.code, err.message));
    };
    xhr.onerror = () => reject(new ApiError(0, 'network', '网络错误，上传中断'));
    xhr.onabort = () => reject(new ApiError(0, 'aborted', '上传已取消'));
    xhr.send(fd);
  });
}
```

- [ ] **Step 2: 写 `frontend/src/lib/format.js`**

```js
// 纯展示逻辑：字节、时间、头像。
const pad = (n) => String(n).padStart(2, '0');

export function fmtBytes(n) {
  const num = Number(n) || 0;
  if (num < 1024) return num + ' B';
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = num / 1024;
  let i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i += 1;
  }
  return value.toFixed(value >= 10 ? 0 : 1) + ' ' + units[i];
}

// 相对时间：刚刚 / 12 分钟前 / 3 小时前 / 昨天 18:03 / 11-20 09:41 / 2025-11-20 09:41
export function fmtRelative(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso || '';
  const diff = Date.now() - d.getTime();
  const minute = 60 * 1000;
  const hour = 60 * minute;
  const day = 24 * hour;
  if (diff < minute) return '刚刚';
  if (diff < hour) return Math.floor(diff / minute) + ' 分钟前';
  if (diff < day) return Math.floor(diff / hour) + ' 小时前';

  const hm = pad(d.getHours()) + ':' + pad(d.getMinutes());
  const yesterday = new Date();
  yesterday.setDate(yesterday.getDate() - 1);
  if (d.toDateString() === yesterday.toDateString()) return '昨天 ' + hm;

  const md = pad(d.getMonth() + 1) + '-' + pad(d.getDate());
  if (d.getFullYear() === new Date().getFullYear()) return md + ' ' + hm;
  return d.getFullYear() + '-' + md + ' ' + hm;
}

// 绝对时间，放到 title 里
export function fmtAbsolute(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso || '';
  return (
    d.getFullYear() +
    '-' +
    pad(d.getMonth() + 1) +
    '-' +
    pad(d.getDate()) +
    ' ' +
    pad(d.getHours()) +
    ':' +
    pad(d.getMinutes()) +
    ':' +
    pad(d.getSeconds())
  );
}

export function initial(name) {
  const s = (name || '?').trim();
  return s ? s.slice(0, 1).toUpperCase() : '?';
}

// 按用户名稳定地取一个色相，用于头像底色
export function avatarHue(name) {
  let hash = 0;
  for (const ch of name || '?') hash = (hash * 31 + ch.codePointAt(0)) % 360;
  return hash;
}
```

- [ ] **Step 3: 写 `frontend/src/lib/events.js`**

```js
// EventSource 包装：把事件转给回调，并上报连接状态。
export function connectEvents({ onEvent, onState }) {
  let es = null;
  let closed = false;

  function open() {
    if (closed) return;
    onState('connecting');
    es = new EventSource('/api/events', { withCredentials: true });

    es.addEventListener('open', () => onState('open'));
    es.addEventListener('item.created', (e) => onEvent('item.created', e.data));
    es.addEventListener('item.updated', (e) => onEvent('item.updated', e.data));
    es.addEventListener('item.deleted', (e) => onEvent('item.deleted', e.data));

    es.addEventListener('error', () => {
      // EventSource 自身会重连；只有被彻底关闭（如 401）时才手动重建
      if (es && es.readyState === EventSource.CLOSED) {
        onState('error');
        es.close();
        setTimeout(open, 3000);
      } else {
        onState('error');
      }
    });
  }

  open();

  return {
    close() {
      closed = true;
      if (es) es.close();
    },
  };
}
```

- [ ] **Step 4: 写 `frontend/src/composables/useToast.js`**

```js
import { reactive } from 'vue';

let seq = 0;
const toasts = reactive([]);

function dismiss(id) {
  const idx = toasts.findIndex((t) => t.id === id);
  if (idx >= 0) toasts.splice(idx, 1);
}

function push(message, kind = 'info', ttl = 4000) {
  const id = (seq += 1);
  toasts.push({ id, message: String(message || ''), kind });
  if (ttl > 0) setTimeout(() => dismiss(id), ttl);
  return id;
}

export function useToast() {
  return {
    toasts,
    dismiss,
    info: (message) => push(message, 'info'),
    success: (message) => push(message, 'success'),
    error: (message) => push(message, 'error', 6000),
  };
}
```

- [ ] **Step 5: 写 `frontend/src/composables/useConfirm.js`**

```js
import { reactive } from 'vue';

const state = reactive({
  open: false,
  title: '',
  message: '',
  confirmText: '确定',
  cancelText: '取消',
  danger: false,
});

let resolveFn = null;

// 用法：const ok = await confirmAction({ title, message, danger: true })
export function confirmAction(options = {}) {
  return new Promise((resolve) => {
    if (resolveFn) {
      const prev = resolveFn;
      resolveFn = null;
      prev(false);
    }
    state.open = true;
    state.title = options.title || '请确认';
    state.message = options.message || '';
    state.confirmText = options.confirmText || '确定';
    state.cancelText = options.cancelText || '取消';
    state.danger = Boolean(options.danger);
    resolveFn = resolve;
  });
}

export function settleConfirm(ok) {
  state.open = false;
  const fn = resolveFn;
  resolveFn = null;
  if (fn) fn(Boolean(ok));
}

export function useConfirm() {
  return { state, confirmAction, settleConfirm };
}
```

- [ ] **Step 6: 写 `frontend/src/composables/useUi.js`**

```js
import { ref } from 'vue';

// 顶栏按钮与抽屉、对话框之间共享的 UI 开关
const adminOpen = ref(false);

export function useUi() {
  return {
    adminOpen,
    openAdmin() {
      adminOpen.value = true;
    },
    closeAdmin() {
      adminOpen.value = false;
    },
  };
}
```

- [ ] **Step 7: 写 `frontend/src/components/ToastStack.vue`**

```vue
<script setup>
import { useToast } from '../composables/useToast.js';

const { toasts, dismiss } = useToast();
</script>

<template>
  <Teleport to="body">
    <div class="stack" role="status" aria-live="polite">
      <TransitionGroup name="toast">
        <div v-for="t in toasts" :key="t.id" class="toast" :class="t.kind">
          <span class="msg">{{ t.message }}</span>
          <button type="button" class="close" aria-label="关闭提示" @click="dismiss(t.id)">×</button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.stack {
  position: fixed;
  top: 16px;
  right: 16px;
  z-index: 60;
  display: grid;
  gap: 8px;
  width: min(360px, calc(100vw - 32px));
  pointer-events: none;
}

.toast {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-left: 3px solid var(--accent);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-2);
  font-size: 13px;
  pointer-events: auto;
}

.toast.success {
  border-left-color: var(--ok);
}

.toast.error {
  border-left-color: var(--danger);
}

.msg {
  flex: 1;
  word-break: break-word;
}

.close {
  border: 0;
  background: transparent;
  color: var(--text-faint);
  font-size: 16px;
  line-height: 1;
  cursor: pointer;
  padding: 0 2px;
}

.close:hover {
  color: var(--text);
}

.toast-enter-active,
.toast-leave-active {
  transition: opacity var(--dur) var(--ease), transform var(--dur) var(--ease);
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(-6px);
}
</style>
```

- [ ] **Step 8: 写 `frontend/src/components/ConfirmDialog.vue`**

```vue
<script setup>
import { nextTick, ref, watch } from 'vue';
import { settleConfirm, useConfirm } from '../composables/useConfirm.js';

const { state } = useConfirm();
const cancelBtn = ref(null);
const confirmBtn = ref(null);

watch(
  () => state.open,
  async (open) => {
    if (!open) return;
    await nextTick();
    // 危险操作默认聚焦「取消」，避免回车误删
    (state.danger ? cancelBtn.value : confirmBtn.value)?.focus();
  },
);

function onKeydown(event) {
  if (event.key === 'Escape') {
    event.stopPropagation();
    settleConfirm(false);
  }
}
</script>

<template>
  <Teleport to="body">
    <Transition name="dialog">
      <div v-if="state.open" class="wrap" @keydown="onKeydown">
        <div class="backdrop" @click="settleConfirm(false)" />
        <div class="dialog card" role="dialog" aria-modal="true" :aria-label="state.title">
          <h2 class="title">{{ state.title }}</h2>
          <p class="message">{{ state.message }}</p>
          <div class="actions">
            <button ref="cancelBtn" type="button" class="btn" @click="settleConfirm(false)">
              {{ state.cancelText }}
            </button>
            <button
              ref="confirmBtn"
              type="button"
              class="btn"
              :class="state.danger ? 'btn-danger' : 'btn-primary'"
              @click="settleConfirm(true)"
            >
              {{ state.confirmText }}
            </button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.wrap {
  position: fixed;
  inset: 0;
  z-index: 70;
  display: grid;
  place-items: center;
  padding: 16px;
}

.backdrop {
  position: absolute;
  inset: 0;
  background: var(--backdrop);
  backdrop-filter: blur(2px);
}

.dialog {
  position: relative;
  width: min(420px, 100%);
  padding: 20px;
  display: grid;
  gap: var(--gap-3);
  box-shadow: var(--shadow-2);
}

.title {
  font-size: 16px;
}

.message {
  color: var(--text-muted);
  font-size: 14px;
  white-space: pre-wrap;
}

.actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--gap-2);
  margin-top: var(--gap-1);
}

.dialog-enter-active,
.dialog-leave-active {
  transition: opacity var(--dur) var(--ease);
}

.dialog-enter-from,
.dialog-leave-to {
  opacity: 0;
}
</style>
```

- [ ] **Step 9: 在临时 App.vue 里验证 toast 与 confirm**

```vue
<script setup>
import { useToast } from './composables/useToast.js';
import { confirmAction } from './composables/useConfirm.js';
import ThemePicker from './components/ThemePicker.vue';
import ToastStack from './components/ToastStack.vue';
import ConfirmDialog from './components/ConfirmDialog.vue';

const toast = useToast();

async function demo() {
  toast.success('这是一条成功提示');
  const ok = await confirmAction({ title: '删除这条内容？', message: '删除后无法恢复。', confirmText: '删除', danger: true });
  toast.info(ok ? '已确认' : '已取消');
}
</script>

<template>
  <header style="display: flex; align-items: center; gap: 12px; padding: 16px">
    <strong>lanbox</strong>
    <span class="spacer" />
    <ThemePicker />
    <button class="btn" type="button" @click="demo">演示</button>
  </header>
  <ToastStack />
  <ConfirmDialog />
</template>
```

- [ ] **Step 10: 构建并验证（用键盘操作确认 Escape 生效）**

```bash
cd /home/u/workspace/lanbox/frontend && npm run build
cd .. && go run ./cmd/lanbox
```

浏览器点「演示」：toast 出现并在 4 秒后消失；对话框出现，`danger: true` 时焦点在「取消」上（点「确定」才执行），按 `Escape` 关闭；亮/暗主题下都清晰可读。

- [ ] **Step 11: 提交**

```bash
cd /home/u/workspace/lanbox
git add frontend web
git commit -m "feat(web): API/格式化/SSE 基础库与 toast、确认对话框"
```

---

## Task 4：登录与会话（App 骨架）

**Files:**
- Create: `frontend/src/composables/useSession.js`、`frontend/src/components/LoginView.vue`、`frontend/src/components/AppHeader.vue`
- Modify: `frontend/src/App.vue`（正式版骨架）

- [ ] **Step 1: 写 `frontend/src/composables/useSession.js`**

```js
import { ref } from 'vue';
import { api, ApiError } from '../lib/api.js';

const user = ref(null);
const ready = ref(false);
const error = ref('');

async function refresh() {
  error.value = '';
  try {
    user.value = await api.me();
  } catch (err) {
    user.value = null;
    if (!(err instanceof ApiError) || err.status !== 401) error.value = err.message;
  } finally {
    ready.value = true;
  }
}

async function login(username, password) {
  error.value = '';
  const me = await api.login(username, password);
  user.value = me;
  return me;
}

async function logout() {
  try {
    await api.logout();
  } catch {
    /* 网络失败也要把本地状态清掉 */
  }
  user.value = null;
}

export function useSession() {
  return { user, ready, error, refresh, login, logout };
}
```

- [ ] **Step 2: 写 `frontend/src/components/LoginView.vue`**

```vue
<script setup>
import { ref } from 'vue';
import { useSession } from '../composables/useSession.js';

const { login } = useSession();

const username = ref('');
const password = ref('');
const submitting = ref(false);
const error = ref('');

async function onSubmit() {
  if (submitting.value) return;
  submitting.value = true;
  error.value = '';
  try {
    await login(username.value.trim(), password.value);
    password.value = '';
  } catch (err) {
    error.value = err && err.message ? err.message : String(err);
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <div class="page">
    <form class="card login" @submit.prevent="onSubmit">
      <div class="logo">lan<span>box</span></div>
      <p class="muted slogan">共享的留言板与文件中转站</p>

      <label class="field">
        <span>用户名</span>
        <input v-model="username" class="input" autocomplete="username" required autofocus />
      </label>
      <label class="field">
        <span>密码</span>
        <input v-model="password" class="input" type="password" autocomplete="current-password" required />
      </label>

      <p v-if="error" class="error" role="alert">{{ error }}</p>

      <button class="btn btn-primary submit" type="submit" :disabled="submitting">
        {{ submitting ? '登录中…' : '登录' }}
      </button>
    </form>
  </div>
</template>

<style scoped>
.page {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: var(--gap-4);
}

.login {
  width: min(360px, 100%);
  padding: var(--gap-5);
  display: grid;
  gap: var(--gap-4);
}

.logo {
  font-size: 24px;
  font-weight: 650;
  letter-spacing: 0.5px;
}

.logo span {
  color: var(--accent);
}

.slogan {
  margin-top: calc(-1 * var(--gap-2));
}

.submit {
  min-height: 40px;
}
</style>
```

- [ ] **Step 3: 写 `frontend/src/components/AppHeader.vue`**

```vue
<script setup>
import ThemePicker from './ThemePicker.vue';
import { useSession } from '../composables/useSession.js';
import { useUi } from '../composables/useUi.js';
import { initial } from '../lib/format.js';

defineEmits(['logout']);

const { user } = useSession();
const { openAdmin } = useUi();
</script>

<template>
  <header class="topbar">
    <span class="brand">lan<span>box</span></span>

    <span class="who" v-if="user">
      <span class="avatar" aria-hidden="true">{{ initial(user.username) }}</span>
      <span class="name">{{ user.username }}</span>
      <span class="tag">{{ user.is_admin ? '管理员' : '成员' }}</span>
    </span>

    <span class="spacer" />

    <ThemePicker />

    <button v-if="user && user.is_admin" type="button" class="btn btn-sm" @click="openAdmin()">用户管理</button>
    <button type="button" class="btn btn-sm btn-ghost" @click="$emit('logout')">登出</button>
  </header>
</template>

<style scoped>
.topbar {
  position: sticky;
  top: 0;
  z-index: 10;
  display: flex;
  align-items: center;
  gap: var(--gap-3);
  padding: 10px clamp(12px, 3vw, 24px);
  background: var(--header-bg);
  border-bottom: 1px solid var(--line);
  backdrop-filter: blur(10px);
}

.brand {
  font-weight: 650;
  letter-spacing: 0.5px;
}

.brand span {
  color: var(--accent);
}

.who {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--text-muted);
}

.avatar {
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 12px;
  font-weight: 600;
}

.tag {
  padding: 0 6px;
  border: 1px solid var(--line);
  border-radius: var(--radius-pill);
  font-size: 11px;
}

@media (max-width: 640px) {
  .who .name,
  .who .tag {
    display: none;
  }
}
</style>
```

- [ ] **Step 4: 写正式版 `frontend/src/App.vue`（列表组件先用占位，Task 6 替换）**

```vue
<script setup>
import { onMounted } from 'vue';
import LoginView from './components/LoginView.vue';
import AppHeader from './components/AppHeader.vue';
import ToastStack from './components/ToastStack.vue';
import ConfirmDialog from './components/ConfirmDialog.vue';
import { useSession } from './composables/useSession.js';

const { user, ready, refresh, logout } = useSession();

onMounted(refresh);

async function onLogout() {
  await logout();
}
</script>

<template>
  <div v-if="!ready" class="boot" aria-busy="true">
    <span class="spinner" aria-hidden="true" />
    <span class="sr-only">加载中</span>
  </div>

  <LoginView v-else-if="!user" />

  <template v-else>
    <AppHeader @logout="onLogout" />
    <main class="main">
      <p class="muted">（发布区与列表在后续任务接入）</p>
    </main>
  </template>

  <ToastStack />
  <ConfirmDialog />
</template>

<style scoped>
.boot {
  min-height: 100vh;
  display: grid;
  place-items: center;
}

.spinner {
  width: 22px;
  height: 22px;
  border: 2px solid var(--line);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 700ms linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.main {
  max-width: var(--content-width);
  margin: 0 auto;
  padding: var(--gap-4) clamp(12px, 3vw, 24px) var(--gap-6);
  display: grid;
  gap: var(--gap-4);
}
</style>
```

- [ ] **Step 5: 构建并手工验证登录流程**

```bash
cd /home/u/workspace/lanbox/frontend && npm run build
cd .. && go run ./cmd/lanbox
```

预期：
1. 未登录 → 登录卡片，主题切换后登录页颜色也变；
2. 错误密码 → 表单内红字报错（不再跳 alert）；
3. 正确密码 → 顶栏出现头像/用户名/角色；
4. 刷新 → 仍是登录态（cookie 生效）；
5. 点「登出」→ 回到登录页。

- [ ] **Step 6: 提交**

```bash
cd /home/u/workspace/lanbox
git add frontend web
git commit -m "feat(web): 登录视图、会话状态与顶栏"
```

---

## Task 5：列表数据层（useItems：分页/筛选/搜索/SSE）

**Files:**
- Create: `frontend/src/composables/useItems.js`

- [ ] **Step 1: 写 `frontend/src/composables/useItems.js`**

```js
import { computed, ref } from 'vue';
import { api } from '../lib/api.js';
import { connectEvents } from '../lib/events.js';

const list = ref([]);
const cursor = ref('');
const filter = ref('all');
const query = ref('');
const loading = ref(false);
const loadingMore = ref(false);
const error = ref('');
const sseState = ref('idle');
const loaded = ref(false);

let events = null;
let searchTimer = null;

const hasMore = computed(() => Boolean(cursor.value));
const isEmpty = computed(() => loaded.value && !loading.value && list.value.length === 0);

function sortDesc(items) {
  return [...items].sort((a, b) => (a.id < b.id ? 1 : a.id > b.id ? -1 : 0));
}

function dedupe(items) {
  const seen = new Set();
  const out = [];
  for (const item of items) {
    if (seen.has(item.id)) continue;
    seen.add(item.id);
    out.push(item);
  }
  return out;
}

function applyPage(res, append) {
  const incoming = res.items || [];
  const merged = append ? [...list.value, ...incoming] : incoming;
  list.value = sortDesc(dedupe(merged));
  cursor.value = res.next_cursor || '';
  loaded.value = true;
}

async function loadFirst() {
  loading.value = true;
  error.value = '';
  try {
    applyPage(await api.list({ filter: filter.value, q: query.value }), false);
  } catch (err) {
    error.value = err && err.message ? err.message : String(err);
    throw err;
  } finally {
    loading.value = false;
  }
}

async function loadMore() {
  if (!cursor.value || loadingMore.value) return;
  loadingMore.value = true;
  try {
    applyPage(await api.list({ filter: filter.value, q: query.value, before: cursor.value }), true);
  } catch (err) {
    error.value = err && err.message ? err.message : String(err);
  } finally {
    loadingMore.value = false;
  }
}

function upsert(item) {
  list.value = sortDesc(dedupe([item, ...list.value.filter((it) => it.id !== item.id)]));
}

function removeById(id) {
  list.value = list.value.filter((it) => it.id !== id);
}

async function toggleTaken(item) {
  const updated = await api.setTaken(item.id, !item.taken);
  if (filter.value === 'todo' && updated.taken) removeById(updated.id);
  else upsert(updated);
  return updated;
}

async function removeItem(item) {
  await api.remove(item.id);
  removeById(item.id);
}

async function removeFile(item, file) {
  await api.removeFile(item.id, file.id);
  upsert(await api.getItem(item.id));
}

function setFilter(next) {
  if (filter.value === next) return;
  filter.value = next;
  loadFirst().catch(() => {});
}

function setQuery(next) {
  query.value = next;
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    loadFirst().catch(() => {});
  }, 300);
}

function connect() {
  if (events) return;
  events = connectEvents({
    onState(state) {
      sseState.value = state;
    },
    onEvent(type, data) {
      let payload = null;
      try {
        payload = JSON.parse(data);
      } catch {
        return;
      }
      if (type === 'item.deleted' && payload.id) {
        removeById(payload.id);
        return;
      }
      if (!payload.item) return;
      if (query.value) {
        // 有搜索条件时以服务端命中为准
        loadFirst().catch(() => {});
        return;
      }
      if (filter.value === 'todo' && payload.item.taken) {
        removeById(payload.item.id);
        return;
      }
      upsert(payload.item);
    },
  });
}

function disconnect() {
  if (events) {
    events.close();
    events = null;
  }
  sseState.value = 'idle';
}

function reset() {
  disconnect();
  list.value = [];
  cursor.value = '';
  filter.value = 'all';
  query.value = '';
  loading.value = false;
  loadingMore.value = false;
  error.value = '';
  loaded.value = false;
}

export function useItems() {
  return {
    list,
    cursor,
    filter,
    query,
    loading,
    loadingMore,
    error,
    sseState,
    loaded,
    hasMore,
    isEmpty,
    loadFirst,
    loadMore,
    toggleTaken,
    removeItem,
    removeFile,
    setFilter,
    setQuery,
    connect,
    disconnect,
    reset,
  };
}
```

- [ ] **Step 2: 把 `connect()` 接到 App 上**

用以下内容替换 `frontend/src/App.vue` 的整个 `<script setup>` 块：

```vue
<script setup>
import { onMounted } from 'vue';
import LoginView from './components/LoginView.vue';
import AppHeader from './components/AppHeader.vue';
import ToastStack from './components/ToastStack.vue';
import ConfirmDialog from './components/ConfirmDialog.vue';
import { useSession } from './composables/useSession.js';
import { useItems } from './composables/useItems.js';

const { user, ready, refresh, logout } = useSession();
const items = useItems();

onMounted(async () => {
  await refresh();
  if (user.value) {
    items.loadFirst().catch(() => {});
    items.connect();
  }
});

async function onLogout() {
  items.reset();
  await logout();
}
</script>
```

模板部分不变。

- [ ] **Step 3: 给顶栏加实时连接指示（`AppHeader.vue`）**

在 `<script setup>` 顶部加一行 import：

```js
import { computed } from 'vue';
```

并在 `const { openAdmin } = useUi();` 之后加：

```js
import { useItems } from '../composables/useItems.js';   // 与其它 import 放在一起

const { sseState } = useItems();

const sseLabel = computed(() => {
  if (sseState.value === 'open') return '实时已连接';
  if (sseState.value === 'connecting') return '连接中…';
  if (sseState.value === 'error') return '连接断开，重试中';
  return '';
});
```

（import 语句必须写在文件顶部 import 区，不要真的写在 `const` 后面。）

在模板里 `<span class="spacer" />` **之前**插入：

```html
    <span v-if="sseLabel" class="sse" :class="sseState" :title="sseLabel" aria-live="polite">
      <span class="dot" aria-hidden="true" />
      <span class="sse-text">{{ sseLabel }}</span>
    </span>
```

在 `<style scoped>` 里 `.spacer` 之后（媒体查询之前）插入：

```css
.sse {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-muted);
}

.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--text-faint);
}

.sse.open .dot {
  background: var(--ok);
  box-shadow: 0 0 0 3px var(--ok-soft);
}

.sse.error .dot {
  background: var(--danger);
  box-shadow: 0 0 0 3px var(--danger-soft);
}
```

并把媒体查询里的 `.who .name, .who .tag` 规则改为：

```css
@media (max-width: 640px) {
  .who .name,
  .who .tag,
  .sse-text {
    display: none;
  }
}
```

- [ ] **Step 4: 验证 SSE 与登录联动**

终端 A：`cd /home/u/workspace/lanbox && go run ./cmd/lanbox`
终端 B：跑 `cd frontend && npm run build` 把最新界面嵌进去

本步只确认连接状态；发布/删除后的实时联动在 Task 7 Step 4 的清单里一并验证。

```bash
cd /home/u/workspace/lanbox/frontend && npm run build
```

浏览器：登录后顶栏圆点变绿并显示“实时已连接”；停掉服务 → 变成“连接断开，重试中”；重启服务 → 3 秒内自动恢复绿色。Console 无报错（`/api/events` 401/断开期间的错误日志可忽略）。

- [ ] **Step 5: 提交**

```bash
cd /home/u/workspace/lanbox
git add frontend web
git commit -m "feat(web): 列表数据层 useItems（分页/筛选/搜索/SSE 合流）"
```

---

## Task 6：卡片与列表 UI

**Files:**
- Create: `frontend/src/components/ItemCard.vue`、`frontend/src/components/ItemList.vue`
- Modify: `frontend/src/App.vue`（插入 `<ItemList />`）

- [ ] **Step 1: 写 `frontend/src/components/ItemCard.vue`**

```vue
<script setup>
import { avatarHue, fmtAbsolute, fmtBytes, fmtRelative, initial } from '../lib/format.js';

const props = defineProps({
  item: { type: Object, required: true },
});

defineEmits(['toggle', 'delete', 'delete-file']);
</script>

<template>
  <article class="card item" :class="{ taken: item.taken }">
    <header class="head">
      <span class="avatar" :style="{ '--hue': avatarHue(item.created_by) + 'deg' }" aria-hidden="true">
        {{ initial(item.created_by) }}
      </span>
      <div class="who">
        <span class="name">{{ item.created_by || '未知' }}</span>
        <span class="ip">{{ item.source_ip || '未知来源' }}</span>
      </div>
      <span class="spacer" />
      <span v-if="item.taken" class="taken-tag">已取走</span>
      <time class="time" :datetime="item.created_at" :title="fmtAbsolute(item.created_at)">
        {{ fmtRelative(item.created_at) }}
      </time>
    </header>

    <p v-if="item.text" class="text">{{ item.text }}</p>

    <ul v-if="item.files && item.files.length" class="files">
      <li v-for="f in item.files" :key="f.id" class="file">
        <svg class="icon" viewBox="0 0 20 20" aria-hidden="true">
          <path
            d="M5 2.5h6l4 4v11a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1v-14a1 1 0 0 1 1-1Z"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
          />
          <path d="M11 2.5v4h4" fill="none" stroke="currentColor" stroke-width="1.5" />
        </svg>
        <span class="fname" :title="f.name">{{ f.name }}</span>
        <span class="fmeta">
          {{ fmtBytes(f.size) }} · {{ (f.sha256 || '').slice(0, 8) }} · 下载 {{ f.downloads }} 次
        </span>
        <a class="btn btn-sm download" :href="`/api/items/${encodeURIComponent(item.id)}/files/${encodeURIComponent(f.id)}`" :download="f.name">下载</a>
        <button type="button" class="btn btn-sm btn-danger" @click="$emit('delete-file', f)">删除</button>
      </li>
    </ul>

    <footer class="actions">
      <button type="button" class="btn btn-sm" @click="$emit('toggle')">
        {{ item.taken ? '取消取走' : '标记已取走' }}
      </button>
      <span class="spacer" />
      <button type="button" class="btn btn-sm btn-danger" @click="$emit('delete')">删除</button>
    </footer>
  </article>
</template>

<style scoped>
.item {
  padding: var(--gap-4);
  display: grid;
  gap: var(--gap-3);
  transition: opacity var(--dur) var(--ease), border-color var(--dur) var(--ease);
}

.item:hover {
  border-color: var(--line-strong);
}

.item.taken {
  opacity: 0.62;
}

.head {
  display: flex;
  align-items: center;
  gap: var(--gap-2);
  font-size: 12px;
  color: var(--text-muted);
}

.avatar {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  flex: none;
  border-radius: 50%;
  font-size: 12px;
  font-weight: 600;
  color: hsl(var(--hue) 70% 78%);
  background: hsl(var(--hue) 60% 22%);
}

:root[data-theme='light'] .avatar {
  color: hsl(var(--hue) 65% 30%);
  background: hsl(var(--hue) 80% 92%);
}

.who {
  display: flex;
  flex-direction: column;
  line-height: 1.35;
}

.name {
  color: var(--text);
  font-size: 13px;
  font-weight: 550;
}

.ip {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--text-faint);
}

.time {
  white-space: nowrap;
}

.taken-tag {
  padding: 1px 8px;
  border: 1px solid var(--ok-line);
  border-radius: var(--radius-pill);
  background: var(--ok-soft);
  color: var(--ok);
  font-size: 11px;
}

.text {
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 14.5px;
}

.files {
  display: grid;
  gap: var(--gap-2);
}

.file {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--gap-2);
  padding: 7px 10px;
  background: var(--surface-sunken);
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  font-size: 13px;
}

.icon {
  width: 16px;
  height: 16px;
  flex: none;
  color: var(--text-faint);
}

.fname {
  flex: 1 1 160px;
  min-width: 0;
  overflow-wrap: anywhere;
}

.fmeta {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--text-faint);
  white-space: nowrap;
}

.download {
  text-decoration: none;
}

.download:hover {
  text-decoration: none;
}

.actions {
  display: flex;
  align-items: center;
  gap: var(--gap-2);
  padding-top: var(--gap-1);
  border-top: 1px solid var(--line);
}

@media (max-width: 560px) {
  .file .fmeta {
    order: 3;
    width: 100%;
  }
}
</style>
```

- [ ] **Step 2: 写 `frontend/src/components/ItemList.vue`**

```vue
<script setup>
import ItemCard from './ItemCard.vue';
import { useItems } from '../composables/useItems.js';
import { useToast } from '../composables/useToast.js';
import { confirmAction } from '../composables/useConfirm.js';

const {
  list,
  filter,
  query,
  loading,
  loadingMore,
  error,
  hasMore,
  isEmpty,
  loadMore,
  toggleTaken,
  removeItem,
  removeFile,
  setFilter,
  setQuery,
} = useItems();

const toast = useToast();

async function onToggle(item) {
  try {
    await toggleTaken(item);
  } catch (err) {
    toast.error(err.message);
  }
}

async function onDelete(item) {
  const extra = item.files && item.files.length ? `\n将同时删除 ${item.files.length} 个附件。` : '';
  const ok = await confirmAction({
    title: '删除这条内容？',
    message: `删除后无法恢复。${extra}`,
    confirmText: '删除',
    danger: true,
  });
  if (!ok) return;
  try {
    await removeItem(item);
    toast.success('已删除');
  } catch (err) {
    toast.error(err.message);
  }
}

async function onDeleteFile(item, file) {
  const ok = await confirmAction({
    title: '删除附件？',
    message: `「${file.name}」删除后无法恢复。`,
    confirmText: '删除',
    danger: true,
  });
  if (!ok) return;
  try {
    await removeFile(item, file);
    toast.success('附件已删除');
  } catch (err) {
    toast.error(err.message);
  }
}

function onSearch(event) {
  setQuery(event.target.value);
}
</script>

<template>
  <section class="list">
    <div class="toolbar">
      <div class="segmented" role="group" aria-label="筛选">
        <button type="button" class="seg" :class="{ active: filter === 'all' }" :aria-pressed="filter === 'all'" @click="setFilter('all')">全部</button>
        <button type="button" class="seg" :class="{ active: filter === 'todo' }" :aria-pressed="filter === 'todo'" @click="setFilter('todo')">未取走</button>
      </div>

      <label class="search">
        <svg viewBox="0 0 20 20" aria-hidden="true">
          <circle cx="8.5" cy="8.5" r="5" fill="none" stroke="currentColor" stroke-width="1.6" />
          <path d="m12.5 12.5 4 4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
        </svg>
        <input class="input" type="search" :value="query" placeholder="搜索文字或文件名" @input="onSearch" />
      </label>
    </div>

    <p v-if="error" class="error" role="alert">{{ error }}</p>

    <div v-if="loading && !list.length" class="skeleton">
      <div v-for="i in 3" :key="i" class="card sk">
        <div class="sk-line w30" />
        <div class="sk-line w90" />
        <div class="sk-line w60" />
      </div>
    </div>

    <div v-else-if="isEmpty" class="empty card">
      <svg viewBox="0 0 64 64" aria-hidden="true">
        <rect x="10" y="18" width="44" height="30" rx="4" fill="none" stroke="currentColor" stroke-width="2" />
        <path d="M18 30h20M18 38h14" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
      </svg>
      <p class="empty-title">{{ query ? '没有匹配的内容' : '这里还没有内容' }}</p>
      <p class="muted">{{ query ? '换个关键词试试' : '在上方输入文字或拖入文件，发布第一条' }}</p>
    </div>

    <div v-else class="cards">
      <ItemCard
        v-for="item in list"
        :key="item.id"
        :item="item"
        @toggle="onToggle(item)"
        @delete="onDelete(item)"
        @delete-file="(f) => onDeleteFile(item, f)"
      />
    </div>

    <div v-if="hasMore" class="more">
      <button type="button" class="btn" :disabled="loadingMore" @click="loadMore()">
        {{ loadingMore ? '加载中…' : '加载更多' }}
      </button>
    </div>
  </section>
</template>

<style scoped>
.list {
  display: grid;
  gap: var(--gap-3);
}

.toolbar {
  display: flex;
  align-items: center;
  gap: var(--gap-3);
  flex-wrap: wrap;
}

.segmented {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  background: var(--surface-sunken);
  border: 1px solid var(--line);
  border-radius: var(--radius-pill);
}

.seg {
  padding: 5px 14px;
  border: 0;
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--text-muted);
  font-size: 13px;
  cursor: pointer;
  transition: background var(--dur) var(--ease), color var(--dur) var(--ease);
}

.seg:hover {
  color: var(--text);
}

.seg.active {
  background: var(--surface);
  color: var(--text);
  box-shadow: var(--shadow-1);
}

.search {
  display: flex;
  align-items: center;
  gap: var(--gap-2);
  margin-left: auto;
  width: min(280px, 100%);
  padding: 0 10px;
  background: var(--surface-sunken);
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  color: var(--text-faint);
}

.search:focus-within {
  border-color: var(--accent-line);
  box-shadow: 0 0 0 3px var(--accent-soft);
}

.search svg {
  width: 15px;
  height: 15px;
  flex: none;
}

.search .input {
  border: 0;
  background: transparent;
  padding: 8px 0;
  box-shadow: none;
}

.search .input:focus {
  box-shadow: none;
}

.cards,
.skeleton {
  display: grid;
  gap: var(--gap-3);
}

.sk {
  padding: var(--gap-4);
  display: grid;
  gap: 10px;
}

.sk-line {
  height: 12px;
  border-radius: var(--radius-sm);
  background: linear-gradient(90deg, var(--surface-sunken), var(--surface-hover), var(--surface-sunken));
  background-size: 200% 100%;
  animation: shimmer 1.4s infinite linear;
}

.sk-line.w30 {
  width: 30%;
}

.sk-line.w60 {
  width: 60%;
}

.sk-line.w90 {
  width: 90%;
}

@keyframes shimmer {
  from {
    background-position: 200% 0;
  }
  to {
    background-position: -200% 0;
  }
}

.empty {
  padding: var(--gap-6) var(--gap-4);
  display: grid;
  justify-items: center;
  gap: var(--gap-2);
  text-align: center;
  color: var(--text-faint);
}

.empty svg {
  width: 56px;
  height: 56px;
  opacity: 0.6;
}

.empty-title {
  color: var(--text);
  font-size: 14px;
}

.more {
  display: grid;
  place-items: center;
  padding: var(--gap-2) 0 var(--gap-5);
}
</style>
```

- [ ] **Step 3: 把 `<ItemList />` 插到 `App.vue` 的 `<main>` 里**

```vue
    <main class="main">
      <ItemList />
    </main>
```

并在 `<script setup>` 增加 `import ItemList from './components/ItemList.vue';`。

- [ ] **Step 4: 构建并验证列表行为**

```bash
cd /home/u/workspace/lanbox/frontend && npm run build
cd .. && go run ./cmd/lanbox
```

验证清单：
1. 列表有数据：头像、用户名、IP、相对时间；附件行显示大小/sha/下载次数；
2. 点「下载」按钮正常下载（浏览器按 `Content-Disposition` 存盘）；
3. 点「标记已取走」→ 卡片变淡 + 绿标；「未取走」筛选下立即消失；
4. 点「删除」→ 自定义对话框（不是浏览器 confirm）→ 确认后卡片消失 + toast；
5. 搜索关键词 300ms 后出结果；无结果显示空状态文案；
6. 手机宽度（DevTools 375px）无横向滚动。

- [ ] **Step 5: 提交**

```bash
cd /home/u/workspace/lanbox
git add frontend web
git commit -m "feat(web): 卡片化列表、空状态、骨架屏与筛选搜索"
```

---

## Task 7：发布区与上传（ComposerCard + useUpload）

**Files:**
- Create: `frontend/src/composables/useUpload.js`、`frontend/src/components/ComposerCard.vue`
- Modify: `frontend/src/App.vue`（插入 `<ComposerCard />`）

- [ ] **Step 1: 写 `frontend/src/composables/useUpload.js`**

```js
import { computed, ref } from 'vue';
import { uploadItem } from '../lib/api.js';

const text = ref('');
const files = ref([]);
const uploading = ref(false);
const percent = ref(0);
const loadedBytes = ref(0);
const totalBytes = ref(0);
const error = ref('');

const totalSize = computed(() => files.value.reduce((n, f) => n + f.size, 0));
const canSend = computed(() => !uploading.value && (text.value.trim() !== '' || files.value.length > 0));

function addFiles(incoming) {
  const next = Array.from(incoming || []);
  if (next.length) files.value = [...files.value, ...next];
}

function removeAt(index) {
  files.value = files.value.filter((_, i) => i !== index);
}

function reset() {
  text.value = '';
  files.value = [];
  percent.value = 0;
  loadedBytes.value = 0;
  totalBytes.value = 0;
  error.value = '';
}

async function submit() {
  if (!canSend.value) return null;
  error.value = '';
  uploading.value = true;
  percent.value = 0;
  try {
    const created = await uploadItem({
      text: text.value.trim(),
      files: files.value,
      onProgress(loaded, total) {
        loadedBytes.value = loaded;
        totalBytes.value = total;
        percent.value = total > 0 ? Math.round((loaded / total) * 100) : 0;
      },
    });
    reset();
    return created;
  } catch (err) {
    // 失败时保留文字与文件，方便重试
    error.value = err && err.message ? err.message : String(err);
    throw err;
  } finally {
    uploading.value = false;
  }
}

export function useUpload() {
  return {
    text,
    files,
    uploading,
    percent,
    loadedBytes,
    totalBytes,
    totalSize,
    canSend,
    error,
    addFiles,
    removeAt,
    reset,
    submit,
  };
}
```

- [ ] **Step 2: 写 `frontend/src/components/ComposerCard.vue`**

```vue
<script setup>
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { useUpload } from '../composables/useUpload.js';
import { useItems } from '../composables/useItems.js';
import { useToast } from '../composables/useToast.js';
import { fmtBytes } from '../lib/format.js';

const { text, files, uploading, percent, loadedBytes, totalBytes, totalSize, canSend, error, addFiles, removeAt, submit } =
  useUpload();
const { loadFirst } = useItems();
const toast = useToast();

const dragging = ref(false);
const fileInput = ref(null);

function onPick(event) {
  addFiles(event.target.files);
  event.target.value = '';
}

function onDrop(event) {
  dragging.value = false;
  if (event.dataTransfer && event.dataTransfer.files.length) addFiles(event.dataTransfer.files);
}

function onPaste(event) {
  const pasted = [...((event.clipboardData && event.clipboardData.files) || [])];
  if (!pasted.length) return;
  event.preventDefault();
  addFiles(pasted);
}

function onKeydown(event) {
  if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
    event.preventDefault();
    onSubmit();
  }
}

async function onSubmit() {
  if (!canSend.value) return;
  try {
    await submit();
    toast.success('已发送');
    await loadFirst();
  } catch (err) {
    toast.error('发送失败：' + err.message);
  }
}

onMounted(() => document.addEventListener('paste', onPaste));
onBeforeUnmount(() => document.removeEventListener('paste', onPaste));
</script>

<template>
  <form
    class="card composer"
    :class="{ dragging }"
    @submit.prevent="onSubmit"
    @dragover.prevent="dragging = true"
    @dragleave.self="dragging = false"
    @drop.prevent="onDrop"
  >
    <textarea
      v-model="text"
      class="input text"
      rows="3"
      placeholder="请输入文字或文件…（可拖入文件、Ctrl+V 粘贴截图）"
      :disabled="uploading"
      @keydown="onKeydown"
    />

    <ul v-if="files.length" class="pending">
      <li v-for="(f, i) in files" :key="f.name + i">
        <span class="pname" :title="f.name">{{ f.name }}</span>
        <span class="psize">{{ fmtBytes(f.size) }}</span>
        <span class="spacer" />
        <button type="button" class="btn btn-sm btn-ghost" :disabled="uploading" @click="removeAt(i)">移除</button>
      </li>
    </ul>

    <div class="bar">
      <button type="button" class="btn" :disabled="uploading" @click="fileInput.click()">选择文件</button>
      <input ref="fileInput" class="sr-only" type="file" multiple @change="onPick" />
      <span v-if="files.length" class="muted">待上传 {{ files.length }} 个文件，共 {{ fmtBytes(totalSize) }}</span>
      <span class="spacer" />
      <span class="muted hint-key">Ctrl + Enter 发送</span>
      <button type="submit" class="btn btn-primary" :disabled="!canSend">
        {{ uploading ? '发送中…' : '发送' }}
      </button>
    </div>

    <div v-if="uploading" class="progress" role="progressbar" :aria-valuenow="percent" aria-valuemin="0" aria-valuemax="100">
      <div class="fill" :style="{ width: percent + '%' }" />
      <span class="ptext">{{ percent }}%（{{ fmtBytes(loadedBytes) }} / {{ fmtBytes(totalBytes) }}）</span>
    </div>

    <p v-if="error" class="error" role="alert">{{ error }}</p>

    <div v-if="dragging" class="drop-hint">松开即可添加文件</div>
  </form>
</template>

<style scoped>
.composer {
  position: relative;
  padding: var(--gap-4);
  display: grid;
  gap: var(--gap-3);
  transition: border-color var(--dur) var(--ease);
}

.composer.dragging {
  border-color: var(--accent);
}

.text {
  border: 0;
  background: transparent;
  padding: 0;
  min-height: 84px;
  max-height: 220px;
  resize: none;
  font-size: 15px;
}

.text:focus {
  box-shadow: none;
}

.pending {
  display: grid;
  gap: 6px;
}

.pending li {
  display: flex;
  align-items: center;
  gap: var(--gap-2);
  padding: 6px 10px;
  border: 1px dashed var(--line);
  border-radius: var(--radius-sm);
  font-size: 13px;
  color: var(--text-muted);
}

.pname {
  overflow-wrap: anywhere;
}

.psize {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--text-faint);
  white-space: nowrap;
}

.bar {
  display: flex;
  align-items: center;
  gap: var(--gap-2);
  flex-wrap: wrap;
}

.progress {
  position: relative;
  height: 22px;
  background: var(--surface-sunken);
  border: 1px solid var(--line);
  border-radius: var(--radius-pill);
  overflow: hidden;
}

.fill {
  height: 100%;
  background: var(--accent);
  transition: width 150ms linear;
}

.ptext {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  font-size: 12px;
  color: var(--accent-contrast);
  mix-blend-mode: difference;
}

.drop-hint {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  border: 2px dashed var(--accent);
  border-radius: var(--radius-lg);
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 13px;
  pointer-events: none;
}

@media (max-width: 560px) {
  .hint-key {
    display: none;
  }
}
</style>
```

- [ ] **Step 3: 把 `<ComposerCard />` 插到 `App.vue` 的列表之前**

```vue
    <main class="main">
      <ComposerCard />
      <ItemList />
    </main>
```

并 `import ComposerCard from './components/ComposerCard.vue';`。

- [ ] **Step 4: 验证发布流程（两个浏览器窗口各登录一个账号）**

```bash
cd /home/u/workspace/lanbox/frontend && npm run build
cd .. && go run ./cmd/lanbox
```

清单：
1. 输入文字 → `Ctrl+Enter` 发送成功，列表立刻出现新条目，另一窗口 1 秒内出现（SSE）；
2. 选择多个文件 → chip 显示名称/大小，可移除；
3. 拖文件到卡片 → 卡片高亮 + 覆盖提示，松手后进入待上传；
4. 截图后 `Ctrl+V` → 进入待上传；
5. 传一个 100MB 文件 → 进度条百分比与字节数持续更新，内存不涨（任务管理器看一下即可）；
6. 传超限文件（配置 `max_file_size_mb` 调小后测）→ 红字错误 + toast，文字与已选文件仍在，可直接重试；
7. 无文字无文件时「发送」按钮禁用。

- [ ] **Step 5: 提交**

```bash
cd /home/u/workspace/lanbox
git add frontend web
git commit -m "feat(web): 发布区（拖拽/粘贴/进度）与上传状态管理"
```

---

## Task 8：用户管理抽屉

**Files:**
- Create: `frontend/src/components/AdminDrawer.vue`
- Modify: `frontend/src/App.vue`（挂载 `<AdminDrawer />`）

- [ ] **Step 1: 写 `frontend/src/components/AdminDrawer.vue`**

```vue
<script setup>
import { onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue';
import { api } from '../lib/api.js';
import { useUi } from '../composables/useUi.js';
import { useToast } from '../composables/useToast.js';
import { confirmAction } from '../composables/useConfirm.js';

const { adminOpen, closeAdmin } = useUi();
const toast = useToast();

const users = ref([]);
const loading = ref(false);
const error = ref('');
const creating = ref(false);
const form = reactive({ username: '', password: '', isAdmin: false });
const resetFor = ref('');
const resetPassword = ref('');
const closeBtn = ref(null);

async function loadUsers() {
  loading.value = true;
  error.value = '';
  try {
    users.value = await api.users();
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}

async function onCreate() {
  if (creating.value) return;
  creating.value = true;
  error.value = '';
  try {
    await api.createUser(form.username.trim(), form.password, form.isAdmin);
    form.username = '';
    form.password = '';
    form.isAdmin = false;
    toast.success('用户已创建');
    await loadUsers();
  } catch (err) {
    error.value = err.message;
  } finally {
    creating.value = false;
  }
}

function startReset(name) {
  resetFor.value = name;
  resetPassword.value = '';
}

async function commitReset(user) {
  if (!resetPassword.value) return;
  error.value = '';
  try {
    await api.setUserPassword(user.username, resetPassword.value);
    resetFor.value = '';
    resetPassword.value = '';
    toast.success(`已重置 ${user.username} 的密码`);
  } catch (err) {
    error.value = err.message;
  }
}

async function onDelete(user) {
  const ok = await confirmAction({
    title: '删除用户？',
    message: `将删除「${user.username}」，其已发布的内容保留。`,
    confirmText: '删除',
    danger: true,
  });
  if (!ok) return;
  error.value = '';
  try {
    await api.deleteUser(user.username);
    toast.success('用户已删除');
    await loadUsers();
  } catch (err) {
    error.value = err.message;
  }
}

function onKeydown(event) {
  if (event.key === 'Escape') closeAdmin();
}

watch(adminOpen, async (open) => {
  if (open) {
    await loadUsers();
    requestAnimationFrame(() => closeBtn.value?.focus());
  }
});

onMounted(() => document.addEventListener('keydown', onKeydown));
onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown));
</script>

<template>
  <Teleport to="body">
    <Transition name="drawer">
      <div v-if="adminOpen" class="shell">
        <div class="backdrop" @click="closeAdmin()" />
        <aside class="panel" role="dialog" aria-modal="true" aria-label="用户管理">
          <header class="head">
            <strong>用户管理</strong>
            <button ref="closeBtn" type="button" class="btn btn-sm btn-ghost" @click="closeAdmin()">关闭</button>
          </header>

          <form class="card block" @submit.prevent="onCreate">
            <label class="field">
              <span>用户名</span>
              <input v-model="form.username" class="input" autocomplete="off" required />
            </label>
            <label class="field">
              <span>密码（至少 8 位）</span>
              <input v-model="form.password" class="input" type="password" autocomplete="new-password" minlength="8" required />
            </label>
            <label class="check">
              <input v-model="form.isAdmin" type="checkbox" />
              <span>管理员</span>
            </label>
            <button type="submit" class="btn btn-primary" :disabled="creating">{{ creating ? '创建中…' : '新增用户' }}</button>
          </form>

          <p v-if="error" class="error" role="alert">{{ error }}</p>

          <ul v-if="users.length" class="users">
            <li v-for="u in users" :key="u.username" class="user">
              <div class="row">
                <span class="uname">{{ u.username }}</span>
                <span class="tag">{{ u.is_admin ? '管理员' : '成员' }}</span>
                <span class="spacer" />
                <button type="button" class="btn btn-sm" @click="startReset(u.username)">重置密码</button>
                <button type="button" class="btn btn-sm btn-danger" @click="onDelete(u)">删除</button>
              </div>
              <form v-if="resetFor === u.username" class="reset" @submit.prevent="commitReset(u)">
                <input v-model="resetPassword" class="input" type="password" placeholder="新密码（至少 8 位）" minlength="8" required autofocus />
                <button type="submit" class="btn btn-sm btn-primary">保存</button>
                <button type="button" class="btn btn-sm btn-ghost" @click="resetFor = ''">取消</button>
              </form>
            </li>
          </ul>
          <p v-else-if="loading" class="muted">加载中…</p>
          <p v-else class="muted">还没有其他用户。</p>
        </aside>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.shell {
  position: fixed;
  inset: 0;
  z-index: 40;
}

.backdrop {
  position: absolute;
  inset: 0;
  background: var(--backdrop);
}

.panel {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: min(440px, 100%);
  padding: var(--gap-4);
  display: grid;
  gap: var(--gap-3);
  align-content: start;
  overflow: auto;
  background: var(--bg-soft);
  border-left: 1px solid var(--line);
  box-shadow: var(--shadow-2);
}

.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.block {
  padding: var(--gap-4);
  display: grid;
  gap: var(--gap-3);
}

.check {
  display: flex;
  align-items: center;
  gap: var(--gap-2);
  font-size: 13px;
  color: var(--text-muted);
}

.users {
  display: grid;
  gap: var(--gap-2);
}

.user {
  display: grid;
  gap: var(--gap-2);
  padding: 8px 10px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  font-size: 13px;
}

.row {
  display: flex;
  align-items: center;
  gap: var(--gap-2);
  flex-wrap: wrap;
}

.uname {
  font-weight: 550;
}

.tag {
  padding: 0 6px;
  border: 1px solid var(--line);
  border-radius: var(--radius-pill);
  color: var(--text-muted);
  font-size: 11px;
}

.reset {
  display: flex;
  gap: var(--gap-2);
  align-items: center;
}

.reset .input {
  flex: 1;
  min-width: 0;
}

.drawer-enter-active .panel,
.drawer-leave-active .panel {
  transition: transform var(--dur) var(--ease);
}

.drawer-enter-from .panel,
.drawer-leave-to .panel {
  transform: translateX(100%);
}

.drawer-enter-active,
.drawer-leave-active {
  transition: opacity var(--dur) var(--ease);
}

.drawer-enter-from,
.drawer-leave-to {
  opacity: 0;
}

@media (max-width: 560px) {
  .panel {
    width: 100%;
    border-left: 0;
  }
}
</style>
```

- [ ] **Step 2: 在 `App.vue` 挂载抽屉**

```vue
    <AdminDrawer />
```

放在 `<main>` 之后、`<ToastStack />` 之前，并 `import AdminDrawer from './components/AdminDrawer.vue';`。

- [ ] **Step 3: 验证（用管理员登录）**

清单：
1. 顶栏「用户管理」只在管理员账号下出现；非管理员看不见（直接请求 API 会 403，这是服务端保证）；
2. 打开：抽屉从右侧滑入 + 遮罩；`Escape` 关闭；点遮罩关闭；关闭后焦点回到「用户管理」按钮（用 Tab 检查）；
3. 新增用户 → 出现在列表 + toast；密码少于 8 位时服务端返回错误，红字显示在抽屉内；
4. 重置密码 → 行内展开输入框，保存后提示成功；用新密码在另一浏览器能登录；
5. 删除用户 → 自定义确认框 → 删除成功；删除最后一个管理员/自己时服务端报错并在抽屉内显示；
6. 手机宽度下抽屉全屏。

- [ ] **Step 4: 提交**

```bash
cd /home/u/workspace/lanbox
git add frontend web
git commit -m "feat(web): 用户管理抽屉（行内重置密码、确认与错误提示）"
```

---

## Task 9：Go 侧静态交付与安全头（TDD）

**Files:**
- Modify: `internal/httpapi/server.go:81-102`
- Modify: `internal/httpapi/middleware.go:40-48`
- Test: `internal/httpapi/static_test.go`、`web_test.go`

- [ ] **Step 1: 先写失败测试 `internal/httpapi/static_test.go`**

```go
package httpapi_test

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

var assetRef = regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`)

func TestIndexPageSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	resp, err := e.client.Get(e.srv.URL + "/")
	if err != nil {
		t.Fatalf("请求首页失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("首页状态码 = %d，期望 200", resp.StatusCode)
	}

	csp := resp.Header.Get("Content-Security-Policy")
	if strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "unsafe-inline") {
		t.Errorf("CSP 不应包含 unsafe-*：%q", csp)
	}
	for _, want := range []string{"script-src 'self'", "style-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP 缺少 %q：%q", want, csp)
		}
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("首页 Cache-Control = %q，期望 no-cache", got)
	}
}

func TestHashedAssetsAreImmutable(t *testing.T) {
	e := newEnv(t)
	page, err := e.client.Get(e.srv.URL + "/")
	if err != nil {
		t.Fatalf("请求首页失败: %v", err)
	}
	defer page.Body.Close()
	body, err := io.ReadAll(page.Body)
	if err != nil {
		t.Fatalf("读取首页失败: %v", err)
	}
	refs := assetRef.FindAllStringSubmatch(string(body), -1)
	if len(refs) == 0 {
		t.Fatal("index.html 未引用任何 /assets/ 资源，可能忘记 npm run build")
	}

	resp, err := e.client.Get(e.srv.URL + refs[0][1])
	if err != nil {
		t.Fatalf("请求构建产物失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s 状态码 = %d，期望 200", refs[0][1], resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("带哈希产物的 Cache-Control = %q，期望 public, max-age=31536000, immutable", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd /home/u/workspace/lanbox && go test ./internal/httpapi -run 'TestIndexPageSecurityHeaders|TestHashedAssetsAreImmutable' -count=1
```

预期：`TestIndexPageSecurityHeaders` 因 CSP 缺少 `script-src 'self'` 而 FAIL；`TestHashedAssetsAreImmutable` 因 `Cache-Control` 为 `no-cache` 而 FAIL。

- [ ] **Step 3: 改 `internal/httpapi/middleware.go` 的 CSP**

在文件顶部常量区（`sessionCookie` 附近）新增：

```go
// contentSecurityPolicy 明确声明：只允许同源资源，禁止内联脚本/样式与 eval。
// 前端是构建期预编译的 Vue（无运行时模板编译），因此不需要 unsafe-eval。
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self'; font-src 'self'; connect-src 'self'; object-src 'none'; " +
	"base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
```

并把 `securityHeaders` 里的

```go
		h.Set("Content-Security-Policy", "default-src 'self'")
```

改成

```go
		h.Set("Content-Security-Policy", contentSecurityPolicy)
```

- [ ] **Step 4: 改 `internal/httpapi/server.go` 的 `staticHandler`**

```go
// staticHandler 提供内嵌前端资源。
func (s *Server) staticHandler() http.Handler {
	fileServer := http.FileServerFS(s.web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Vite 产物文件名带内容哈希，可以长期缓存
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			fileServer.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Cache-Control", "no-cache")
		// 首页直接读出 index.html，避免 FileServer 对 "/index.html" 做 "./" 重定向
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			page, err := fs.ReadFile(s.web, "index.html")
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", "前端资源缺失")
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(page)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
```

并在 `server.go` 的 import 块加上 `"strings"`。

- [ ] **Step 5: 跑测试确认通过**

```bash
cd /home/u/workspace/lanbox && go test ./internal/httpapi -run 'TestIndexPageSecurityHeaders|TestHashedAssetsAreImmutable' -count=1 -v
```

预期：两条 `--- PASS`。

- [ ] **Step 6: 写构建产物完整性测试 `web_test.go`（根目录，包 `lanbox_test`）**

```go
package lanbox_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"lanbox"
)

var (
	assetRef  = regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`)
	inlineTag = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
	dataURL   = regexp.MustCompile(`(?:src|href)="data:`)
)

func TestEmbeddedFrontendIsComplete(t *testing.T) {
	web := lanbox.WebFS()

	raw, err := fs.ReadFile(web, "index.html")
	if err != nil {
		t.Fatalf("读取 index.html 失败（是否忘记 npm run build？）: %v", err)
	}
	html := string(raw)

	if !strings.Contains(html, `id="app"`) {
		t.Error(`index.html 缺少 Vue 挂载点 <div id="app">`)
	}
	if !strings.Contains(html, "theme-boot.js") {
		t.Error("index.html 未引用 theme-boot.js，主题会闪屏")
	}
	if _, err := fs.Stat(web, "theme-boot.js"); err != nil {
		t.Errorf("web/theme-boot.js 不存在: %v", err)
	}
	if strings.Contains(html, "unsafe-eval") {
		t.Error("index.html 里出现 unsafe-eval：Vue 运行时模板编译会被 CSP 拦截")
	}

	for _, m := range inlineTag.FindAllStringSubmatch(html, -1) {
		attrs, body := m[1], strings.TrimSpace(m[2])
		if !strings.Contains(attrs, "src=") && body != "" {
			t.Errorf("index.html 含内联脚本，会被 CSP 拦截: <script%s>%s", attrs, body)
		}
	}

	if dataURL.MatchString(html) {
		t.Error("index.html 含 data: 资源引用，会被 CSP default-src 'self' 拦截（检查 vite assetsInlineLimit）")
	}

	refs := assetRef.FindAllStringSubmatch(html, -1)
	if len(refs) == 0 {
		t.Fatal("index.html 未引用任何 /assets/ 构建产物，可能忘记 npm run build")
	}
	for _, m := range refs {
		path := strings.TrimPrefix(m[1], "/")
		if _, err := fs.Stat(web, path); err != nil {
			t.Errorf("index.html 引用的资源 %s 不存在: %v", m[1], err)
		}
	}

	entries, err := fs.ReadDir(web, "assets")
	if err != nil {
		t.Fatalf("web/assets 不存在: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("web/assets 为空")
	}
}
```

- [ ] **Step 7: 跑全量测试**

```bash
cd /home/u/workspace/lanbox && make check
```

预期：gofmt 无输出、vet 通过、所有包 `ok`（含新增的 `TestEmbeddedFrontendIsComplete`）。

- [ ] **Step 8: 手动确认缓存与 CSP 真的生效**

```bash
cd /home/u/workspace/lanbox && make build
./lanbox & SRV=$!
sleep 2
curl -sk -D - https://127.0.0.1:8443/ -o /dev/null | grep -iE 'content-security|cache-control'
ASSET=$(curl -sk https://127.0.0.1:8443/ | grep -o '/assets/[^"]*' | head -1)
curl -sk -D - "https://127.0.0.1:8443$ASSET" -o /dev/null | grep -iE '^HTTP|cache-control|content-type'
kill $SRV
```

预期：首页 `no-cache` + 明确 CSP；`/assets/...` 返回 `immutable` 且 `content-type` 为 `text/javascript` 或 `text/css`。

- [ ] **Step 9: 提交**

```bash
cd /home/u/workspace/lanbox
git add internal/httpapi web_test.go
git commit -m "feat(httpapi): 构建产物长缓存、CSP 显式化并加嵌入资源完整性测试"
```

---

## Task 10：构建脚本、文档与全量验收

**Files:**
- Modify: `Makefile`、`README.md`、`.gitignore`

- [ ] **Step 1: `Makefile` 增加前端目标**

在 `.PHONY` 行加入 `web web-dev`：

```make
.PHONY: all build dist checksums test vet fmt check run install clean help web web-dev
```

在 `## clean` 之前插入：

```make
## web: 重新构建前端（需要 Node 20.19+；产物 web/ 要一起提交）
web:
	cd frontend && npm install && npm run build
	@echo "已重建 web/，请把 frontend/ 与 web/ 的改动一起提交"

## web-dev: 启动 Vite 开发服务器（HMR，/api 代理到本机 8443）
web-dev:
	cd frontend && npm run dev
```

- [ ] **Step 2: `.gitignore` 追加前端忽略项**

```gitignore
frontend/node_modules/
```

- [ ] **Step 3: `README.md` 在“交叉编译”一节之后插入「前端开发与主题」小节**

```markdown
## 前端开发与主题

前端是 Vue 3 + Vite，源码在 `frontend/`，构建产物在 `web/` 并被 `//go:embed` 打进二进制。
**`go build` / `make build` 不需要 Node**，直接用仓库里已提交的 `web/`。

只有改动前端时才需要 Node（20.19+ / 22.12+）：

```bash
make web                      # npm install + vite build → 重新生成 web/
git add frontend web && git commit -m "feat(web): ..."   # 产物要和源码一起提交
```

本地调试（热更新）：

```bash
go run ./cmd/lanbox &         # 先让后端跑在 :8443
make web-dev                  # 打开 http://127.0.0.1:5173，/api 已代理到后端
```

> `http://localhost` 属浏览器安全上下文，自签证书下的 `Secure` cookie 在开发模式下同样有效。

主题：右上角可切换「跟随系统 / 亮色 / 暗色」，选择存在浏览器 `localStorage`，刷新保持；
主题令牌集中在 `frontend/src/styles/tokens.css`，新增主题只需加一段 `:root[data-theme='自定义名']`。

安全约束：页面禁止内联脚本与 `data:` 资源（CSP 为 `default-src 'self'`），因此不要把图标/字体改成内联
（`vite.config.js` 里 `assetsInlineLimit: 0` 就是在保证这一点）；`web_test.go` 会在违反时报错。

提交前的自检：

```bash
make check     # gofmt + vet + 全部 Go 测试（含 web/ 产物完整性检查）
```
```

- [ ] **Step 4: 全量构建与测试**

```bash
cd /home/u/workspace/lanbox
make check
make build
make dist
ls -l dist/
```

预期：`make check` 全绿；`make dist` 产出四个平台二进制 + `SHA256SUMS`。

- [ ] **Step 5: 端到端冒烟（两个浏览器窗口，建议一个亮色一个暗色）**

1. 启动 `./lanbox`，用管理员登录 A 窗口；
2. A 发文字 + 2 个文件 → B 窗口 1 秒内出现；
3. B 下载文件、标记已取走 → A 窗口卡片变淡；
4. B 删除其中一条 → A 窗口卡片消失；
5. A 在管理抽屉新增用户 C，B 登出后能用 C 登录；
6. 两端主题独立切换（各自 `localStorage`），切换后刷新仍保持；
7. A 窗口切到亮色，DevTools 里确认 `--text` 与 `--surface` 对比度足够（正文 ≥ 7:1）；
8. Console 无 CSP violation、无 JS 异常；
9. 375px 宽度下无横向滚动。

- [ ] **Step 6: 清理构建产物里的本地数据**

```bash
cd /home/u/workspace/lanbox
git status --short          # 不应出现 data/ 或 dist/（已被 .gitignore 忽略）
```

- [ ] **Step 7: 提交**

```bash
cd /home/u/workspace/lanbox
git add Makefile README.md .gitignore
git commit -m "docs: 前端开发流程与主题说明，Makefile 增加 web/web-dev 目标"
```

---

## 完成标准（对照设计文档第 10 节）

- [ ] `frontend/` 有源码与 `package-lock.json`，`web/` 只有构建产物，旧 `web/*.js` 已删除
- [ ] `make check` 通过（含 `web_test.go` 与 `static_test.go`）
- [ ] `make build` / `make dist` 成功，二进制内嵌新前端
- [ ] 三态主题：即时切换、刷新保持、无闪屏、系统跟随生效
- [ ] 登录、发布（拖拽/粘贴/进度）、SSE、筛选/搜索/分页、取走、删除、用户管理全部可用
- [ ] 响应头 CSP 不含 `unsafe-eval` / `unsafe-inline`；`/assets/` 为 `immutable` 缓存
- [ ] 375px 宽度可用；亮/暗两套主题对比度达标
