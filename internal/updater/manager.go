package updater

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager 层的错误。
var (
	// ErrBusy 表示已有升级正在进行。
	ErrBusy = errors.New("升级进行中")
	// ErrVersionMismatch 表示请求的版本与最近一次检查结果不一致。
	ErrVersionMismatch = errors.New("版本不一致，请重新检查更新")
	// ErrNotSupported 表示当前宿主不支持在线更新（如 Android）。
	ErrNotSupported = errors.New("当前运行环境不支持在线更新")
)

// Stamp 是零值序列化成 null 的时间戳（encoding/json 的 omitempty 对 time.Time 无效）。
type Stamp struct{ time.Time }

func (s Stamp) MarshalJSON() ([]byte, error) {
	if s.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(s.Time)
}

func (s *Stamp) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		s.Time = time.Time{}
		return nil
	}
	return json.Unmarshal(b, &s.Time)
}

// State 是对外暴露的更新状态快照（HTTP JSON / Wails 绑定共用）。
type State struct {
	Current     string `json:"current"`
	Latest      string `json:"latest,omitempty"`
	Pending     string `json:"pending,omitempty"` // 已替换、等重启生效的版本
	Notes       string `json:"notes,omitempty"`
	Published   Stamp  `json:"published"`
	Size        int64  `json:"size,omitempty"`
	CheckedAt   Stamp  `json:"checked_at"`
	CheckError  string `json:"check_error,omitempty"`
	Available   bool   `json:"available"`
	Updating    bool   `json:"updating"`
	Progress    int    `json:"progress"`
	CanRollback bool   `json:"can_rollback"`
	Supported   bool   `json:"supported"`
}

// ManagerOptions 是构造 Manager 的输入。
type ManagerOptions struct {
	Updater *Updater // nil = 该宿主不支持在线更新
	ExePath string   // 当前可执行文件路径
	// Restart 非阻塞地请求宿主重启进程（服务端：OnRestart；agent：退出并拉起新进程）。
	Restart func()
	OnState func(State) // 状态变化回调，可为 nil
	Log     *log.Logger // 可为 nil
	Now     func() time.Time
	// After 用于后台检查的等待，测试可替换。
	After func(time.Duration) <-chan time.Time
}

// Manager 持有检查缓存、升级互斥与进度，是服务端与 agent 共用的门面。
type Manager struct {
	up      *Updater
	exe     string
	restart func()
	onState func(State)
	log     *log.Logger
	now     func() time.Time
	after   func(time.Duration) <-chan time.Time

	mu       sync.Mutex
	st       State
	rel      Release
	applying bool
}

// NewManager 构造 Manager；Updater 为 nil 时得到一个「不支持更新」的空实现。
func NewManager(o ManagerOptions) *Manager {
	if o.Log == nil {
		o.Log = log.New(os.Stderr, "", log.LstdFlags)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.After == nil {
		o.After = time.After
	}
	m := &Manager{up: o.Updater, exe: o.ExePath, restart: o.Restart, onState: o.OnState,
		log: o.Log, now: o.Now, after: o.After}
	if m.up != nil {
		m.st = State{
			Current:     m.up.Current(),
			Supported:   true,
			CanRollback: HasBackup(m.exe),
		}
		// 上次升级中断留下的临时文件在这里清掉。
		if n := CleanupTemps(filepath.Dir(m.exe)); n > 0 {
			m.log.Printf("info: 清理了 %d 个遗留的更新临时文件", n)
		}
	} else {
		m.st = State{Current: "unknown"}
	}
	return m
}

// State 返回当前快照。
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

// Check 立即联网检查。失败时保留上一次的可用信息，只记录错误。
func (m *Manager) Check(ctx context.Context) (State, error) {
	m.mu.Lock()
	if m.up == nil {
		st := m.st
		m.mu.Unlock()
		return st, ErrNotSupported
	}
	if m.applying {
		st := m.st
		m.mu.Unlock()
		return st, ErrBusy
	}
	up := m.up
	m.mu.Unlock()

	rel, err := up.Check(ctx)

	m.mu.Lock()
	m.st.CheckedAt = Stamp{m.now()}
	if err != nil {
		if errors.Is(err, ErrUpToDate) {
			m.st.Latest = rel.Version
			m.st.Available = false
			m.st.CheckError = ""
			m.rel = rel
			st := m.st
			m.mu.Unlock()
			m.emit(st)
			return st, nil
		}
		m.st.CheckError = err.Error()
		st := m.st
		m.mu.Unlock()
		m.log.Printf("warn: 检查更新失败: %v", err)
		m.emit(st)
		return st, err
	}
	m.rel = rel
	m.st.Latest = rel.Version
	m.st.Notes = rel.Notes
	m.st.Published = Stamp{rel.Published}
	m.st.Size = rel.Size
	m.st.Available = true
	m.st.CheckError = ""
	st := m.st
	m.mu.Unlock()
	m.log.Printf("info: 发现新版本 %s（当前 %s）", rel.Version, st.Current)
	m.emit(st)
	return st, nil
}

// Apply 下载、校验并替换当前可执行文件，然后请求宿主重启。
//
// version 必须与最近一次检查到的版本一致（防止拿旧缓存误升级）。
func (m *Manager) Apply(ctx context.Context, version string) (State, error) {
	m.mu.Lock()
	if m.up == nil {
		st := m.st
		m.mu.Unlock()
		return st, ErrNotSupported
	}
	if m.applying {
		st := m.st
		m.mu.Unlock()
		return st, ErrBusy
	}
	rel := m.rel
	if !m.st.Available || rel.SHA256 == "" || rel.Version != normalizeVersion(version) {
		st := m.st
		m.mu.Unlock()
		return st, ErrVersionMismatch
	}
	m.applying = true
	m.st.Updating = true
	m.st.Progress = 0
	up, exe := m.up, m.exe
	st := m.st
	m.mu.Unlock()
	m.log.Printf("info: 开始升级到 %s", rel.Version)
	m.emit(st)

	finish := func() {
		m.mu.Lock()
		m.applying = false
		m.st.Updating = false
		st := m.st
		m.mu.Unlock()
		m.emit(st)
	}

	tmp, err := up.Download(ctx, filepath.Dir(exe), rel, m.setProgress)
	if err != nil {
		finish()
		m.log.Printf("warn: 升级到 %s 失败: %v", rel.Version, err)
		return m.State(), err
	}
	backup, err := Stage(exe, tmp)
	if err != nil {
		finish()
		m.log.Printf("warn: 替换可执行文件失败: %v", err)
		return m.State(), err
	}
	m.log.Printf("info: 已替换为 %s（旧版本备份在 %s），请求重启", rel.Version, backup)

	m.mu.Lock()
	m.applying = false
	m.st.Updating = false
	m.st.Progress = 100
	m.st.Available = false
	m.st.Pending = rel.Version
	m.st.CanRollback = true
	st = m.st
	m.mu.Unlock()
	m.emit(st)

	if m.restart != nil {
		m.restart()
	}
	return st, nil
}

// Rollback 把 .old 备份换回来并请求重启。
func (m *Manager) Rollback() (State, error) {
	m.mu.Lock()
	if m.up == nil {
		st := m.st
		m.mu.Unlock()
		return st, ErrNotSupported
	}
	if m.applying {
		st := m.st
		m.mu.Unlock()
		return st, ErrBusy
	}
	exe := m.exe
	m.applying = true
	m.st.Updating = true
	st := m.st
	m.mu.Unlock()
	m.emit(st)

	failed, err := Rollback(exe)

	m.mu.Lock()
	m.applying = false
	m.st.Updating = false
	m.st.Progress = 0
	st = m.st
	m.mu.Unlock()

	if err != nil {
		m.emit(st)
		return m.State(), err
	}
	m.log.Printf("info: 已回滚（当前版本留作 %s），请求重启", failed)
	m.mu.Lock()
	m.st.CanRollback = false
	m.st.Pending = ""
	m.st.Available = false
	st = m.st
	m.mu.Unlock()
	m.emit(st)
	if m.restart != nil {
		m.restart()
	}
	return st, nil
}

// Run 按 mode（off/startup/daily）在后台检查；ctx 取消即退出。非阻塞。
func (m *Manager) Run(ctx context.Context, mode string) {
	if m.up == nil || mode == "" || mode == "off" {
		return
	}
	go func() {
		// 起步等一会儿，别和启动时的证书/监听抢资源。
		select {
		case <-ctx.Done():
			return
		case <-m.after(10 * time.Second):
		}
		m.checkOnce(ctx)
		if mode != "daily" {
			return
		}
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.checkOnce(ctx)
			}
		}
	}()
}

// checkOnce 是后台检查：错误已经在 Check 里记日志，这里不用再管。
func (m *Manager) checkOnce(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, _ = m.Check(ctx) // 结果与错误都已在缓存/日志里
}

// setProgress 是下载进度回调（节流由 Download 内部负责）。
func (m *Manager) setProgress(done, total int64) {
	p := 0
	if total > 0 {
		p = int(done * 100 / total)
	}
	m.mu.Lock()
	if p <= m.st.Progress || p > 100 {
		m.mu.Unlock()
		return
	}
	m.st.Progress = p
	st := m.st
	m.mu.Unlock()
	m.emit(st)
}

func (m *Manager) emit(st State) {
	if m.onState != nil {
		m.onState(st)
	}
}

// VersionLine 便于日志与 CLI 展示：「当前 1.2.3，最新 1.2.4」。
func (st State) VersionLine() string {
	cur := strings.TrimSpace(st.Current)
	if cur == "" {
		cur = "unknown"
	}
	switch {
	case st.CheckError != "":
		return cur + "（检查失败：" + st.CheckError + "）"
	case st.Available:
		return cur + " → " + st.Latest
	default:
		return cur + "（已是最新）"
	}
}
