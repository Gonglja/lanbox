// Package updater 负责检查 lanbox 的新版本、下载并校验二进制、替换当前可执行文件与回滚。
//
// 只用标准库：服务端与 CLI 都保持零第三方依赖。
package updater

import (
	"fmt"
	"strconv"
	"strings"
)

// prePart 是预发布标识的一段：数字段（1）按数值比，其它按字典序比，数字段永远小于字母段。
type prePart struct {
	num   int
	isNum bool
	text  string
}

// Version 是解析后的宽松 semver：核心 1-3 段（缺省补 0）+ 可选预发布。
type Version struct {
	Major, Minor, Patch int
	Pre                 []prePart
}

// String 返回规范化形式（无 v 前缀、无 build 元数据）。
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		parts := make([]string, len(v.Pre))
		for i, p := range v.Pre {
			parts[i] = p.text
		}
		s += "-" + strings.Join(parts, ".")
	}
	return s
}

// parseVersion 解析 "1.2.3" / "v1.2.3-rc1+build" 这类版本号。
func parseVersion(s string) (Version, error) {
	var v Version
	s = strings.TrimSpace(s)
	if s == "" {
		return v, fmt.Errorf("版本号为空")
	}
	if i := strings.IndexByte(s, '+'); i >= 0 { // 忽略 build 元数据
		s = s[:i]
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")

	core, pre := s, ""
	if i := strings.IndexByte(s, '-'); i >= 0 {
		core, pre = s[:i], s[i+1:]
		if pre == "" {
			return v, fmt.Errorf("版本号 %q 的预发布段为空", s)
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return v, fmt.Errorf("版本号 %q 的核心段必须是 1-3 个数字", s)
	}
	nums := [3]int{}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, fmt.Errorf("版本号 %q 的第 %d 段不是非负整数", s, i+1)
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]

	if pre != "" {
		for _, p := range strings.Split(pre, ".") {
			if p == "" {
				return v, fmt.Errorf("版本号 %q 的预发布段含空项", s)
			}
			if n, err := strconv.Atoi(p); err == nil && n >= 0 {
				v.Pre = append(v.Pre, prePart{num: n, isNum: true, text: p})
				continue
			}
			v.Pre = append(v.Pre, prePart{text: p})
		}
	}
	return v, nil
}

// parseLocal 解析本机版本：解析不了（dev / 空 / 非法）时视为最低版本，
// 这样自编译版本永远能看到「有新版本」。
func parseLocal(s string) Version {
	if v, err := parseVersion(s); err == nil {
		return v
	}
	return Version{Pre: []prePart{{text: "dev"}}}
}

// compareVersion 返回 -1/0/1。正式版大于同号预发布版。
func compareVersion(a, b Version) int {
	if c := cmpInt(a.Major, b.Major); c != 0 {
		return c
	}
	if c := cmpInt(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := cmpInt(a.Patch, b.Patch); c != 0 {
		return c
	}
	if len(a.Pre) == 0 && len(b.Pre) == 0 {
		return 0
	}
	if len(a.Pre) == 0 {
		return 1
	}
	if len(b.Pre) == 0 {
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		if c := cmpPre(a.Pre[i], b.Pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(a.Pre), len(b.Pre))
}

func cmpPre(a, b prePart) int {
	switch {
	case a.isNum && b.isNum:
		return cmpInt(a.num, b.num)
	case a.isNum: // 数字段小于字母段
		return -1
	case b.isNum:
		return 1
	default:
		return strings.Compare(a.text, b.text)
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// normalizeVersion 把 "v1.2.3+build" 压成展示用的 "1.2.3"；解析不了时原样返回。
func normalizeVersion(s string) string {
	if v, err := parseVersion(s); err == nil {
		return v.String()
	}
	return strings.TrimSpace(s)
}
