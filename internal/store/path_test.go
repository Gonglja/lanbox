package store

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitizeRelPath(t *testing.T) {
	deep := strings.Repeat("d/", MaxPathDepth+1) + "f"
	long := strings.Repeat(strings.Repeat("a", maxSegmentRunes)+"/", 8) + strings.Repeat("a", maxSegmentRunes)

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
		{strings.Repeat("a", maxSegmentRunes+1), "", true},
		{deep, "", true},
		{long, "", true},
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
