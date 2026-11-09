// Package config 负责连接配置、偏好设置与查询脚本的本地持久化。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dbclient/dbclient/internal/domain"
)

// Preferences 是跨 GUI / CLI 共享的用户偏好。
type Preferences struct {
	MaxRows          int    `json:"max_rows"`
	Theme            string `json:"theme"`
	Language         string `json:"language"`
	ConfirmDangerous bool   `json:"confirm_dangerous"`
}

// SavedQuery 是收藏的查询脚本。Language/Collection 让收藏可跨 SQL 与 MongoDB 复用。
type SavedQuery struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Connection string    `json:"connection,omitempty"`
	Database   string    `json:"database,omitempty"`
	Collection string    `json:"collection,omitempty"`
	Language   string    `json:"language,omitempty"`
	Text       string    `json:"text"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// QueryHistoryEntry 是一条本地查询历史（与脱敏的审计日志互补，保存完整语句便于重放）。
type QueryHistoryEntry struct {
	ID         string    `json:"id"`
	At         time.Time `json:"at"`
	Connection string    `json:"connection,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Action     string    `json:"action,omitempty"`
	Database   string    `json:"database,omitempty"`
	Collection string    `json:"collection,omitempty"`
	Language   string    `json:"language,omitempty"`
	Text       string    `json:"text,omitempty"`
	Rows       int64     `json:"rows,omitempty"`
	DurationMs int64     `json:"duration_ms,omitempty"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
}

// Config 是配置文件的根结构。凭证不在此文件中。
type Config struct {
	Version      int                       `json:"version"`
	Connections  []domain.ConnectionConfig `json:"connections"`
	Preferences  Preferences               `json:"preferences"`
	SavedQueries []SavedQuery              `json:"saved_queries,omitempty"`
	QueryHistory []QueryHistoryEntry       `json:"query_history,omitempty"`
}

// Default 返回带默认偏好的空白配置。
func Default() *Config {
	return &Config{
		Version: 1,
		Preferences: Preferences{
			MaxRows:          domain.DefaultMaxRows,
			Theme:            "system",
			Language:         "zh-CN",
			ConfirmDangerous: true,
		},
	}
}

// Store 是配置持久化端口，便于后续替换为 SQLite 实现。
type Store interface {
	Load() (*Config, error)
	Save(cfg *Config) error
	Path() string
}

// FileStore 使用 JSON 文件持久化配置。
type FileStore struct {
	path string
	mu   sync.Mutex
}

// DefaultDir 返回默认数据目录 ~/.dbclient。
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法获取用户目录: %w", err)
	}
	return filepath.Join(home, ".dbclient"), nil
}

// New 创建配置存储；dir 为空时使用默认目录。
func New(dir string) (*FileStore, error) {
	if dir == "" {
		var err error
		if dir, err = DefaultDir(); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建配置目录失败: %w", err)
	}
	return &FileStore{path: filepath.Join(dir, "config.json")}, nil
}

func (s *FileStore) Path() string { return s.path }

func (s *FileStore) Load() (*Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	cfg := Default()
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("配置格式错误 (%s): %w", s.path, err)
	}
	if cfg.Preferences.MaxRows <= 0 {
		cfg.Preferences.MaxRows = domain.DefaultMaxRows
	}
	return cfg, nil
}

func (s *FileStore) Save(cfg *Config) error {
	if cfg == nil {
		return errors.New("配置为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(s.path, b, 0o600)
}

// FindConnection 按名称或 ID 查找连接配置。
func FindConnection(cfg *Config, nameOrID string) (domain.ConnectionConfig, bool) {
	for _, c := range cfg.Connections {
		if c.Name == nameOrID || c.ID == nameOrID {
			return c, true
		}
	}
	return domain.ConnectionConfig{}, false
}

// UpsertConnection 新增或更新连接配置。
func UpsertConnection(cfg *Config, c domain.ConnectionConfig) {
	for i := range cfg.Connections {
		if cfg.Connections[i].ID == c.ID {
			cfg.Connections[i] = c
			return
		}
	}
	cfg.Connections = append(cfg.Connections, c)
}

// RemoveConnection 删除指定连接配置。
func RemoveConnection(cfg *Config, id string) bool {
	for i := range cfg.Connections {
		if cfg.Connections[i].ID == id {
			cfg.Connections = append(cfg.Connections[:i], cfg.Connections[i+1:]...)
			return true
		}
	}
	return false
}

// UpsertSavedQuery 按 ID 或名称新增/更新收藏，名称唯一。
func UpsertSavedQuery(cfg *Config, q SavedQuery) {
	for i := range cfg.SavedQueries {
		if cfg.SavedQueries[i].ID == q.ID || cfg.SavedQueries[i].Name == q.Name {
			cfg.SavedQueries[i] = q
			return
		}
	}
	cfg.SavedQueries = append(cfg.SavedQueries, q)
}

// RemoveSavedQuery 按 ID 或名称删除收藏。
func RemoveSavedQuery(cfg *Config, nameOrID string) bool {
	for i := range cfg.SavedQueries {
		if cfg.SavedQueries[i].ID == nameOrID || cfg.SavedQueries[i].Name == nameOrID {
			cfg.SavedQueries = append(cfg.SavedQueries[:i], cfg.SavedQueries[i+1:]...)
			return true
		}
	}
	return false
}

// AppendHistory 把一条历史插到最前面，并按上限裁剪（最新在前）。
func AppendHistory(cfg *Config, e QueryHistoryEntry, limit int) {
	if limit <= 0 {
		limit = 200
	}
	cfg.QueryHistory = append([]QueryHistoryEntry{e}, cfg.QueryHistory...)
	if len(cfg.QueryHistory) > limit {
		cfg.QueryHistory = cfg.QueryHistory[:limit]
	}
}
