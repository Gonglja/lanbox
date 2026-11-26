package store

import "strings"

const (
	// MaxPathDepth 是附件相对路径的最大层数。
	MaxPathDepth = 16
	// MaxPathBytes 是附件相对路径的最大字节数。
	MaxPathBytes = 1024
	// maxSegmentRunes 是路径单段的最大 rune 数，超出即报错（不截断，避免撞名）。
	maxSegmentRunes = 120
)

// SanitizeRelPath 清洗用户提交的相对路径并保留目录结构。
// 拒绝绝对路径、盘符、任何 ".." 段、超深与超长路径。
// 与 SanitizeName 的唯一差异：保留前导点，使 .env / .gitignore 能正常传输。
func SanitizeRelPath(raw string) (string, error) {
	if raw == "" {
		return "", ErrBadPath
	}
	p := strings.ReplaceAll(raw, "\\", "/")
	if strings.HasPrefix(p, "/") || isWindowsAbs(p) {
		return "", ErrBadPath
	}
	segs := make([]string, 0, 8)
	for _, seg := range strings.Split(p, "/") {
		// 先清洗再判定：否则 " .."、".\x00." 这类输入清洗后会重组出 ".."。
		seg = cleanSegment(seg)
		switch seg {
		case "", ".":
			continue
		case "..":
			return "", ErrBadPath
		}
		if len([]rune(seg)) > maxSegmentRunes {
			return "", ErrBadPath
		}
		segs = append(segs, seg)
	}
	if len(segs) == 0 || len(segs) > MaxPathDepth {
		return "", ErrBadPath
	}
	out := strings.Join(segs, "/")
	if len(out) > MaxPathBytes {
		return "", ErrBadPath
	}
	return out, nil
}

// cleanSegment 去掉段内的控制字符与文件系统保留字符。
func cleanSegment(seg string) string {
	var b strings.Builder
	for _, r := range seg {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// isWindowsAbs 判断是否是盘符形式的绝对路径（C:/x、c:/x）。
// 盘符后必须紧跟分隔符：否则 "a:b.txt" 这类普通文件名会被误判为绝对路径。
func isWindowsAbs(p string) bool {
	if len(p) < 3 || p[1] != ':' || p[2] != '/' {
		return false
	}
	c := p[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
