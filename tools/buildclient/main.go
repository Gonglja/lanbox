// Command buildclient 把 client/ 目录打包成一个自包含的 HTML 文件。
//
// 做法：
//  1. 内联 <link rel="stylesheet">（web/style.css + client/client.css）、favicon（data: URL）
//     与经典脚本（web/theme-boot.js）；
//  2. 从 <script type="module" src> 出发遍历模块图，按依赖优先排序，
//     把每个模块里的相对说明符换成 __MOD<i>__ 占位符；
//  3. 生成一小段 loader：按顺序把每个模块转成 blob: URL 模块（依赖已先生成），
//     最后 import 入口模块。
//
// 这样既不需要前端构建链（无 npm、无 bundler 依赖），产物也能直接以 file:// 打开。
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	root := flag.String("root", ".", "仓库根目录（含 client/ 与 web/）")
	out := flag.String("out", filepath.Join("dist", "lanbox-client.html"), "输出文件路径")
	flag.Parse()

	res, err := Build(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打包失败: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建目录失败: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, res.HTML, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "写文件失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("已生成 %s（%d 个模块，%d KB）\n", *out, res.Modules, len(res.HTML)/1024)
}

// Result 是打包产物。
type Result struct {
	HTML    []byte
	Modules int // 内联进来的 ES 模块数量
}

// Build 返回打包好的单文件 HTML。
func Build(root string) (*Result, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	indexPath := filepath.Join(absRoot, "client", "index.html")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, err
	}
	html := string(raw)

	entryRef, err := moduleEntry(html)
	if err != nil {
		return nil, err
	}
	entry, err := resolveRef(indexPath, entryRef)
	if err != nil {
		return nil, err
	}
	order, err := moduleGraph(entry)
	if err != nil {
		return nil, err
	}
	idx := make(map[string]int, len(order))
	for i, p := range order {
		idx[p] = i
	}
	mods := make([]string, 0, len(order))
	for _, p := range order {
		src, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		code, err := placeholderize(string(src), p, idx)
		if err != nil {
			return nil, err
		}
		mods = append(mods, jsString(code))
	}

	html, err = replaceScripts(html, indexPath, entryRef, renderLoader(mods))
	if err != nil {
		return nil, err
	}
	html, err = replaceLinks(html, indexPath)
	if err != nil {
		return nil, err
	}
	return &Result{HTML: []byte(html), Modules: len(mods)}, nil
}

var (
	linkTagRe   = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	scriptTagRe = regexp.MustCompile(`(?is)<script\b[^>]*>\s*</script>`)
	attrRe      = regexp.MustCompile(`(?is)([a-z][a-z0-9:_-]*)\s*=\s*"([^"]*)"`)
	// 说明符可能是单引号或双引号（RE2 不支持反向引用，所以写成两个分支）
	specRe = regexp.MustCompile(`(?m)(?:\bfrom\s*|\bimport\s*\(\s*|\bimport\s+)(?:'([^'\n]+)'|"([^"\n]+)")`)
)

// tagAttrs 解析标签属性（只需处理我们自己写的那几种写法）。
func tagAttrs(tag string) map[string]string {
	m := map[string]string{}
	for _, g := range attrRe.FindAllStringSubmatch(tag, -1) {
		m[strings.ToLower(g[1])] = g[2]
	}
	return m
}

// moduleEntry 返回 <script type="module" src="..."> 的 src。
func moduleEntry(html string) (string, error) {
	for _, tag := range scriptTagRe.FindAllString(html, -1) {
		a := tagAttrs(tag)
		if strings.EqualFold(a["type"], "module") && a["src"] != "" {
			return a["src"], nil
		}
	}
	return "", fmt.Errorf("client/index.html 里找不到 <script type=\"module\" src=...> 入口")
}

// resolveRef 把相对引用解析成绝对路径；不处理外部 URL。
func resolveRef(baseFile, ref string) (string, error) {
	if strings.Contains(ref, "://") || strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "//") {
		return "", fmt.Errorf("%s: 不支持内联外部引用 %q", baseFile, ref)
	}
	if !strings.HasPrefix(ref, "./") && !strings.HasPrefix(ref, "../") {
		return "", fmt.Errorf("%s: 不支持裸引用 %q（只处理相对路径）", baseFile, ref)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(baseFile), filepath.FromSlash(ref))), nil
}

// specOf 从一个匹配片段里取出说明符。
func specOf(m string) (string, bool) {
	g := specRe.FindStringSubmatch(m)
	if g == nil {
		return "", false
	}
	if g[1] != "" {
		return g[1], true
	}
	if g[2] != "" {
		return g[2], true
	}
	return "", false
}

// moduleSpecifiers 取出源码里的模块说明符（from '...'、import '...'、import('...')）。
func moduleSpecifiers(src string) []string {
	var out []string
	for _, m := range specRe.FindAllString(src, -1) {
		if spec, ok := specOf(m); ok {
			out = append(out, spec)
		}
	}
	return out
}

// moduleGraph 返回依赖优先的模块顺序（入口在最后）；环形依赖报错。
func moduleGraph(entry string) ([]string, error) {
	const (
		white = iota
		gray
		black
	)
	state := map[string]int{}
	var order []string

	var visit func(string) error
	visit = func(p string) error {
		switch state[p] {
		case gray:
			return fmt.Errorf("模块之间存在循环依赖: %s", p)
		case black:
			return nil
		}
		state[p] = gray
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, spec := range moduleSpecifiers(string(src)) {
			dep, err := resolveRef(p, spec)
			if err != nil {
				return err
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[p] = black
		order = append(order, p)
		return nil
	}
	if err := visit(entry); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("模块图为空: %s", entry)
	}
	return order, nil
}

// placeholderize 把模块里的相对说明符替换成 __MOD<i>__（运行时再换成 blob URL）。
func placeholderize(src, file string, idx map[string]int) (string, error) {
	var firstErr error
	out := specRe.ReplaceAllStringFunc(src, func(m string) string {
		spec, ok := specOf(m)
		if !ok {
			return m
		}
		dep, err := resolveRef(file, spec)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return m
		}
		i, ok := idx[dep]
		if !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: 模块 %s 不在依赖图里", file, spec)
			}
			return m
		}
		// 匹配片段以说明符+引号结尾，只替换说明符本体，保留引号与关键字
		return m[:len(m)-len(spec)-1] + fmt.Sprintf("__MOD%d__", i) + m[len(m)-1:]
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

// jsString 把源码编码成安全的 JS 字符串字面量，并让内联脚本里不出现 </script。
func jsString(code string) string {
	b, err := json.Marshal(code)
	if err != nil {
		panic(err) // 只可能是编程错误：字符串编码不会失败
	}
	return strings.ReplaceAll(string(b), "</", `<\/`)
}

// renderLoader 生成装载模块的经典脚本。
func renderLoader(mods []string) string {
	var b strings.Builder
	b.WriteString("<script>\n")
	b.WriteString("// 由 tools/buildclient 生成，请勿手改：ES 模块按依赖顺序转成 blob URL 后加载。\n")
	b.WriteString("(function () {\n  var MODS = [\n")
	for i, m := range mods {
		b.WriteString("    ")
		b.WriteString(m)
		if i < len(mods)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  ];\n")
	b.WriteString(`  if (typeof URL.createObjectURL !== 'function') {
    document.body.innerHTML = '<p style="padding:24px;font:14px system-ui">当前浏览器不支持模块内联，请改用直接托管 client/ 目录的方式。</p>';
    return;
  }
  var urls = [];
  for (var i = 0; i < MODS.length; i++) {
    var code = MODS[i];
    // 依赖的索引一定小于自身（拓扑排序），所以此处它们的 URL 都已就绪
    for (var j = 0; j < i; j++) code = code.split('__MOD' + j + '__').join(urls[j]);
    urls.push(URL.createObjectURL(new Blob([code], { type: 'text/javascript' })));
  }
  import(urls[urls.length - 1]);
})();
</script>`)
	return b.String()
}

// replaceScripts 内联经典脚本，并用 loader 替换模块入口。
func replaceScripts(html, indexPath, entryRef, loader string) (string, error) {
	var firstErr error
	out := scriptTagRe.ReplaceAllStringFunc(html, func(tag string) string {
		a := tagAttrs(tag)
		src := a["src"]
		if src == "" {
			return tag
		}
		if strings.EqualFold(a["type"], "module") {
			if src != entryRef {
				if firstErr == nil {
					firstErr = fmt.Errorf("只支持一个模块入口，多余的是 %q", src)
				}
				return tag
			}
			return loader
		}
		p, err := resolveRef(indexPath, src)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return tag
		}
		code, err := os.ReadFile(p)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return tag
		}
		return "<script>\n" + escapeScriptEnd(string(code)) + "\n</script>"
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

// replaceLinks 内联样式表与 favicon。
func replaceLinks(html, indexPath string) (string, error) {
	var firstErr error
	out := linkTagRe.ReplaceAllStringFunc(html, func(tag string) string {
		a := tagAttrs(tag)
		rel := strings.ToLower(a["rel"])
		href := a["href"]
		if href == "" || (rel != "stylesheet" && rel != "icon") {
			return tag
		}
		p, err := resolveRef(indexPath, href)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return tag
		}
		data, err := os.ReadFile(p)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return tag
		}
		if rel == "icon" {
			return fmt.Sprintf("<link rel=\"icon\" href=\"data:%s;base64,%s\">",
				mimeByExt(p), base64Std(data))
		}
		if strings.Contains(strings.ToLower(string(data)), "</style") {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s 含有 </style，无法安全内联", p)
			}
			return tag
		}
		return "<style>\n" + string(data) + "\n</style>"
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

// escapeScriptEnd 让内联脚本里不出现能提前闭合标签的 </script。
// 经典脚本保持原样（不能像模块源码那样整体 JSON 编码，否则换行等转义会破坏语法）。
func escapeScriptEnd(code string) string {
	return strings.ReplaceAll(code, "</script", `<\/script`)
}

func base64Std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func mimeByExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}
