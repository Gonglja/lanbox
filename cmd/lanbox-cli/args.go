package main

import "strings"

// flagsFirst 把选项连同其取值移到位置参数之前。
// Go 的 flag 遇到第一个位置参数就不再解析，
// 这样 "get <ID> -o 目录" 这种自然写法也能生效。
// valueFlags 列出需要吞掉下一个参数的选项名（如 "-o"）。
func flagsFirst(args []string, valueFlags ...string) []string {
	takesValue := make(map[string]bool, len(valueFlags))
	for _, name := range valueFlags {
		takesValue[name] = true
	}
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		// "-" 表示标准输入，是位置参数而不是选项。
		if a == "-" || !strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			continue
		}
		if takesValue[a] && !strings.Contains(a, "=") && i+1 < len(args) {
			flags = append(flags, a, args[i+1])
			i++
			continue
		}
		flags = append(flags, a)
	}
	return append(flags, rest...)
}

// 全局选项：既可以写在子命令之前，也可以写在子命令之后。
// 键为去掉前导横线后的名字，这样 -json 与 --json 等价。
var (
	globalKnown      = map[string]bool{"s": true, "ca": true, "json": true, "insecure": true, "v": true}
	globalTakesValue = map[string]bool{"s": true, "ca": true}
)

// splitGlobals 挑出全局选项，返回（全局选项、其余参数）。
// 子命令之前的任何选项都归全局 flagset（未知选项由它报错），
// 子命令之后只挑出已知的全局选项，剩下的留给子命令自己解析。
func splitGlobals(args []string) (globals, rest []string) {
	seenCmd := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-" || !strings.HasPrefix(a, "-") {
			seenCmd = true // 第一个位置参数就是子命令
			rest = append(rest, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		if idx := strings.IndexByte(name, '='); idx >= 0 {
			name = name[:idx]
		}
		if seenCmd && !globalKnown[name] {
			rest = append(rest, a)
			continue
		}
		globals = append(globals, a)
		if globalTakesValue[name] && !strings.Contains(a, "=") && i+1 < len(args) {
			globals = append(globals, args[i+1])
			i++
		}
	}
	return globals, rest
}
