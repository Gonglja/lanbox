package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var (
	// ErrUserExists 表示用户名已存在。
	ErrUserExists = errors.New("用户名已存在")
	// ErrUserNotFound 表示用户名不存在。
	ErrUserNotFound = errors.New("用户不存在")
	// ErrSelfDelete 表示试图删除自己。
	ErrSelfDelete = errors.New("不能删除当前登录的账号")
	// ErrLastAdmin 表示试图删除最后一个管理员。
	ErrLastAdmin = errors.New("不能删除最后一个管理员")
	// ErrBadCredentials 表示用户名或密码错误。
	ErrBadCredentials = errors.New("用户名或密码错误")
)

// User 是一个账号。Salt/Hash/Iter 用于密码校验。
type User struct {
	Username  string    `json:"username"`
	Salt      string    `json:"salt"`
	Hash      string    `json:"hash"`
	Iter      int       `json:"iter"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

// Session 是一个内存会话。
type Session struct {
	Username string
	Expires  time.Time
}

// Manager 管理用户库（持久化到 path）与会话（仅内存）。
type Manager struct {
	path     string
	ttl      time.Duration
	mu       sync.RWMutex
	users    map[string]*User
	sessions map[string]*Session
	limiter  *Limiter
	now      func() time.Time
}

type userFile struct {
	Users []*User `json:"users"`
}

// Open 打开（或初始化）用户库。
func Open(path string, ttl time.Duration) (*Manager, error) {
	m := &Manager{
		path:     path,
		ttl:      ttl,
		users:    map[string]*User{},
		sessions: map[string]*Session{},
		limiter:  NewLimiter(15*time.Minute, 5, 10*time.Minute),
		now:      time.Now,
	}
	m.limiter.now = m.now
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("创建用户库目录失败: %w", err)
	}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		var f userFile
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("解析用户库 %s 失败: %w", path, err)
		}
		for _, u := range f.Users {
			if u != nil && u.Username != "" {
				m.users[u.Username] = u
			}
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, fmt.Errorf("读取用户库失败: %w", err)
	}
	return m, nil
}

// BootstrapAdmin 在用户库为空时创建 admin 并返回随机密码。
func (m *Manager) BootstrapAdmin() (password string, created bool, err error) {
	m.mu.RLock()
	empty := len(m.users) == 0
	m.mu.RUnlock()
	if !empty {
		return "", false, nil
	}
	pw := GeneratePassword()
	if err := m.CreateUser("admin", pw, true); err != nil {
		return "", false, err
	}
	return pw, true, nil
}

// Verify 校验用户名密码。
func (m *Manager) Verify(username, password string) (*User, bool) {
	m.mu.RLock()
	u, ok := m.users[username]
	var salt, hash string
	var iter int
	if ok {
		salt, hash, iter = u.Salt, u.Hash, u.Iter
	}
	m.mu.RUnlock()
	if !ok {
		// 用户不存在也做一次哈希，避免时序泄漏用户是否存在
		VerifyPassword(password, "AAAAAAAAAAAAAAAAAAAAAA==", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", PBKDF2Iter)
		return nil, false
	}
	if !VerifyPassword(password, salt, hash, iter) {
		return nil, false
	}
	cp := *u
	cp.Salt, cp.Hash, cp.Iter = "", "", 0
	return &cp, true
}

// Has 报告用户是否存在。
func (m *Manager) Has(username string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.users[username]
	return ok
}

// CreateUser 新建账号。
func (m *Manager) CreateUser(username, password string, isAdmin bool) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	if username == "" {
		return errors.New("用户名不能为空")
	}
	salt, hash, iter, err := HashPassword(password)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[username]; ok {
		return ErrUserExists
	}
	m.users[username] = &User{
		Username: username, Salt: salt, Hash: hash, Iter: iter,
		IsAdmin: isAdmin, CreatedAt: m.now().UTC(),
	}
	return m.saveLocked()
}

// SetPassword 重设密码，并踢掉该用户的所有会话。
func (m *Manager) SetPassword(username, password string) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	salt, hash, iter, err := HashPassword(password)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[username]
	if !ok {
		return ErrUserNotFound
	}
	u.Salt, u.Hash, u.Iter = salt, hash, iter
	for t, s := range m.sessions {
		if s.Username == username {
			delete(m.sessions, t)
		}
	}
	return m.saveLocked()
}

// DeleteUser 删除账号，actor 是当前操作者。
func (m *Manager) DeleteUser(actor, target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[target]; !ok {
		return ErrUserNotFound
	}
	if actor == target {
		return ErrSelfDelete
	}
	if m.users[target].IsAdmin && m.adminCountLocked() <= 1 {
		return ErrLastAdmin
	}
	delete(m.users, target)
	for t, s := range m.sessions {
		if s.Username == target {
			delete(m.sessions, t)
		}
	}
	return m.saveLocked()
}

func (m *Manager) adminCountLocked() int {
	n := 0
	for _, u := range m.users {
		if u.IsAdmin {
			n++
		}
	}
	return n
}

// List 返回按用户名排序的用户（不含哈希材料）。
func (m *Manager) List() []*User {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*User, 0, len(m.users))
	for _, u := range m.users {
		cp := *u
		cp.Salt, cp.Hash, cp.Iter = "", "", 0
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

// NewSession 为已校验的用户创建会话令牌。
func (m *Manager) NewSession(username string) (token string, expires time.Time, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[username]; !ok {
		return "", time.Time{}, ErrUserNotFound
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", time.Time{}, fmt.Errorf("生成会话令牌失败: %w", err)
	}
	token = hex.EncodeToString(raw[:])
	expires = m.now().Add(m.ttl)
	m.sessions[token] = &Session{Username: username, Expires: expires}
	return token, expires, nil
}

// Lookup 校验会话并做滑动续期。
func (m *Manager) Lookup(token string) (*User, bool) {
	if token == "" {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[token]
	if !ok {
		return nil, false
	}
	now := m.now()
	if now.After(s.Expires) {
		delete(m.sessions, token)
		return nil, false
	}
	u, ok := m.users[s.Username]
	if !ok {
		delete(m.sessions, token)
		return nil, false
	}
	s.Expires = now.Add(m.ttl)
	cp := *u
	cp.Salt, cp.Hash, cp.Iter = "", "", 0
	return &cp, true
}

// Drop 删除单个会话（登出）。
func (m *Manager) Drop(token string) {
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

// Limiter 返回登录失败限速器。
func (m *Manager) Limiter() *Limiter { return m.limiter }

// saveLocked 原子写用户库。调用方必须持有写锁。
func (m *Manager) saveLocked() error {
	f := userFile{Users: make([]*User, 0, len(m.users))}
	for _, u := range m.users {
		f.Users = append(f.Users, u)
	}
	sort.Slice(f.Users, func(i, j int) bool { return f.Users[i].Username < f.Users[j].Username })
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化用户库失败: %w", err)
	}
	tmp := m.path + ".tmp"
	fh, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("写入用户库失败: %w", err)
	}
	if _, err := fh.Write(raw); err != nil {
		fh.Close()
		os.Remove(tmp)
		return fmt.Errorf("写入用户库失败: %w", err)
	}
	if err := fh.Sync(); err != nil {
		fh.Close()
		os.Remove(tmp)
		return fmt.Errorf("同步用户库失败: %w", err)
	}
	if err := fh.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("关闭用户库失败: %w", err)
	}
	if err := os.Rename(tmp, m.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("替换用户库失败: %w", err)
	}
	return nil
}
