package store

import (
	"bytes"
	"errors"
	"testing"
)

func TestStatsEmpty(t *testing.T) {
	s := newStore(t, Options{})
	if got := s.Stats(); got != (Stats{}) {
		t.Fatalf("空库统计应全 0，得到 %+v", got)
	}
}

func TestStatsCountsItemsAndFiles(t *testing.T) {
	s := newStore(t, Options{})
	if _, err := s.Create(CreateInput{Text: "只有文字", CreatedBy: "alice"}); err != nil {
		t.Fatal(err)
	}
	it, err := s.Create(CreateInput{Text: "带附件", CreatedBy: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFile(it.ID, "a.txt", 3, bytes.NewReader([]byte("aaa"))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFile(it.ID, "d/b.bin", 5, bytes.NewReader([]byte("bbbbb"))); err != nil {
		t.Fatal(err)
	}
	taken, err := s.Create(CreateInput{Text: "已取走", CreatedBy: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTaken(taken.ID, true, "dave"); err != nil {
		t.Fatal(err)
	}

	got := s.Stats()
	want := Stats{Items: 3, Todo: 2, Taken: 1, Files: 2, Bytes: 8}
	if got != want {
		t.Fatalf("统计不对：want %+v, got %+v", want, got)
	}
}

func TestStatsCountsEveryFile(t *testing.T) {
	s := newStore(t, Options{})
	it, err := s.Create(CreateInput{Text: "多附件", CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		name := string(rune('a'+i)) + ".txt"
		if _, err := s.AddFile(it.ID, name, 2, bytes.NewReader([]byte("xy"))); err != nil {
			t.Fatal(err)
		}
	}
	got := s.Stats()
	if got.Items != 1 || got.Files != 3 || got.Bytes != 6 {
		t.Fatalf("统计不对: %+v", got)
	}
}

func TestFreeBytesUsesInjectedProbe(t *testing.T) {
	want := int64(123456789)
	probed := ""
	s := newStore(t, Options{FreeSpace: func(path string) (int64, error) {
		probed = path
		return want, nil
	}})
	got, err := s.FreeBytes()
	if err != nil {
		t.Fatalf("FreeBytes 失败: %v", err)
	}
	if got != want {
		t.Fatalf("可用空间应为 %d，得到 %d", want, got)
	}
	if probed != s.itemsRoot {
		t.Fatalf("应探测 items 目录 %q，实际 %q", s.itemsRoot, probed)
	}
}

func TestFreeBytesPropagatesError(t *testing.T) {
	boom := errors.New("statfs 失败")
	s := newStore(t, Options{FreeSpace: func(string) (int64, error) { return 0, boom }})
	if _, err := s.FreeBytes(); !errors.Is(err, boom) {
		t.Fatalf("应透传探测错误，得到 %v", err)
	}
}
