// Package auth 提供 Web 模式的账号存储、会话管理与每用户业务内核注册表。
// 每个用户拥有独立的数据目录与独立 *app.App，业务用例层无需感知多租户。
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUserExists        = errors.New("用户名已被注册")
	ErrInvalidCredential = errors.New("用户名或密码错误")
	ErrInvalidUsername   = errors.New("用户名需为 3-32 位字母、数字、下划线或中划线")
	ErrWeakPassword      = errors.New("密码长度至少为 8 位")
	ErrSignupDisabled    = errors.New("注册已关闭")
	ErrInvalidInvite     = errors.New("注册邀请码不正确")
	ErrUserNotFound      = errors.New("用户不存在")
)

// User 是账号记录；密码只保存 bcrypt 哈希，绝不落明文。
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	CreatedAt    time.Time `json:"created_at"`
}

type userFile struct {
	Version int     `json:"version"`
	Users   []*User `json:"users"`
}

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)
var userIDRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Store 以 users.json（0600 + 原子替换）持久化账号表。
type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("创建账号目录失败: %w", err)
	}
	return &Store{path: filepath.Join(root, "users.json")}, nil
}

// load/save 由调用方持锁。
func (s *Store) load() (userFile, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return userFile{Version: 1, Users: []*User{}}, nil
	}
	if err != nil {
		return userFile{}, fmt.Errorf("读取账号表失败: %w", err)
	}
	f := userFile{Version: 1, Users: []*User{}}
	if err := json.Unmarshal(b, &f); err != nil {
		return userFile{}, fmt.Errorf("账号表格式错误: %w", err)
	}
	if f.Users == nil {
		f.Users = []*User{}
	}
	return f, nil
}

func (s *Store) save(f userFile) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Create 校验格式、检查重名并写入新账号。
func (s *Store) Create(username, password string) (*User, error) {
	if !usernameRE.MatchString(username) {
		return nil, ErrInvalidUsername
	}
	if len(password) < 8 {
		return nil, ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	for _, u := range f.Users {
		if strings.EqualFold(u.Username, username) {
			return nil, ErrUserExists
		}
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	u := &User{ID: id, Username: username, PasswordHash: string(hash), CreatedAt: time.Now().UTC()}
	f.Users = append(f.Users, u)
	if err := s.save(f); err != nil {
		return nil, err
	}
	return u, nil
}

// Authenticate 校验账号密码；任何失败都返回同一错误，避免账号枚举。
func (s *Store) Authenticate(username, password string) (*User, error) {
	s.mu.Lock()
	f, err := s.load()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	for _, u := range f.Users {
		if strings.EqualFold(u.Username, username) {
			if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
				return nil, ErrInvalidCredential
			}
			return u, nil
		}
	}
	return nil, ErrInvalidCredential
}

func (s *Store) ByID(id string) (*User, error) {
	s.mu.Lock()
	f, err := s.load()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	for _, u := range f.Users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, ErrUserNotFound
}

func (s *Store) Count() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	return len(f.Users), err
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
