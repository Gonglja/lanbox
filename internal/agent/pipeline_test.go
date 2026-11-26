package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func baseSettings() Settings {
	return Settings{
		Enabled: true, Text: true, Files: true, Images: true,
		DedupeSeconds: 30, MaxTextBytes: 1024, MaxImageMB: 1, MaxTotalMB: 10, MaxFiles: 10,
	}
}

func newTestPipeline(t *testing.T, s Settings) (*Pipeline, *time.Time) {
	t.Helper()
	p := NewPipeline(s)
	p.SetAvailable(true)
	clock := time.Date(2026, 11, 26, 15, 30, 0, 0, time.Local)
	p.now = func() time.Time { return clock }
	return p, &clock
}

func TestDecideSkips(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*Settings)
		clip Clip
		want string // 期望的跳过原因子串；空表示应当上传
	}{
		{name: "总开关关", mod: func(s *Settings) { s.Enabled = false }, clip: Clip{Text: "hi"}, want: "已关闭"},
		{name: "暂停", mod: func(s *Settings) { s.Paused = true }, clip: Clip{Text: "hi"}, want: "已暂停"},
		{name: "忽略格式", clip: Clip{Text: "hi", Ignore: true}, want: "忽略"},
		{name: "文本开关关", mod: func(s *Settings) { s.Text = false }, clip: Clip{Text: "hi"}, want: "文本"},
		{name: "空文本", clip: Clip{Text: "   \n\t "}, want: "为空"},
		{name: "文本超限", mod: func(s *Settings) { s.MaxTextBytes = 4 }, clip: Clip{Text: "12345"}, want: "上限"},
		{name: "图片开关关", mod: func(s *Settings) { s.Images = false }, clip: Clip{PNG: []byte("png")}, want: "图片"},
		{name: "图片超限", mod: func(s *Settings) { s.MaxImageMB = 1 }, clip: Clip{PNG: make([]byte, 1<<20+1)}, want: "上限"},
		{name: "文件开关关", mod: func(s *Settings) { s.Files = false }, clip: Clip{Paths: []string{"x"}}, want: "文件"},
		{name: "文本上传", clip: Clip{Text: "hello"}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := baseSettings()
			if tc.mod != nil {
				tc.mod(&s)
			}
			p, _ := newTestPipeline(t, s)
			d := p.Decide(tc.clip)
			if tc.want == "" {
				if !d.Upload {
					t.Fatalf("应上传，却被跳过: %s", d.Skip)
				}
				return
			}
			if d.Upload {
				t.Fatal("应被跳过，却决定上传")
			}
			if !strings.Contains(d.Skip, tc.want) {
				t.Fatalf("跳过原因应含 %q，得到 %q", tc.want, d.Skip)
			}
		})
	}
}

func TestDecideUnavailable(t *testing.T) {
	p := NewPipeline(baseSettings())
	p.SetAvailable(false)
	if d := p.Decide(Clip{Text: "hi"}); d.Upload {
		t.Fatal("未登录时不应上传")
	}
}

func TestFilesTakePriorityOverText(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, _ := newTestPipeline(t, baseSettings())
	d := p.Decide(Clip{Text: "a.txt", Paths: []string{f}})
	if !d.Upload || d.Kind != KindFiles {
		t.Fatalf("应按文件上传，得到 %+v", d)
	}
	if d.Text != "" {
		t.Fatalf("同时存在文件时文本应被丢弃，得到 %q", d.Text)
	}
	if len(d.Files) != 1 || d.Files[0].Rel != "a.txt" || d.Files[0].Size != 3 {
		t.Fatalf("附件不符合预期: %+v", d.Files)
	}
}

func TestDirectoryExpansion(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "素材")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.txt", filepath.Join("sub", "b.txt")} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := newTestPipeline(t, baseSettings())
	d := p.Decide(Clip{Paths: []string{dir}})
	if !d.Upload {
		t.Fatalf("应上传，被跳过: %s", d.Skip)
	}
	var rels []string
	for _, f := range d.Files {
		rels = append(rels, f.Rel)
	}
	want := []string{"素材/a.txt", "素材/sub/b.txt"}
	if strings.Join(rels, ",") != strings.Join(want, ",") {
		t.Fatalf("相对路径应为 %v，得到 %v", want, rels)
	}
}

func TestFileLimits(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("12345678"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := newTestPipeline(t, baseSettings())
	if d := p.Decide(Clip{Paths: []string{dir}}); !d.Upload {
		t.Fatalf("3 个文件应允许上传: %s", d.Skip)
	}

	s := baseSettings()
	s.MaxFiles = 2
	p2, _ := newTestPipeline(t, s)
	if d := p2.Decide(Clip{Paths: []string{dir}}); d.Upload || !strings.Contains(d.Skip, "数量") {
		t.Fatalf("超过文件数上限应跳过，得到 %+v", d)
	}

	s = baseSettings()
	s.MaxTotalMB = 1
	s.MaxFiles = 100
	p3, _ := newTestPipeline(t, s)
	big := filepath.Join(dir, "big")
	if err := os.WriteFile(big, make([]byte, 1<<20+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if d := p3.Decide(Clip{Paths: []string{big}}); d.Upload || !strings.Contains(d.Skip, "总大小") {
		t.Fatalf("超过总量上限应跳过，得到 %+v", d)
	}
}

func TestMissingPathIsSkipped(t *testing.T) {
	p, _ := newTestPipeline(t, baseSettings())
	d := p.Decide(Clip{Paths: []string{filepath.Join(t.TempDir(), "nope")}})
	if d.Upload {
		t.Fatal("不存在的路径不应上传")
	}
}

func TestDedupeWindow(t *testing.T) {
	p, clock := newTestPipeline(t, baseSettings())
	if d := p.Decide(Clip{Text: "same"}); !d.Upload {
		t.Fatal("首次应上传")
	}
	*clock = clock.Add(10 * time.Second)
	if d := p.Decide(Clip{Text: "same"}); d.Upload {
		t.Fatal("去重窗口内重复内容应跳过")
	}
	*clock = clock.Add(31 * time.Second)
	if d := p.Decide(Clip{Text: "same"}); !d.Upload {
		t.Fatal("超出窗口后应再次上传")
	}

	s := baseSettings()
	s.DedupeSeconds = 0
	p2, _ := newTestPipeline(t, s)
	p2.Decide(Clip{Text: "same"})
	if d := p2.Decide(Clip{Text: "same"}); !d.Upload {
		t.Fatal("关闭去重后应每次都上传")
	}
}

func TestImageKindCarriesData(t *testing.T) {
	p, _ := newTestPipeline(t, baseSettings())
	png := []byte("fake-png")
	d := p.Decide(Clip{PNG: png})
	if !d.Upload || d.Kind != KindImage {
		t.Fatalf("图片应上传，得到 %+v", d)
	}
	if len(d.Files) != 1 || string(d.Files[0].Data) != string(png) {
		t.Fatalf("图片内容应随决策返回: %+v", d.Files)
	}
	if d.Files[0].Rel == "" || !strings.HasSuffix(d.Files[0].Rel, ".png") {
		t.Fatalf("图片文件名不符合预期: %q", d.Files[0].Rel)
	}
}

func TestBuildIgnoresSwitchesButEnforcesLimits(t *testing.T) {
	s := baseSettings()
	s.Enabled = false
	s.Paused = true
	s.Text = false
	p, _ := newTestPipeline(t, s)
	if d := p.Build("手动内容", nil); !d.Upload || d.Text != "手动内容" {
		t.Fatalf("手动提交不受开关影响，得到 %+v", d)
	}
	s.MaxTextBytes = 2
	p.SetSettings(s)
	if d := p.Build("手动内容", nil); d.Upload {
		t.Fatal("手动提交仍应受文本上限约束")
	}
	if d := p.Build("", nil); d.Upload {
		t.Fatal("空内容不应上传")
	}
}
