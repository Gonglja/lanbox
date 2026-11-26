package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"lanbox/internal/client"
)

func TestRenderItemsTable(t *testing.T) {
	items := []client.Item{{
		ID:        "0001-ab",
		CreatedAt: time.Date(2026, 11, 26, 10, 0, 0, 0, time.Local),
		CreatedBy: "admin",
		Text:      "第一行\n第二行",
		Taken:     true,
		Files:     []client.File{{ID: "f1", Path: "a/b.txt", Size: 2048}},
	}}
	var buf bytes.Buffer
	if err := renderItems(&buf, items); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"0001-ab", "admin", "2.0 KB", "已取走", "第一行 第二行"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q:\n%s", want, out)
		}
	}
}

func TestSummarizeTrimsAndTruncates(t *testing.T) {
	if got := summarize("  a\nb  "); got != "a b" {
		t.Fatalf("应压成单行，得到 %q", got)
	}
	long := strings.Repeat("字", 60)
	got := summarize(long)
	if len([]rune(got)) != 41 || !strings.HasSuffix(got, "…") {
		t.Fatalf("应截断到 40 字并加省略号，得到 %q", got)
	}
}

func TestFmtBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 999: "999 B", 1024: "1.0 KB", 2048: "2.0 KB", 3 << 20: "3.0 MB"}
	for in, want := range cases {
		if got := fmtBytes(in); got != want {
			t.Fatalf("fmtBytes(%d)=%q，期望 %q", in, got, want)
		}
	}
}
