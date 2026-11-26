package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Uploader 是上传层能力（由装配层用 internal/client 实现）。
type Uploader interface {
	// Put 上传一条内容，返回服务端 item ID。onProgress 可为 nil。
	Put(ctx context.Context, text string, files []File, onProgress func(sent, total int64)) (string, error)
	// Delete 删除一条已上传内容（撤回）。
	Delete(ctx context.Context, itemID string) error
}

// Failure 是失败分类，决定是否重试。
type Failure int

const (
	FailureRetry     Failure = iota // 可重试（网络等）
	FailureAuth                     // 会话失效，可尝试重新登录一次
	FailureTooLarge                 // 413，不重试
	FailureNoSpace                  // 507，不重试
	FailureTLS                      // 证书问题，不重试
	FailurePermanent                // 其它不可重试
	FailureCanceled                 // 用户取消
)

// RecordState 是本地历史条目的状态。
type RecordState string

const (
	StateQueued    RecordState = "queued"
	StateUploading RecordState = "uploading"
	StateOK        RecordState = "ok"
	StateFailed    RecordState = "failed"
	StateCanceled  RecordState = "canceled"
	StateWithdrawn RecordState = "withdrawn"
)

// Record 是一条本机上传记录（暴露给界面）。
type Record struct {
	ID       string      `json:"id"`
	ItemID   string      `json:"item_id,omitempty"`
	At       time.Time   `json:"at"`
	Kind     Kind        `json:"kind"`
	Source   string      `json:"source"` // clipboard | manual
	State    RecordState `json:"state"`
	Size     int64       `json:"size"`
	FileN    int         `json:"file_n"`
	Text     string      `json:"text"`
	Preview  string      `json:"preview"`
	Error    string      `json:"error,omitempty"`
	Progress int         `json:"progress"`
	Attempts int         `json:"attempts"`
	Skipped  int         `json:"skipped,omitempty"`
}

// State 是界面上方状态条的数据。
type State struct {
	Paused    bool   `json:"paused"`
	Available bool   `json:"available"`
	LastSkip  string `json:"last_skip,omitempty"`
	Active    int    `json:"active"`
	Queue     int    `json:"queue"`
}

// Options 构造 Manager。
type Options struct {
	Uploader    Uploader
	Settings    Settings
	HistorySize int
	// Classify 把上传错误映射成失败分类；为空时除取消外一律可重试。
	Classify func(error) Failure
	// Reauth 在会话失效时尝试重新登录，返回 true 表示可以继续重试。
	Reauth func(ctx context.Context) (bool, error)
	Logger func(format string, args ...any)

	OnRecords func([]Record)
	OnState   func(State)

	Backoff  []time.Duration // 每次重试前的等待，默认 2s/10s/30s
	Debounce time.Duration   // 剪贴板去抖，默认 300ms
	Sleep    func(time.Duration)
	Now      func() time.Time
}

type payload struct {
	text  string
	files []File
	kind  Kind
}

// Job 是一个待执行的上传任务。
type Job struct {
	recID  string
	text   string
	files  []File
	kind   Kind
	ctx    context.Context
	cancel context.CancelFunc
}

// Manager 负责上传队列、退避重试、进度与本地历史。
type Manager struct {
	opts     Options
	pipeline *Pipeline

	mu      sync.Mutex
	records []Record // 最新在前
	payload map[string]payload
	cancels map[string]context.CancelFunc
	state   State
	closed  bool
	seq     int

	in   chan Clip
	jobs chan *Job
	stop chan struct{}
	wg   sync.WaitGroup
}

// NewManager 构造并启动 Manager。
func NewManager(o Options) *Manager {
	if o.HistorySize <= 0 {
		o.HistorySize = 50
	}
	if len(o.Backoff) == 0 {
		o.Backoff = []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second}
	}
	// 负数表示关闭去抖（测试用），零值用默认。
	if o.Debounce == 0 {
		o.Debounce = 300 * time.Millisecond
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Classify == nil {
		o.Classify = func(err error) Failure {
			if errors.Is(err, context.Canceled) {
				return FailureCanceled
			}
			return FailureRetry
		}
	}
	if o.Logger == nil {
		o.Logger = func(string, ...any) {}
	}
	m := &Manager{
		opts:     o,
		pipeline: NewPipeline(o.Settings),
		payload:  map[string]payload{},
		cancels:  map[string]context.CancelFunc{},
		in:       make(chan Clip, 8),
		jobs:     make(chan *Job, 32),
		stop:     make(chan struct{}),
	}
	m.state = State{Paused: o.Settings.Paused}
	m.wg.Add(2)
	go m.dispatchLoop()
	go m.workerLoop()
	return m
}

// Pipeline 暴露决策流水线。
func (m *Manager) Pipeline() *Pipeline { return m.pipeline }

// SetSettings 更新配置（会同步到 Pipeline）。
func (m *Manager) SetSettings(s Settings) {
	m.pipeline.SetSettings(s)
	m.mu.Lock()
	m.state.Paused = s.Paused
	m.mu.Unlock()
	m.emitState()
}

// SetAvailable 标记服务端是否可用。
func (m *Manager) SetAvailable(ok bool) {
	m.pipeline.SetAvailable(ok)
	m.mu.Lock()
	m.state.Available = ok
	m.mu.Unlock()
	m.emitState()
}

// Pause 暂停/恢复自动上传。
func (m *Manager) Pause(p bool) {
	s := m.pipeline.Settings()
	s.Paused = p
	m.pipeline.SetSettings(s)
	m.mu.Lock()
	m.state.Paused = p
	if p {
		m.state.LastSkip = "已暂停"
	}
	m.mu.Unlock()
	m.emitState()
}

// Submit 投递一次剪贴板内容（自动上传路径，带去抖与决策）。
func (m *Manager) Submit(c Clip) {
	if c.At.IsZero() {
		c.At = m.opts.Now()
	}
	select {
	case m.in <- c:
	default:
		// 通道满时丢最旧的，保留最新剪贴板状态。
		select {
		case <-m.in:
		default:
		}
		select {
		case m.in <- c:
		default:
		}
	}
}

// SubmitManual 立即上传用户提交的文本与路径，返回本地记录。
func (m *Manager) SubmitManual(text string, paths []string) (Record, error) {
	d := m.pipeline.Build(text, paths)
	if !d.Upload {
		return Record{}, fmt.Errorf("%s", d.Skip)
	}
	rec := m.newRecord(d, "manual")
	m.enqueue(rec, d)
	return rec, nil
}

// SubmitManualContent 上传已准备好的内容（例如剪贴板图片），
// 不再检查开关与去重，但仍由调用方负责限额。
func (m *Manager) SubmitManualContent(text string, kind Kind, files []File, skipped int) (Record, error) {
	if len(files) == 0 && strings.TrimSpace(text) == "" {
		return Record{}, fmt.Errorf("内容为空")
	}
	d := Decision{Upload: true, Kind: kind, Text: text, Files: files, Skipped: skipped}
	rec := m.newRecord(d, "manual")
	m.enqueue(rec, d)
	return rec, nil
}

// History 返回本地历史快照（最新在前）。
func (m *Manager) History() []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Record, len(m.records))
	copy(out, m.records)
	return out
}

// State 返回状态快照。
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Retry 重新投递一条失败记录。
func (m *Manager) Retry(id string) error {
	m.mu.Lock()
	idx := m.findIndex(id)
	if idx < 0 {
		m.mu.Unlock()
		return fmt.Errorf("记录不存在")
	}
	rec := m.records[idx]
	p, ok := m.payload[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("内容已释放，无法重试（重新复制一次即可）")
	}
	if rec.State == StateUploading || rec.State == StateQueued {
		m.mu.Unlock()
		return fmt.Errorf("正在上传中")
	}
	m.records[idx].State = StateQueued
	m.records[idx].Error = ""
	m.records[idx].Progress = 0
	m.records[idx].Attempts = 0
	m.state.Queue = m.pendingCount()
	m.mu.Unlock()
	m.emitRecords()
	m.emitState()
	m.enqueue(rec, Decision{Upload: true, Kind: p.kind, Text: p.text, Files: p.files})
	return nil
}

// Cancel 取消排队中或进行中的上传。
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	idx := m.findIndex(id)
	if idx < 0 {
		m.mu.Unlock()
		return fmt.Errorf("记录不存在")
	}
	st := m.records[idx].State
	if st != StateUploading && st != StateQueued {
		m.mu.Unlock()
		return fmt.Errorf("该记录不在上传中")
	}
	cancel := m.cancels[id]
	m.records[idx].State = StateCanceled
	m.records[idx].Error = "已取消"
	m.state.Queue = m.pendingCount()
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.emitRecords()
	m.emitState()
	return nil
}

// Withdraw 撤回一条已上传记录（删除服务端内容）。
func (m *Manager) Withdraw(ctx context.Context, id string) error {
	m.mu.Lock()
	idx := m.findIndex(id)
	if idx < 0 {
		m.mu.Unlock()
		return fmt.Errorf("记录不存在")
	}
	rec := m.records[idx]
	if rec.ItemID == "" {
		m.mu.Unlock()
		return fmt.Errorf("尚未上传成功，无法撤回")
	}
	m.mu.Unlock()
	if err := m.opts.Uploader.Delete(ctx, rec.ItemID); err != nil {
		return err
	}
	m.mu.Lock()
	if i := m.findIndex(id); i >= 0 {
		m.records[i].State = StateWithdrawn
		m.records[i].Error = ""
	}
	delete(m.payload, id)
	m.mu.Unlock()
	m.emitRecords()
	return nil
}

// Close 停止后台协程并取消进行中的上传。
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	cancels := make([]context.CancelFunc, 0, len(m.cancels))
	for _, c := range m.cancels {
		cancels = append(cancels, c)
	}
	m.mu.Unlock()
	close(m.stop)
	for _, c := range cancels {
		c()
	}
	m.wg.Wait()
}

// ---------- 内部实现 ----------

func (m *Manager) dispatchLoop() {
	defer m.wg.Done()
	for {
		select {
		case <-m.stop:
			return
		case c := <-m.in:
			if m.opts.Debounce > 0 {
				m.opts.Sleep(m.opts.Debounce)
			}
			// 合并等待期间到达的更新，只保留最新内容。
			for merged := true; merged; {
				select {
				case c2 := <-m.in:
					c = c2
				default:
					merged = false
				}
			}
			m.handleClip(c)
		}
	}
}

func (m *Manager) handleClip(c Clip) {
	d := m.pipeline.Decide(c)
	if !d.Upload {
		m.opts.Logger("跳过剪贴板内容: %s", d.Skip)
		m.mu.Lock()
		m.state.LastSkip = d.Skip
		m.mu.Unlock()
		m.emitState()
		return
	}
	if d.Skipped > 0 {
		m.opts.Logger("上传 %s：跳过 %d 个无法读取的文件", d.Kind, d.Skipped)
	}
	rec := m.newRecord(d, "clipboard")
	m.enqueue(rec, d)
}

func (m *Manager) newRecord(d Decision, source string) Record {
	var size int64
	for _, f := range d.Files {
		size += f.Size
	}
	size += int64(len(d.Text))
	rec := Record{
		ID:      m.nextID(),
		At:      m.opts.Now(),
		Kind:    d.Kind,
		Source:  source,
		State:   StateQueued,
		Size:    size,
		FileN:   len(d.Files),
		Text:    d.Text,
		Skipped: d.Skipped,
	}
	if d.Kind == KindText {
		rec.Preview = preview(d.Text)
	} else {
		rec.Preview = filesPreview(d.Files)
	}
	m.mu.Lock()
	m.records = append([]Record{rec}, m.records...)
	m.payload[rec.ID] = payload{text: d.Text, files: d.Files, kind: d.Kind}
	for len(m.records) > m.opts.HistorySize {
		dropped := m.records[len(m.records)-1]
		m.records = m.records[:len(m.records)-1]
		delete(m.payload, dropped.ID)
		if c := m.cancels[dropped.ID]; c != nil {
			c()
			delete(m.cancels, dropped.ID)
		}
	}
	m.state.Queue = m.pendingCount()
	m.mu.Unlock()
	m.emitRecords()
	m.emitState()
	return rec
}

func (m *Manager) enqueue(rec Record, d Decision) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if prev := m.cancels[rec.ID]; prev != nil {
		prev()
	}
	m.cancels[rec.ID] = cancel
	m.mu.Unlock()
	j := &Job{recID: rec.ID, text: d.Text, files: d.Files, kind: d.Kind, ctx: ctx, cancel: cancel}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		select {
		case m.jobs <- j:
		case <-m.stop:
			cancel()
		}
	}()
}

func (m *Manager) workerLoop() {
	defer m.wg.Done()
	for {
		select {
		case <-m.stop:
			return
		case j := <-m.jobs:
			m.run(j)
		}
	}
}

func (m *Manager) run(j *Job) {
	defer j.cancel()
	// 排队期间可能已被取消，或记录已被历史容量淘汰。
	m.mu.Lock()
	idx := m.findIndex(j.recID)
	if idx < 0 || m.records[idx].State == StateCanceled {
		m.mu.Unlock()
		return
	}
	m.records[idx].State = StateUploading
	m.records[idx].Progress = 0
	m.state.Active++
	m.state.Queue = m.pendingCount()
	m.mu.Unlock()
	m.emitRecords()
	m.emitState()

	reauthTried := false
	attempt := 0
	for {
		attempt++
		m.mu.Lock()
		if i := m.findIndex(j.recID); i >= 0 {
			m.records[i].Attempts = attempt
		}
		m.mu.Unlock()

		err := m.put(j)
		if err == nil {
			m.finish(j.recID, StateOK, "")
			return
		}
		switch m.opts.Classify(err) {
		case FailureCanceled:
			m.finish(j.recID, StateCanceled, "已取消")
			return
		case FailureTooLarge, FailureNoSpace, FailureTLS, FailurePermanent:
			m.finish(j.recID, StateFailed, err.Error())
			return
		case FailureAuth:
			if reauthTried || m.opts.Reauth == nil {
				m.finish(j.recID, StateFailed, "登录已失效，请重新登录")
				return
			}
			reauthTried = true
			ok, rerr := m.opts.Reauth(j.ctx)
			if rerr != nil || !ok {
				m.finish(j.recID, StateFailed, "登录已失效，请重新登录")
				return
			}
			attempt-- // 重新登录不消耗重试次数
			continue
		}
		if attempt > len(m.opts.Backoff) {
			m.finish(j.recID, StateFailed, err.Error())
			return
		}
		m.opts.Logger("上传失败（第 %d 次），%s 后重试: %v", attempt, m.opts.Backoff[attempt-1], err)
		select {
		case <-j.ctx.Done():
		case <-m.stop:
		default:
			m.opts.Sleep(m.opts.Backoff[attempt-1])
		}
		if j.ctx.Err() != nil {
			m.finish(j.recID, StateCanceled, "已取消")
			return
		}
	}
}

func (m *Manager) put(j *Job) error {
	text := j.text
	if prefix := strings.TrimSpace(m.pipeline.Settings().TextPrefix); prefix != "" {
		if j.kind == KindText && strings.TrimSpace(text) != "" {
			text = prefix + "\n" + text
		} else if j.kind != KindText && text == "" {
			text = prefix
		}
	}
	var lastNotify time.Time
	id, err := m.opts.Uploader.Put(j.ctx, text, j.files, func(sent, total int64) {
		if total <= 0 {
			return
		}
		pct := int(sent * 100 / total)
		now := m.opts.Now()
		m.mu.Lock()
		if i := m.findIndex(j.recID); i >= 0 {
			m.records[i].Progress = pct
		}
		throttle := now.Sub(lastNotify) < 200*time.Millisecond
		m.mu.Unlock()
		if !throttle {
			lastNotify = now
			m.emitRecords()
		}
	})
	if err != nil {
		return err
	}
	m.mu.Lock()
	if i := m.findIndex(j.recID); i >= 0 {
		m.records[i].ItemID = id
		m.records[i].Progress = 100
	}
	// 成功后释放内存中的图片内容，避免历史长期占用内存。
	if p, ok := m.payload[j.recID]; ok {
		for i := range p.files {
			p.files[i].Data = nil
		}
		m.payload[j.recID] = p
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) finish(id string, st RecordState, msg string) {
	m.mu.Lock()
	if i := m.findIndex(id); i >= 0 {
		m.records[i].State = st
		m.records[i].Error = msg
		if st == StateOK {
			m.records[i].Progress = 100
		}
	}
	delete(m.cancels, id)
	if m.state.Active > 0 {
		m.state.Active--
	}
	m.state.Queue = m.pendingCount()
	if st == StateFailed || st == StateCanceled {
		m.state.LastSkip = msg
	}
	m.mu.Unlock()
	m.emitRecords()
	m.emitState()
}

func (m *Manager) findIndex(id string) int {
	for i := range m.records {
		if m.records[i].ID == id {
			return i
		}
	}
	return -1
}

func (m *Manager) pendingCount() int {
	n := 0
	for i := range m.records {
		if m.records[i].State == StateQueued || m.records[i].State == StateUploading {
			n++
		}
	}
	return n
}

func (m *Manager) nextID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	return fmt.Sprintf("r%d-%d", m.opts.Now().UnixNano(), m.seq)
}

func (m *Manager) emitRecords() {
	if m.opts.OnRecords == nil {
		return
	}
	m.opts.OnRecords(m.History())
}

func (m *Manager) emitState() {
	if m.opts.OnState == nil {
		return
	}
	m.opts.OnState(m.State())
}

func preview(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return string(r)
}

func filesPreview(files []File) string {
	if len(files) == 0 {
		return ""
	}
	if len(files) == 1 {
		return files[0].Rel
	}
	return fmt.Sprintf("%s 等 %d 个文件", files[0].Rel, len(files))
}
