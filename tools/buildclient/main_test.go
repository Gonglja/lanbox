package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// build 在临时根目录里跑一次打包（fixture 由调用方写入）。
func buildTemp(t *testing.T, files map[string]string) (*Result, error) {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Build(root)
}

const minimalHTML = `<!doctype html><html><head>
<link rel="stylesheet" href="./a.css">
</head><body><script type="module" src="./app.js"></script></body></html>`

func TestBuildInlinesEverything(t *testing.T) {
	res, err := buildTemp(t, map[string]string{
		"client/index.html": minimalHTML,
		"client/a.css":      "body{color:red}",
		"client/app.js":     "import { x } from './lib.js';\nconsole.log('入口', x);\n",
		"client/lib.js":     "export const x = 1;\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.HTML)
	if res.Modules != 2 {
		t.Fatalf("应有 2 个模块，得到 %d", res.Modules)
	}
	if !strings.Contains(out, "<style>\nbody{color:red}\n</style>") {
		t.Fatalf("样式表没被内联: %s", out)
	}
	if strings.Contains(out, `type="module"`) {
		t.Fatal("模块标签应被 loader 取代")
	}
	if !strings.Contains(out, "var MODS = [") {
		t.Fatal("缺少 loader")
	}
	// 依赖在前、入口在后：入口引用 lib 的占位符，lib 自身不含占位符
	if !strings.Contains(out, `from '__MOD0__'`) {
		t.Fatalf("入口的相对说明符没有被替换成占位符: %s", out)
	}
	if strings.Index(out, "export const x = 1") > strings.Index(out, "console.log('入口'") {
		t.Fatal("模块顺序应为依赖优先")
	}
	// 内联脚本里不能出现能提前闭合标签的 </script
	if n := strings.Count(out, "</script>"); n != 1 {
		t.Fatalf("应只有 loader 一个 </script>，得到 %d", n)
	}
}

func TestBuildEscapesScriptEndInsideModules(t *testing.T) {
	res, err := buildTemp(t, map[string]string{
		"client/index.html": minimalHTML,
		"client/a.css":      "body{}",
		"client/app.js":     "const s = '</script>'; console.log(s);\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.HTML)
	if strings.Count(out, "</script>") != 1 {
		t.Fatalf("模块里的 </script> 必须被转义，得到 %d 个闭合标签", strings.Count(out, "</script>"))
	}
	// encoding/json 会把 < > 编成 \u003c/\u003e，等价于转义且仍是合法 JS
	if !strings.Contains(out, `\u003c/script\u003e`) && !strings.Contains(out, `<\/script>`) {
		t.Fatalf("模块里的 </script> 没有被转义: %s", out)
	}
}

func TestBuildRejectsCycle(t *testing.T) {
	_, err := buildTemp(t, map[string]string{
		"client/index.html": minimalHTML,
		"client/a.css":      "body{}",
		"client/app.js":     "import './b.js';\n",
		"client/b.js":       "import './app.js';\n",
	})
	if err == nil || !strings.Contains(err.Error(), "循环依赖") {
		t.Fatalf("应报告循环依赖，得到 %v", err)
	}
}

func TestBuildMissingFile(t *testing.T) {
	_, err := buildTemp(t, map[string]string{
		"client/index.html": minimalHTML,
		"client/app.js":     "console.log(1);\n",
	})
	if err == nil || !strings.Contains(err.Error(), "a.css") {
		t.Fatalf("应报告缺失的样式表，得到 %v", err)
	}
}

func TestBuildRequiresModuleEntry(t *testing.T) {
	_, err := buildTemp(t, map[string]string{
		"client/index.html": `<html><body><script src="./x.js"></script></body></html>`,
		"client/x.js":       "console.log(1);\n",
	})
	if err == nil || !strings.Contains(err.Error(), "module") {
		t.Fatalf("应要求模块入口，得到 %v", err)
	}
}

// TestBuildRepoClient 守住真实产物：顺序、内联、无残留相对说明符。
func TestBuildRepoClient(t *testing.T) {
	res, err := Build("../..")
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.HTML)
	if res.Modules < 8 {
		t.Fatalf("真实客户端应内联至少 8 个模块，得到 %d", res.Modules)
	}
	if !strings.Contains(out, "lanbox") || !strings.Contains(out, "服务器地址") {
		t.Fatal("产物里应包含客户端连接页")
	}
	if len(out) < 60_000 {
		t.Fatalf("产物过小（%d 字节），样式或模块可能没内联", len(out))
	}
	if strings.Contains(out, `href="../web/`) || strings.Contains(out, `src="../web/`) {
		t.Fatal("仍有未内联的 ../web 资源引用")
	}
	// 拓扑顺序：env.js 在 main.js 之前，入口 app.js 最后
	envIdx := strings.Index(out, "把「服务端同源 Web UI」与「异地纯 web 客户端」的差异收敛到一处")
	mainIdx := strings.Index(out, "hooks 供纯 web 客户端")
	entryIdx := strings.Index(out, "纯 web 客户端的连接层")
	if envIdx < 0 || mainIdx < 0 || entryIdx < 0 {
		t.Fatalf("模块标记缺失 env=%d main=%d entry=%d", envIdx, mainIdx, entryIdx)
	}
	if !(envIdx < mainIdx && mainIdx < entryIdx) {
		t.Fatalf("模块顺序不对 env=%d main=%d entry=%d", envIdx, mainIdx, entryIdx)
	}
}
