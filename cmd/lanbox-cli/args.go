package main

import (
	"flag"
	"fmt"
	"strings"
)

// parseArgs 解析"选项与位置参数可交错"的命令行，例如 get <ID> -o dir。
// 直接从 fs 里查选项定义，因此不需要维护第二份选项表；遇到 "--" 之后全是位置参数。
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		// "--" 是显式终止符，其后的内容一律当作位置参数（"--" 本身丢弃）。
		if a == "--" {
			return append(pos, args[i+1:]...), nil
		}
		// 单独的 "-" 是"从标准输入读"的约定，属位置参数。
		if a == "-" || !strings.HasPrefix(a, "-") {
			pos = append(pos, a)
			continue
		}
		name, value, hasValue := splitOption(a)
		if name == "" {
			return nil, fmt.Errorf("%w: 无法识别的选项: %s", errUsage, a)
		}
		f := fs.Lookup(name)
		if f == nil {
			return nil, fmt.Errorf("%w: 未知选项: %s", errUsage, a)
		}
		if !hasValue {
			if isBoolOption(f) {
				// 布尔选项写成 -name 即视为 true，不消费下一个 token。
				value = "true"
			} else {
				// 非布尔选项的值可以是任意 token（包括以 - 开头的，如 -o -insecure）。
				if i+1 >= len(args) {
					return nil, fmt.Errorf("%w: 选项 -%s 需要一个值", errUsage, name)
				}
				i++
				value = args[i]
			}
		}
		// 直接通过 fs.Set 生效，避免拼回字符串再 Parse 带来的二次解析歧义。
		if err := fs.Set(name, value); err != nil {
			return nil, fmt.Errorf("%w: 选项 -%s 的值无效: %v", errUsage, name, err)
		}
	}
	return pos, nil
}

// splitOption 把 "-name"、" -name=value"、"--name=value" 拆成名字与值。
// hasValue 为 false 时表示没有写等号，值需要从下一个 token 取（若该选项非布尔）。
func splitOption(a string) (name, value string, hasValue bool) {
	s := strings.TrimLeft(a, "-")
	if s == "" {
		return "", "", false
	}
	if idx := strings.IndexByte(s, '='); idx >= 0 {
		return s[:idx], s[idx+1:], true
	}
	return s, "", false
}

// isBoolOption 判断选项是否布尔型（布尔选项不需要显式取值）。
func isBoolOption(f *flag.Flag) bool {
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}

// splitCommand 返回子命令在 args 中的下标：之前是全局选项，之后是子命令自己的参数。
func splitCommand(fs *flag.FlagSet, args []string) (int, error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		// "--" 之后紧跟的就是子命令（"--" 本身会被 parseArgs 丢弃）。
		if a == "--" {
			return i + 1, nil
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			return i, nil // 第一个位置参数即子命令
		}
		name, _, hasValue := splitOption(a)
		f := fs.Lookup(name)
		if f == nil {
			return 0, fmt.Errorf("%w: 未知选项: %s", errUsage, a)
		}
		// 跳过选项取值，避免把 -s 的地址误认成子命令。
		if !hasValue && !isBoolOption(f) {
			i++
		}
	}
	return len(args), nil // 没有子命令
}
