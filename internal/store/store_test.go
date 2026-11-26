package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T, opts Options) *Store {
	t.Helper()
	if opts.MaxFileBytes == 0 {
		opts.MaxFileBytes = 8 << 20
	}
	s, err := Open(filepath.Join(t.TempDir(), "items"), opts)
	if err != nil {
		t.Fatalf("打开存储失败: %v", err)
	}
	return s
}

func TestCreateAndGet(t *testing.T) {
	s := newStore(t, Options{})
	it, err := s.Create(CreateInput{Text: "  hello  ", CreatedBy: "alice", SourceIP: "1.2.3.4"})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if it.Text != "hello" {
		t.Fatalf("文本应被裁剪空白，得到 %q", it.Text)
	}
	if it.Files == nil || len(it.Files) != 0 {
		t.Fatalf("新建内容应含空文件切片（而非 nil）: %+v", it.Files)
	}
	got, err := s.Get(it.ID)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if got.ID != it.ID || got.CreatedBy != "alice" || got.SourceIP != "1.2.3.4" {
		t.Fatalf("读回内容不一致: %+v", got)
	}
	if got.Taken {
		t.Fatal("新建内容不应是已取走")
	}
	if _, err := os.Stat(filepath.Join(s.itemsRoot, it.ID, "meta.json")); err != nil {
		t.Fatalf("meta.json 未落盘: %v", err)
	}
	if _, err := s.Get("不存在"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，得到 %v", err)
	}
}

func TestIDsAreSortableByCreation(t *testing.T) {
	s := newStore(t, Options{})
	var ids []string
	for i := 0; i < 3; i++ {
		it, err := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, it.ID)
		time.Sleep(2 * time.Millisecond)
	}
	if !(ids[0] < ids[1] && ids[1] < ids[2]) {
		t.Fatalf("ID 字典序应等于时间序: %v", ids)
	}
}

func TestListOrderCursorAndFilters(t *testing.T) {
	s := newStore(t, Options{})
	var created []*Item
	for i := 0; i < 5; i++ {
		it, err := s.Create(CreateInput{Text: "msg" + string(rune('a'+i)), CreatedBy: "u"})
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, it)
		time.Sleep(2 * time.Millisecond)
	}
	if _, err := s.SetTaken(created[0].ID, true, "bob"); err != nil {
		t.Fatal(err)
	}

	res, err := s.List(ListQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 || res.Items[0].ID != created[4].ID || res.Items[1].ID != created[3].ID {
		t.Fatalf("应按时间倒序: %+v", res.Items)
	}
	if res.NextCursor != created[3].ID {
		t.Fatalf("游标应为最后一个已返回 id，得到 %q", res.NextCursor)
	}

	rest, err := s.List(ListQuery{Limit: 10, Before: res.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(rest.Items) != 3 || rest.NextCursor != "" {
		t.Fatalf("第二页应有 3 条且无游标: len=%d cursor=%q", len(rest.Items), rest.NextCursor)
	}

	todo, err := s.List(ListQuery{Todo: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(todo.Items) != 4 {
		t.Fatalf("未取走应为 4 条，得到 %d", len(todo.Items))
	}

	found, err := s.List(ListQuery{Query: "MSGC"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Items) != 1 || found.Items[0].ID != created[2].ID {
		t.Fatalf("搜索应大小写不敏感匹配 1 条: %+v", found.Items)
	}
}

func TestSetTakenToggle(t *testing.T) {
	s := newStore(t, Options{})
	it, _ := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
	got, err := s.SetTaken(it.ID, true, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Taken || got.TakenBy != "bob" || got.TakenAt == nil {
		t.Fatalf("标记已取走失败: %+v", got)
	}
	back, err := s.SetTaken(it.ID, false, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if back.Taken || back.TakenAt != nil || back.TakenBy != "" {
		t.Fatalf("取消取走应清空标记: %+v", back)
	}
	reloaded, err := s.Get(it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Taken {
		t.Fatal("取消取走未持久化")
	}
}

func TestDeleteRemovesDirectory(t *testing.T) {
	s := newStore(t, Options{})
	it, _ := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
	dir := filepath.Join(s.itemsRoot, it.ID)
	if err := s.Delete(it.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("目录应已删除: %v", err)
	}
	if _, err := s.Get(it.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("索引中应已移除")
	}
	if err := s.Delete(it.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除应返回 ErrNotFound，得到 %v", err)
	}
}

func TestOpenSkipsCorruptMeta(t *testing.T) {
	root := filepath.Join(t.TempDir(), "items")
	good, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	it, _ := good.Create(CreateInput{Text: "ok", CreatedBy: "u"})
	badDir := filepath.Join(root, "9999999999999-deadbeef", "files")
	if err := os.MkdirAll(badDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "9999999999999-deadbeef", "meta.json"), []byte("{坏数据"), 0o640); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root, Options{})
	if err != nil {
		t.Fatalf("损坏内容不应导致启动失败: %v", err)
	}
	res, err := reopened.List(ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].ID != it.ID {
		t.Fatalf("应只加载 1 条正常内容，得到 %d", len(res.Items))
	}
}

func TestGetReturnsCopy(t *testing.T) {
	s := newStore(t, Options{})
	it, _ := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
	it.Text = strings.Repeat("y", 10)
	again, _ := s.Get(it.ID)
	if again.Text != "x" {
		t.Fatalf("外部修改返回值污染了存储: %q", again.Text)
	}
}

func TestSetText(t *testing.T) {
	s := newStore(t, Options{MaxTextBytes: 8})
	it, _ := s.Create(CreateInput{CreatedBy: "u"})
	got, err := s.SetText(it.ID, "  hi  ")
	if err != nil {
		t.Fatalf("设置文本失败: %v", err)
	}
	if got.Text != "hi" {
		t.Fatalf("文本应裁剪空白: %q", got.Text)
	}
	reloaded, _ := s.Get(it.ID)
	if reloaded.Text != "hi" {
		t.Fatal("文本未持久化")
	}
	if _, err := s.SetText(it.ID, strings.Repeat("x", 9)); !errors.Is(err, ErrTextTooLong) {
		t.Fatalf("超长文本应报 ErrTextTooLong，得到 %v", err)
	}
	if _, err := s.SetText("nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("内容不存在应报 ErrNotFound，得到 %v", err)
	}
}
