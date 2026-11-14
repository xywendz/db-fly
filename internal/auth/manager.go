package auth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/db-fly/db-fly/internal/app"
	"github.com/db-fly/db-fly/internal/bootstrap"
	"github.com/db-fly/db-fly/internal/platform/audit"
	"github.com/db-fly/db-fly/internal/platform/config"
)

// Options 是多用户管理器的启动参数。
type Options struct {
	DataDir     string // 为空时使用 ~/.db-fly
	UploadDir   string // 为空时每用户目录下创建 uploads/
	AllowSignup bool
	SignupCode  string // 注册邀请码，为空表示不限制
	SessionTTL  time.Duration
	// Audit 是审计/数据库操作日志器；为空时各用户运行时按自身数据目录生成日志。
	// 若提供（通常由入口统一配置到「运行目录/logs」），则所有用户的操作日志集中写入同一处。
	Audit audit.Logger
}

// Runtime 是某个登录用户的完整运行时：独立业务内核 + 独立数据/上传目录。
type Runtime struct {
	User        *User
	App         *app.App
	DataDir     string
	SecretsName string
	UploadDir   string
}

// Manager 组合账号、会话与用户运行时注册表。
type Manager struct {
	root, usersDir, uploadOverride string
	allowSignup                    bool
	signupCode                     string
	store                          *Store
	sessions                       *Sessions
	audit                          audit.Logger // 可能为 nil（按用户各自目录记日志）
	mu                             sync.Mutex
	runtimes                       map[string]*Runtime
}

func New(opt Options) (*Manager, error) {
	root := opt.DataDir
	if root == "" {
		dir, err := config.DefaultDir()
		if err != nil {
			return nil, err
		}
		root = dir
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	usersDir := filepath.Join(abs, "users")
	if err := os.MkdirAll(usersDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建用户数据目录失败: %w", err)
	}
	store, err := NewStore(abs)
	if err != nil {
		return nil, err
	}
	return &Manager{
		root: abs, usersDir: usersDir, uploadOverride: opt.UploadDir,
		allowSignup: opt.AllowSignup, signupCode: opt.SignupCode,
		store: store, sessions: NewSessions(opt.SessionTTL), audit: opt.Audit,
		runtimes: make(map[string]*Runtime),
	}, nil
}

func (m *Manager) SignupEnabled() bool       { return m.allowSignup }
func (m *Manager) InviteRequired() bool      { return m.signupCode != "" }
func (m *Manager) UsersDir() string          { return m.usersDir }
func (m *Manager) SessionTTL() time.Duration { return m.sessions.ttl }

// CreateSession 为已通过校验的用户创建会话（注册成功后自动登录）。
func (m *Manager) CreateSession(u *User) (string, error) { return m.sessions.Create(u.ID) }

// Register 校验注册策略，创建账号并立即初始化该用户的数据目录。
func (m *Manager) Register(username, password, invite string) (*User, error) {
	if !m.allowSignup {
		return nil, ErrSignupDisabled
	}
	if m.signupCode != "" && subtle.ConstantTimeCompare([]byte(invite), []byte(m.signupCode)) != 1 {
		return nil, ErrInvalidInvite
	}
	u, err := m.store.Create(username, password)
	if err != nil {
		return nil, err
	}
	if _, err := m.RuntimeFor(u.ID); err != nil {
		return nil, err
	}
	return u, nil
}

// Login 校验密码并创建会话，返回会话 token。
func (m *Manager) Login(username, password string) (*User, string, error) {
	u, err := m.store.Authenticate(username, password)
	if err != nil {
		return nil, "", err
	}
	token, err := m.CreateSession(u)
	if err != nil {
		return nil, "", err
	}
	return u, token, nil
}

func (m *Manager) Logout(token string) { m.sessions.Delete(token) }

// Refresh 顺延会话过期时间（滑动过期），由每次有效访问时调用。
func (m *Manager) Refresh(token string) { m.sessions.Touch(token) }

// UserByToken 由会话 token 解析当前用户。
func (m *Manager) UserByToken(token string) (*User, error) {
	id, ok := m.sessions.UserID(token)
	if !ok {
		return nil, ErrInvalidCredential
	}
	return m.store.ByID(id)
}

// RuntimeFor 懒加载用户运行时；同一用户始终复用同一个 *app.App。
func (m *Manager) RuntimeFor(userID string) (*Runtime, error) {
	if !userIDRE.MatchString(userID) {
		return nil, errors.New("无效的用户 ID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if rt, ok := m.runtimes[userID]; ok {
		return rt, nil
	}
	u, err := m.store.ByID(userID)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(m.usersDir, userID)
	// 统一审计日志（入口配置的 logs/audit.log）优先；否则回退到各用户目录内日志。
	var logger audit.Logger
	if m.audit != nil {
		logger = m.audit
	} else if l, lerr := audit.New(dir); lerr == nil {
		logger = l
	} else {
		logger = audit.Nop{}
	}
	base, err := bootstrap.New(dir, logger)
	if err != nil {
		return nil, err
	}
	uploadDir := filepath.Join(dir, "uploads")
	if m.uploadOverride != "" {
		uploadDir = filepath.Join(m.uploadOverride, userID)
	}
	if err := os.MkdirAll(uploadDir, 0o700); err != nil {
		_ = base.Close()
		return nil, err
	}
	rt := &Runtime{
		User: u, App: base.App,
		DataDir: base.DataDir, SecretsName: base.SecretsName, UploadDir: uploadDir,
	}
	// 记录操作归属人，使审计日志可追溯到具体用户。
	if base.App != nil {
		base.App.Owner = u.Username
	}
	m.runtimes[userID] = rt
	return rt, nil
}

// Close 关闭全部用户会话并清理上传暂存文件。
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var first error
	for id, rt := range m.runtimes {
		if err := rt.App.Close(); err != nil && first == nil {
			first = err
		}
		if err := os.RemoveAll(rt.UploadDir); err != nil && first == nil {
			first = err
		}
		delete(m.runtimes, id)
	}
	return first
}
