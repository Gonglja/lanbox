//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"lanbox/internal/agent"
	"lanbox/internal/client"
	"lanbox/internal/mdns"
	"lanbox/internal/session"
	"lanbox/internal/winclip"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// UIState 是界面状态条与登录页需要的全部状态。
type UIState struct {
	Version     string `json:"version"`
	Server      string `json:"server"`
	Username    string `json:"username"`
	LoggedIn    bool   `json:"logged_in"`
	Enabled     bool   `json:"enabled"`
	Paused      bool   `json:"paused"`
	Listening   bool   `json:"listening"`
	Available   bool   `json:"available"`
	// 断线重连状态：界面用它决定状态点颜色、顶部文案与提示条。
	Connected         bool   `json:"connected"`
	Reconnecting      bool   `json:"reconnecting"`
	ReconnectAttempts int    `json:"reconnect_attempts"`
	NextRetryAt       int64  `json:"next_retry_at"` // epoch 毫秒，前端自己走秒
	ReconnectStopped  bool   `json:"reconnect_stopped"`
	ReconnectReason   string `json:"reconnect_reason"`
	HasPassword bool   `json:"has_password"`
	LastErr     string `json:"last_err"`
	LastSkip    string `json:"last_skip"`
	Active      int    `json:"active"`
	Queue       int    `json:"queue"`
	LogPath     string `json:"log_path"`
	DataDir     string `json:"data_dir"`
	Autostart   bool   `json:"autostart"`
	CloseTray   bool   `json:"close_to_tray"`
}

// Found 是一个 mDNS 发现结果（给界面列表用）。
type Found struct {
	Instance string   `json:"instance"`
	Host     string   `json:"host"`
	URL      string   `json:"url"`
	Addrs    []string `json:"addrs"`
	FP       string   `json:"fp"`
	Version  string   `json:"version"`
}

// LoginRequest 是登录参数。
type LoginRequest struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	PinFP    string `json:"pin_fp"`
	Remember bool   `json:"remember"`
}

// clientHolder 持有当前登录客户端，供上传器与各绑定方法共用。
type clientHolder struct {
	mu  sync.RWMutex
	cli *client.Client
}

func (h *clientHolder) set(c *client.Client) {
	h.mu.Lock()
	h.cli = c
	h.mu.Unlock()
}

func (h *clientHolder) get() *client.Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cli
}

// uploader 把 internal/agent 的 Uploader 接口接到 internal/client。
type uploader struct {
	holder *clientHolder
}

func (u *uploader) Put(ctx context.Context, text string, files []agent.File, onProgress func(sent, total int64)) (string, error) {
	cli := u.holder.get()
	if cli == nil {
		return "", errors.New("尚未登录")
	}
	ups := make([]client.Upload, 0, len(files))
	for _, f := range files {
		f := f
		ups = append(ups, client.Upload{
			Path: f.Rel,
			Size: f.Size,
			Open: func() (io.ReadCloser, error) {
				if f.Data != nil {
					return io.NopCloser(bytes.NewReader(f.Data)), nil
				}
				fh, err := os.Open(f.Path)
				if err != nil {
					return nil, err
				}
				return fh, nil
			},
		})
	}
	item, err := cli.Put(ctx, text, ups, onProgress)
	if err != nil {
		return "", err
	}
	return item.ID, nil
}

func (u *uploader) Delete(ctx context.Context, itemID string) error {
	cli := u.holder.get()
	if cli == nil {
		return errors.New("尚未登录")
	}
	return cli.DeleteItem(ctx, itemID)
}

// App 是暴露给前端的绑定对象。
type App struct {
	ctx     context.Context
	version string
	logger  *log.Logger
	holder  *clientHolder

	mu       sync.Mutex
	settings Settings
	sess     *client.Session
	httpc    interface{} // 仅用于对比，实际客户端在 holder 里
	found    []mdns.Service
	lastErr  string
	mgr      *agent.Manager
	rc       *agent.Reconnector
	// saveSession 默认写 ~/.config/lanbox/session.json；测试里替换成不落盘，
	// 免得把开发机上的真实会话覆盖掉。
	saveSession func(*client.Session) error
	watcher  *winclip.Watcher
	watching bool
	quitting bool
	clipCh   chan agent.Clip
	tray     *tray

	logPath string
	dataDir string
}

// NewApp 构造绑定对象（尚未启动）。
func NewApp(version string, logger *log.Logger) *App {
	s, err := loadSettings()
	if err != nil && logger != nil {
		logger.Printf("warn: %v", err)
	}
	a := &App{
		version:  version,
		logger:   logger,
		holder:   &clientHolder{},
		settings: s,
		clipCh:   make(chan agent.Clip, 8),
	}
	if dir, err := session.Dir(); err == nil {
		a.dataDir = dir
		a.logPath = filepath.Join(dir, "agent.log")
	}
	// 先建重连状态机：Manager 的 OnState 回调里会读它（uiState→rc），顺序不能反。
	a.rc = agent.NewReconnector(agent.DefaultReconnectPolicy())
	a.saveSession = session.Save
	a.mgr = agent.NewManager(agent.Options{
		Uploader:    &uploader{holder: a.holder},
		Settings:    s.agentSettings(),
		HistorySize: s.App.HistorySize,
		Classify:    classify,
		Reauth:      a.reauth,
		Logger:      a.logf,
		OnRecords:   func(recs []agent.Record) { a.emit("records", recs) },
		OnState:     func(st agent.State) { a.emitState() },
	})
	a.mgr.SetAvailable(false)
	return a
}

func (a *App) logf(format string, args ...any) {
	if a.logger != nil {
		a.logger.Printf(format, args...)
	}
}

func (a *App) setLastErr(msg string) {
	a.mu.Lock()
	a.lastErr = msg
	a.mu.Unlock()
	a.emitState()
}

// startup 是 Wails 的 OnStartup 回调：恢复会话、启动监听与托盘。
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if a.settings.App.Autostart {
		_ = setAutostart(true)
	}
	a.restoreSession()
	a.syncWatcher()
	a.enableReconnect()
	a.emitState()
	a.emit("records", a.mgr.History())
	go a.pumpClipboard()
	go a.reconnectLoop(ctx)
	if a.settings.App.CloseToTray {
		// 启动时保持在托盘，用户从托盘点开窗口即可。
	}
}

// pumpClipboard 把监听线程读到的事件转给 Manager。
func (a *App) pumpClipboard() {
	for clip := range a.clipCh {
		a.mgr.Submit(clip)
	}
}

// restoreSession 用本地会话文件恢复登录状态；失效时尝试自动重登。
func (a *App) restoreSession() {
	sess, err := session.Load()
	if err != nil {
		a.logf("读取会话失败: %v", err)
		a.setLastErr(err.Error())
		return
	}
	if sess == nil {
		return
	}
	// 会话文件来自 lanbox-cli 时没有 pin_fp：首次使用时固定当前证书指纹（TOFU），
	// 否则自签证书会导致恢复会话失败。
	if sess.PinFP == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		fp, err := probeCert(ctx, sess.URL)
		cancel()
		if err == nil && fp != "" {
			sess.PinFP = fp
			if err := session.Save(sess); err == nil {
				a.logf("已首次固定服务器证书指纹: %s", fp)
			}
		} else {
			a.logf("warn: 无法获取服务器证书指纹: %v", err)
		}
	}
	hc := pinnedClient(sess.PinFP)
	cli, err := client.New(client.Options{BaseURL: sess.URL, SID: sess.SID, HTTP: hc})
	if err != nil {
		a.setLastErr(err.Error())
		return
	}
	a.mu.Lock()
	a.sess = sess
	a.mu.Unlock()
	a.holder.set(cli)
	// 这里不再自己探活/重登：交给重连守护统一处理（启动后 1 秒内第一次探活），
	// 避免两处逻辑各打一次 /api/me 或 /api/login。
}

// relogin 用保存的密码重新登录；返回原始错误，便于区分密码错 / 被限速 / 网络不通。
func (a *App) relogin(ctx context.Context) error {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	pass := s.password()
	if s.Server.URL == "" || s.Server.Username == "" || pass == "" {
		return errNoPassword
	}
	sess, hc, err := doLogin(ctx, s.Server.URL, s.Server.Username, pass, s.Server.PinFP)
	if err != nil {
		return err
	}
	cli, err := client.New(client.Options{BaseURL: sess.URL, SID: sess.SID, HTTP: hc})
	if err != nil {
		return err
	}
	save := a.saveSession
	if save == nil {
		save = session.Save
	}
	if err := save(sess); err != nil {
		a.logf("保存会话失败: %v", err)
	}
	a.mu.Lock()
	a.sess = sess
	a.mu.Unlock()
	a.holder.set(cli)
	return nil
}

// reauth 是 agent.Manager 的会话失效回调（上传时撞到 401）。
func (a *App) reauth(ctx context.Context) (bool, error) {
	err := a.relogin(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return false, nil
		}
		// 失败原因回灌状态机，交给后台守护按退避继续（不要把登录限速打爆）。
		a.noteReloginFailure(err)
		return false, nil
	}
	a.mgr.SetAvailable(true)
	a.syncWatcher()
	a.rc.ReloginOK(time.Now())
	return true, nil
}

// syncWatcher 按配置启停剪贴板监听。
func (a *App) syncWatcher() {
	a.mu.Lock()
	want := a.settings.Clipboard.Enabled && !a.settings.Clipboard.Paused && a.holder.get() != nil
	watching := a.watching
	if want && !watching {
		if a.watcher == nil {
			a.watcher = winclip.NewWatcher(a.logf)
		}
		a.watching = true
	}
	if !want && watching {
		a.watching = false
	}
	w := a.watcher
	a.mu.Unlock()

	if want && !watching {
		if err := w.Start(a.clipCh); err != nil {
			a.mu.Lock()
			a.watching = false
			a.mu.Unlock()
			a.setLastErr("剪贴板监听启动失败: " + err.Error())
			return
		}
		a.logf("剪贴板监听已启动")
	}
	if !want && watching && w != nil {
		w.Close()
		a.mu.Lock()
		a.watcher = nil
		a.mu.Unlock()
		a.logf("剪贴板监听已停止")
	}
}

// emit 向前端推送事件（未启动时静默丢弃）。
func (a *App) emit(event string, payload any) {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, event, payload)
}

// uiState 组装界面状态。
func (a *App) uiState() UIState {
	ms := a.mgr.State()
	stopped, reason := a.rc.Stopped()
	a.mu.Lock()
	defer a.mu.Unlock()
	st := UIState{
		Version:           a.version,
		Enabled:           a.settings.Clipboard.Enabled,
		Paused:            a.settings.Clipboard.Paused,
		Listening:         a.watching,
		Available:         a.holder.get() != nil && ms.Available,
		Connected:         a.rc.Connected(),
		Reconnecting:      a.rc.Reconnecting(),
		ReconnectAttempts: a.rc.Attempts(),
		ReconnectStopped:  stopped,
		ReconnectReason:   reason,
		HasPassword:       a.settings.Server.Password != "",
		LastErr:           a.lastErr,
		LastSkip:          ms.LastSkip,
		Active:            ms.Active,
		Queue:             ms.Queue,
		LogPath:           a.logPath,
		DataDir:           a.dataDir,
		Autostart:         a.settings.App.Autostart,
		CloseTray:         a.settings.App.CloseToTray,
	}
	if at := a.rc.NextRetryAt(); !at.IsZero() {
		st.NextRetryAt = at.UnixMilli()
	}
	if a.sess != nil {
		st.Server = a.sess.URL
		st.Username = a.sess.Username
		st.LoggedIn = a.holder.get() != nil
	}
	return st
}

func (a *App) emitState() {
	st := a.uiState()
	a.emit("state", st)
	if a.tray != nil {
		a.tray.update(st)
	}
}

// ---------- 绑定方法 ----------

// GetState 返回界面状态。
func (a *App) GetState() UIState { return a.uiState() }

// GetSettings 返回配置（密码字段置空，只暴露是否已保存）。
func (a *App) GetSettings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.settings
	s.Server.Password = ""
	return s
}

// SaveSettings 保存配置并立即生效。
func (a *App) SaveSettings(next Settings) error {
	next.Server.Password = ""
	a.mu.Lock()
	prev := a.settings
	a.mu.Unlock()

	// 保留已保存的密码密文，除非用户关掉了"记住密码"。
	if next.App.RememberPassword {
		next.Server.Password = prev.Server.Password
	}
	if next.Server.URL == "" {
		next.Server.URL = prev.Server.URL
	}
	if next.Server.Username == "" {
		next.Server.Username = prev.Server.Username
	}
	if next.Server.PinFP == "" {
		next.Server.PinFP = prev.Server.PinFP
	}
	next.clamp()
	if err := next.save(); err != nil {
		return err
	}
	if prev.App.Autostart != next.App.Autostart {
		if err := setAutostart(next.App.Autostart); err != nil {
			a.logf("设置开机自启失败: %v", err)
			next.App.Autostart = prev.App.Autostart
			_ = next.save()
			return err
		}
	}
	a.mu.Lock()
	a.settings = next
	a.mu.Unlock()
	a.mgr.SetSettings(next.agentSettings())
	a.syncWatcher()
	// 服务器/账号/密码变了就重置守护，按新配置立即重新判断能不能自动连上。
	if prev.Server.URL != next.Server.URL || prev.Server.Username != next.Server.Username ||
		prev.Server.PinFP != next.Server.PinFP || prev.Server.Password != next.Server.Password {
		a.enableReconnect()
	}
	a.emitState()
	return nil
}

// Discover 在局域网内查找 lanbox 实例（约 2.5 秒）。
func (a *App) Discover() ([]Found, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	svcs, err := mdns.Browse(ctx, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("mDNS 发现失败: %w", err)
	}
	out := make([]Found, 0, len(svcs))
	for _, s := range svcs {
		addrs := make([]string, 0, len(s.Addrs))
		for _, ip := range s.Addrs {
			addrs = append(addrs, ip.String())
		}
		out = append(out, Found{
			Instance: s.Instance,
			Host:     s.Host,
			URL:      pickURL(s),
			Addrs:    addrs,
			FP:       s.TXT["fp"],
			Version:  s.TXT["v"],
		})
	}
	a.mu.Lock()
	a.found = svcs
	a.mu.Unlock()
	return out, nil
}

// ProbeCert 读取服务器的证书指纹（首次手动信任用）。
func (a *App) ProbeCert(baseURL string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return probeCert(ctx, baseURL)
}

// Login 用地址 + 账号登录。
func (a *App) Login(req LoginRequest) (UIState, error) {
	if strings.TrimSpace(req.Username) == "" {
		return a.uiState(), errors.New("用户名不能为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, hc, err := doLogin(ctx, req.URL, req.Username, req.Password, req.PinFP)
	if err != nil {
		return a.uiState(), err
	}
	cli, err := client.New(client.Options{BaseURL: sess.URL, SID: sess.SID, HTTP: hc})
	if err != nil {
		return a.uiState(), err
	}
	if err := session.Save(sess); err != nil {
		return a.uiState(), fmt.Errorf("保存会话失败: %w", err)
	}
	a.mu.Lock()
	a.sess = sess
	a.holder.set(cli)
	a.settings.Server = ServerSettings{
		URL:      sess.URL,
		Username: sess.Username,
		PinFP:    req.PinFP,
	}
	a.settings.App.RememberPassword = req.Remember
	if req.Remember {
		if err := a.settings.setPassword(req.Password); err != nil {
			a.logf("warn: %v", err)
		}
	}
	saved := a.settings
	a.mu.Unlock()
	if err := saved.save(); err != nil {
		a.logf("warn: 保存配置失败: %v", err)
	}
	a.mgr.SetAvailable(true)
	a.setLastErr("")
	a.syncWatcher()
	a.rc.Enable(true)
	a.emitState()
	return a.uiState(), nil
}

// LoginFound 用 mDNS 发现结果登录（自动固定 TXT 里的证书指纹）。
func (a *App) LoginFound(instance, username, password string, remember bool) (UIState, error) {
	a.mu.Lock()
	var svc *mdns.Service
	for i := range a.found {
		if a.found[i].Instance == instance {
			svc = &a.found[i]
			break
		}
	}
	a.mu.Unlock()
	if svc == nil {
		return a.uiState(), errors.New("未找到该实例，请刷新发现列表")
	}
	return a.Login(LoginRequest{
		URL:      pickURL(*svc),
		Username: username,
		Password: password,
		PinFP:    svc.TXT["fp"],
		Remember: remember,
	})
}

// Logout 注销并停止监听。
func (a *App) Logout() error {
	if cli := a.holder.get(); cli != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = cli.Logout(ctx)
		cancel()
	}
	a.holder.set(nil)
	a.mu.Lock()
	a.sess = nil
	a.settings.Server.Password = ""
	saved := a.settings
	a.mu.Unlock()
	_ = session.Remove()
	_ = saved.save()
	a.rc.Disable()
	a.mgr.SetAvailable(false)
	a.syncWatcher()
	a.emitState()
	return nil
}

// Pause 暂停/恢复自动上传（暂停状态持久化）。
func (a *App) Pause(paused bool) UIState {
	a.mu.Lock()
	a.settings.Clipboard.Paused = paused
	saved := a.settings
	a.mu.Unlock()
	_ = saved.save()
	a.mgr.Pause(paused)
	a.syncWatcher()
	a.emitState()
	return a.uiState()
}

// GetHistory 返回本机上传历史。
func (a *App) GetHistory() []agent.Record { return a.mgr.History() }

// SendManual 上传一条手动内容（文本 + 文件/文件夹路径）。
func (a *App) SendManual(text string, paths []string) (agent.Record, error) {
	rec, err := a.mgr.SubmitManual(text, paths)
	if err != nil {
		return rec, err
	}
	return rec, nil
}

// UploadClipboardNow 立即上传当前剪贴板（忽略去重与开关，但仍守限额）。
func (a *App) UploadClipboardNow() error {
	clip, err := winclip.Read()
	if err != nil {
		return err
	}
	if clip.Ignore {
		return errors.New("当前剪贴板内容要求忽略（密码管理器等）")
	}
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	switch {
	case len(clip.Paths) > 0:
		_, err = a.mgr.SubmitManual(clip.Text, clip.Paths)
	case len(clip.PNG) > 0:
		if s.Clipboard.MaxImageMB > 0 && len(clip.PNG) > s.Clipboard.MaxImageMB<<20 {
			return fmt.Errorf("图片超过 %dMB 上限", s.Clipboard.MaxImageMB)
		}
		name := "clipboard-" + time.Now().Format("20060102-150405") + ".png"
		_, err = a.mgr.SubmitManualContent("", agent.KindImage, []agent.File{{Rel: name, Size: int64(len(clip.PNG)), Data: clip.PNG}}, 0)
	case strings.TrimSpace(clip.Text) != "":
		_, err = a.mgr.SubmitManual(clip.Text, nil)
	default:
		err = errors.New("剪贴板为空")
	}
	return err
}

// PickFiles 弹出文件选择器，返回选中路径（不做上传）。
func (a *App) PickFiles() ([]string, error) { return winclip.PickFiles("选择要发送的文件") }

// PickFolder 弹出文件夹选择器，返回路径（不做上传）。
func (a *App) PickFolder() (string, error) { return winclip.PickFolder("选择要发送的文件夹") }

// Retry 重试一条失败记录。
func (a *App) Retry(id string) error { return a.mgr.Retry(id) }

// Cancel 取消一条上传中的记录。
func (a *App) Cancel(id string) error { return a.mgr.Cancel(id) }

// Withdraw 撤回一条已上传记录（同时删除服务端内容）。
func (a *App) Withdraw(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return a.mgr.Withdraw(ctx, id)
}

// OpenLog 在资源管理器中定位日志文件。
func (a *App) OpenLog() error { return revealPath(a.logPath) }

// OpenDataDir 打开配置目录。
func (a *App) OpenDataDir() error { return revealPath(a.dataDir) }

// Version 返回客户端版本。
func (a *App) Version() string { return a.version }

// hideOnClose 处理窗口关闭：开启"最小化到托盘"时隐藏窗口并阻止退出。
func (a *App) hideOnClose() bool {
	a.mu.Lock()
	hide := a.settings.App.CloseToTray && !a.quitting
	a.mu.Unlock()
	if !hide {
		return false
	}
	if a.ctx != nil {
		wailsruntime.WindowHide(a.ctx)
	}
	return true
}

// shutdown 是 Wails 退出回调。
func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	a.quitting = true
	w := a.watcher
	a.watcher = nil
	a.watching = false
	a.mu.Unlock()
	if w != nil {
		w.Close()
	}
	a.mgr.Close()
}

// onFileDrop 处理拖进窗口的文件。
func (a *App) onFileDrop(x, y int, paths []string) {
	if len(paths) == 0 {
		return
	}
	if _, err := a.mgr.SubmitManual("", paths); err != nil {
		a.setLastErr(err.Error())
	}
}

// pickURL 在多个地址里挑一个连得上的，拼成 URL。
func pickURL(s mdns.Service) string {
	if s.Port <= 0 {
		return ""
	}
	var v4, v6 []string
	for _, ip := range s.Addrs {
		if ip.IsLinkLocalUnicast() {
			continue
		}
		if ip.To4() != nil {
			v4 = append(v4, ip.String())
		} else {
			v6 = append(v6, "["+ip.String()+"]")
		}
	}
	cands := append(v4, v6...)
	for _, h := range cands {
		raw := strings.Trim(h, "[]")
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(raw, strconv.Itoa(s.Port)), 300*time.Millisecond)
		if err == nil {
			conn.Close()
			return formatURL(h, s.Port)
		}
	}
	if len(cands) > 0 {
		return formatURL(cands[0], s.Port)
	}
	if s.Host != "" {
		return formatURL(s.Host, s.Port)
	}
	return ""
}

func formatURL(host string, port int) string {
	if port == 443 {
		return "https://" + host
	}
	return "https://" + host + ":" + strconv.Itoa(port)
}

// revealPath 用资源管理器定位文件/目录。
func revealPath(path string) error {
	if path == "" {
		return errors.New("路径未知")
	}
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		return exec.Command("explorer.exe", path).Start()
	}
	if _, err := os.Stat(path); err != nil {
		// 文件不存在时退化为打开所在目录。
		path = filepath.Dir(path)
	}
	return exec.Command("explorer.exe", "/select,"+path).Start()
}
