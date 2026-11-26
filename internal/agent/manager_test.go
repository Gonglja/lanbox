package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeUploader struct {
	mu       sync.Mutex
	calls    []string // 每次上传的文本（去重后）
	files    [][]File
	fail     []error // 按调用序号返回的错误，用尽后成功
	itemSeq  int
	deleted  []string
	blocking bool // Put 阻塞直到 ctx 取消
	started  chan struct{}
}

func (f *fakeUploader) Put(ctx context.Context, text string, files []File, onProgress func(sent, total int64)) (string, error) {
	f.mu.Lock()
	idx := len(f.calls)
	f.calls = append(f.calls, text)
	f.files = append(f.files, files)
	var err error
	if idx < len(f.fail) {
		err = f.fail[idx]
	}
	blocking := f.blocking
	started := f.started
	f.mu.Unlock()

	if onProgress != nil {
		onProgress(int64(len(text))/2, int64(len(text)))
		onProgress(int64(len(text)), int64(len(text)))
	}
	if blocking {
		if started != nil {
			select {
			case started <- struct{}{}:
			default:
			}
		}
		<-ctx.Done()
		return "", ctx.Err()
	}
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	f.itemSeq++
	id := fmt.Sprintf("item-%d", f.itemSeq)
	f.mu.Unlock()
	return id, nil
}

func (f *fakeUploader) Delete(ctx context.Context, itemID string) error {
	f.mu.Lock()
	f.deleted = append(f.deleted, itemID)
	f.mu.Unlock()
	return nil
}

func (f *fakeUploader) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func testSettings() Settings {
	s := baseSettings()
	return s
}

type harness struct {
	m      *Manager
	up     *fakeUploader
	sleeps []time.Duration
	mu     sync.Mutex
	state  State
	recs   []Record
}

func newHarness(t *testing.T, o Options) *harness {
	t.Helper()
	h := &harness{up: &fakeUploader{}}
	if o.Uploader == nil {
		o.Uploader = h.up
	} else if up, ok := o.Uploader.(*fakeUploader); ok {
		h.up = up
	}
	o.Debounce = -1
	o.Sleep = func(d time.Duration) { h.mu.Lock(); h.sleeps = append(h.sleeps, d); h.mu.Unlock() }
	o.OnState = func(s State) { h.mu.Lock(); h.state = s; h.mu.Unlock() }
	o.OnRecords = func(r []Record) { h.mu.Lock(); h.recs = r; h.mu.Unlock() }
	h.m = NewManager(o)
	t.Cleanup(h.m.Close)
	return h
}

func (h *harness) waitState(t *testing.T, pred func(Record) bool) Record {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		recs := append([]Record(nil), h.recs...)
		h.mu.Unlock()
		for _, r := range recs {
			if pred(r) {
				return r
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待状态超时；当前记录: %+v", h.m.History())
	return Record{}
}

func TestSubmitTextUploads(t *testing.T) {
	h := newHarness(t, Options{Settings: fullSettings(), HistorySize: 10})
	h.m.SetAvailable(true)
	h.m.Submit(Clip{Text: "hello"})
	rec := h.waitState(t, func(r Record) bool { return r.State == StateOK })
	if rec.ItemID != "item-1" || rec.Kind != KindText || rec.Preview != "hello" {
		t.Fatalf("上传记录不符合预期: %+v", rec)
	}
	if h.up.count() != 1 {
		t.Fatalf("应只上传一次，实际 %d", h.up.count())
	}
}

func fullSettings() Settings {
	s := baseSettings()
	s.Enabled = true
	s.Paused = false
	return s
}

func TestPauseAndAvailabilityBlockUpload(t *testing.T) {
	h := newHarness(t, Options{Settings: fullSettings()})
	h.m.SetAvailable(true)
	h.m.Pause(true)
	h.m.Submit(Clip{Text: "while-paused"})
	time.Sleep(80 * time.Millisecond)
	if h.up.count() != 0 {
		t.Fatal("暂停时不应上传")
	}
	if h.m.State().LastSkip == "" {
		t.Fatal("暂停时应给出跳过原因")
	}

	h.m.Pause(false)
	h.m.SetAvailable(false)
	h.m.Submit(Clip{Text: "offline"})
	time.Sleep(80 * time.Millisecond)
	if h.up.count() != 0 {
		t.Fatal("未连接服务器时不应上传")
	}
}

func TestDedupeInManager(t *testing.T) {
	h := newHarness(t, Options{Settings: fullSettings()})
	h.m.SetAvailable(true)
	h.m.Submit(Clip{Text: "dup"})
	h.waitState(t, func(r Record) bool { return r.State == StateOK })
	h.m.Submit(Clip{Text: "dup"})
	time.Sleep(100 * time.Millisecond)
	if h.up.count() != 1 {
		t.Fatalf("去重窗口内应只上传一次，实际 %d", h.up.count())
	}
}

func TestRetryBackoffThenSuccess(t *testing.T) {
	up := &fakeUploader{fail: []error{errors.New("network"), errors.New("network")}}
	h := newHarness(t, Options{Settings: fullSettings(), Uploader: up})
	h.m.SetAvailable(true)
	h.m.Submit(Clip{Text: "retry-me"})
	rec := h.waitState(t, func(r Record) bool { return r.State == StateOK })
	if rec.Attempts != 3 {
		t.Fatalf("应第 3 次成功，Attempts=%d", rec.Attempts)
	}
	up.mu.Lock()
	n := len(up.calls)
	up.mu.Unlock()
	if n != 3 {
		t.Fatalf("应调用 3 次，实际 %d", n)
	}
	h.mu.Lock()
	sleeps := append([]time.Duration(nil), h.sleeps...)
	h.mu.Unlock()
	if len(sleeps) != 2 || sleeps[0] != 2*time.Second || sleeps[1] != 10*time.Second {
		t.Fatalf("退避序列应为 2s/10s，得到 %v", sleeps)
	}
}

func TestTooLargeDoesNotRetry(t *testing.T) {
	tooLarge := errors.New("item too large")
	up := &fakeUploader{fail: []error{tooLarge}}
	h := newHarness(t, Options{
		Settings: fullSettings(), Uploader: up,
		Classify: func(err error) Failure { return FailureTooLarge },
	})
	h.m.SetAvailable(true)
	h.m.Submit(Clip{Text: "big"})
	rec := h.waitState(t, func(r Record) bool { return r.State == StateFailed })
	if rec.Attempts != 1 {
		t.Fatalf("413 不应重试，Attempts=%d", rec.Attempts)
	}
	if h.up.count() != 1 {
		t.Fatalf("应只尝试一次，实际 %d", h.up.count())
	}
}

func TestAuthTriggersReauthOnce(t *testing.T) {
	up := &fakeUploader{fail: []error{errors.New("401")}}
	reauth := 0
	h := newHarness(t, Options{
		Settings: fullSettings(), Uploader: up,
		Classify: func(err error) Failure { return FailureAuth },
		Reauth: func(ctx context.Context) (bool, error) {
			reauth++
			return false, nil
		},
	})
	h.m.SetAvailable(true)
	h.m.Submit(Clip{Text: "auth"})
	rec := h.waitState(t, func(r Record) bool { return r.State == StateFailed })
	if reauth != 1 {
		t.Fatalf("应尝试重新登录 1 次，实际 %d", reauth)
	}
	if rec.ItemID != "" {
		t.Fatal("失败记录不应有 item id")
	}
}

func TestWithdrawDeletesRemote(t *testing.T) {
	h := newHarness(t, Options{Settings: fullSettings()})
	h.m.SetAvailable(true)
	h.m.SubmitManual("withdraw-me", nil)
	rec := h.waitState(t, func(r Record) bool { return r.State == StateOK })
	if err := h.m.Withdraw(context.Background(), rec.ID); err != nil {
		t.Fatalf("撤回失败: %v", err)
	}
	h.up.mu.Lock()
	deleted := append([]string(nil), h.up.deleted...)
	h.up.mu.Unlock()
	if len(deleted) != 1 || deleted[0] != rec.ItemID {
		t.Fatalf("应删除服务端内容 %q，实际 %v", rec.ItemID, deleted)
	}
	got := h.waitState(t, func(r Record) bool { return r.State == StateWithdrawn })
	if got.ID != rec.ID {
		t.Fatalf("状态应为 withdrawn: %+v", got)
	}
}

func TestManualUploadIgnoresPause(t *testing.T) {
	h := newHarness(t, Options{Settings: fullSettings()})
	h.m.SetAvailable(true)
	h.m.Pause(true)
	if _, err := h.m.SubmitManual("manual", nil); err != nil {
		t.Fatalf("手动上传不应被暂停阻止: %v", err)
	}
	h.waitState(t, func(r Record) bool { return r.State == StateOK })
}

func TestCancelInFlight(t *testing.T) {
	up := &fakeUploader{blocking: true, started: make(chan struct{}, 1)}
	h := newHarness(t, Options{Settings: fullSettings(), Uploader: up})
	h.m.SetAvailable(true)
	rec, err := h.m.SubmitManual("cancel-me", nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-up.started:
	case <-time.After(2 * time.Second):
		t.Fatal("上传未开始")
	}
	if err := h.m.Cancel(rec.ID); err != nil {
		t.Fatalf("取消失败: %v", err)
	}
	got := h.waitState(t, func(r Record) bool { return r.State == StateCanceled })
	if got.ID != rec.ID {
		t.Fatalf("应为已取消: %+v", got)
	}
}

func TestHistoryCapAndPayloadRelease(t *testing.T) {
	h := newHarness(t, Options{Settings: fullSettings(), HistorySize: 3})
	h.m.SetAvailable(true)
	for i := 0; i < 5; i++ {
		if _, err := h.m.SubmitManual(fmt.Sprintf("m%d", i), nil); err != nil {
			t.Fatal(err)
		}
		h.waitState(t, func(r Record) bool { return r.State == StateOK })
	}
	if got := len(h.m.History()); got != 3 {
		t.Fatalf("历史应截断到 3 条，实际 %d", got)
	}
}

func TestTextPrefixApplied(t *testing.T) {
	s := fullSettings()
	s.TextPrefix = "📋 来自 PC"
	h := newHarness(t, Options{Settings: s})
	h.m.SetAvailable(true)
	h.m.Submit(Clip{Text: "body"})
	h.waitState(t, func(r Record) bool { return r.State == StateOK })
	h.up.mu.Lock()
	got := h.up.calls[0]
	h.up.mu.Unlock()
	if got != "📋 来自 PC\nbody" {
		t.Fatalf("前缀应用错误: %q", got)
	}
}

func TestProgressReaches100(t *testing.T) {
	h := newHarness(t, Options{Settings: fullSettings()})
	h.m.SetAvailable(true)
	h.m.Submit(Clip{Text: "progress"})
	rec := h.waitState(t, func(r Record) bool { return r.State == StateOK })
	if rec.Progress != 100 {
		t.Fatalf("成功后进度应为 100，得到 %d", rec.Progress)
	}
}
