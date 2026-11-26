package store

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitizeRelPath(t *testing.T) {
	deep := strings.Repeat("d/", MaxPathDepth+1) + "f"
	long := strings.Repeat(strings.Repeat("a", maxSegmentRunes)+"/", 8) + strings.Repeat("a", maxSegmentRunes)
	// 单段上限只有 120 rune，凑满 1024 字节必须多段拼；
	// 用 8 段拼出 968 字节前缀，再加短尾段，总层数 9，仍在 MaxPathDepth 内。
	// 以下三条测的是 MaxPathBytes，不是段长。
	pad := strings.Repeat(strings.Repeat("a", maxSegmentRunes)+"/", 8) // 968 字节
	byteOK := pad + strings.Repeat("a", 55)                            // 1023 字节
	byteEdge := pad + strings.Repeat("a", 56)                          // 1024 字节
	byteOver := pad + strings.Repeat("a", 57)                          // 1025 字节

	cases := []struct {
		in   string
		want string
		bad  bool
	}{
		{"a.txt", "a.txt", false},
		{"photos/2024/a.jpg", "photos/2024/a.jpg", false},
		{`photos\2024\a.jpg`, "photos/2024/a.jpg", false},
		{"a//b", "a/b", false},
		{"a/./b", "a/b", false},
		{"a/", "a", false},
		// 全是空段，清洗后无内容
		{".", "", true},
		{"./", "", true},
		{".env", ".env", false},
		{"dir/.gitignore", "dir/.gitignore", false},
		{"  name.txt  ", "name.txt", false},
		{"a:b.txt", "ab.txt", false},
		{"a\x00b.txt", "ab.txt", false},
		{"", "", true},
		{"/etc/passwd", "", true},
		{"../x", "", true},
		{"a/../../b", "", true},
		{`..\x`, "", true},
		{`C:\Windows\x`, "", true},
		{"///", "", true},
		{" ..", "", true},
		{".\x00./x", "", true},
		{"a/\t../b", "", true},
		{strings.Repeat("a", maxSegmentRunes+1), "", true},
		{deep, "", true},
		{long, "", true},
		// 字节上限精确边界：1023 / 1024 通过，1025 拒绝
		{byteOK, byteOK, false},
		{byteEdge, byteEdge, false},
		{byteOver, "", true},
	}
	for _, c := range cases {
		got, err := SanitizeRelPath(c.in)
		if c.bad {
			if !errors.Is(err, ErrBadPath) {
				t.Fatalf("%q 应报 ErrBadPath，得到 %q / %v", c.in, got, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q 不应报错: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("%q 清洗为 %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeRelPathDepthBoundary(t *testing.T) {
	ok := strings.Repeat("d/", MaxPathDepth-1) + "f"
	if _, err := SanitizeRelPath(ok); err != nil {
		t.Fatalf("%d 层应通过: %v", MaxPathDepth, err)
	}
}

func TestSanitizeRelPathByteUpperBound(t *testing.T) {
	// 多段拼出接近上限的路径，验证 1024 字节是硬边界（每段都不超过 maxSegmentRunes）。
	// 注意不能用 511 段拼：那会先撞上 MaxPathDepth（16 层）。
	pad := strings.Repeat(strings.Repeat("a", maxSegmentRunes)+"/", 8) // 968 字节、8 段
	ok := pad + strings.Repeat("a", 55)                                // 1023 字节
	edge := pad + strings.Repeat("a", 56)                              // 1024 字节
	over := pad + strings.Repeat("a", 57)                              // 1025 字节
	if _, err := SanitizeRelPath(ok); err != nil {
		t.Fatalf("1023 字节应通过: %v", err)
	}
	if _, err := SanitizeRelPath(edge); err != nil {
		t.Fatalf("1024 字节应通过: %v", err)
	}
	if _, err := SanitizeRelPath(over); !errors.Is(err, ErrBadPath) {
		t.Fatalf("1025 字节应报 ErrBadPath，得到 %v", err)
	}
}

func TestSanitizeRelPathHardensOversizedInput(t *testing.T) {
	// 1 MiB 无分隔符输入：必须快速拒绝，不能先切段放大内存。
	if _, err := SanitizeRelPath(strings.Repeat("a", 1<<20)); !errors.Is(err, ErrBadPath) {
		t.Fatalf("超长输入应立刻报 ErrBadPath，得到 %v", err)
	}
}

func TestIsWindowsAbs(t *testing.T) {
	cases := map[string]bool{
		"C:/x": true, "c:/": true, "a:b.txt": false, "C:": false, "1:/x": false, "/x": false, "": false,
	}
	for in, want := range cases {
		if got := isWindowsAbs(strings.ReplaceAll(in, `\`, "/")); got != want {
			t.Fatalf("isWindowsAbs(%q)=%v，期望 %v", in, got, want)
		}
	}
}
