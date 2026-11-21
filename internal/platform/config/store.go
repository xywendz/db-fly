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

	"github.com/xywendz/db-fly/internal/domain"
)

// Preferences 是跨 GUI / CLI 共享的用户偏好。
type Preferences struct {
	MaxRows          int    `json:"max_rows"`
	Theme            string `json:"theme"`
	Language         string `json:"language"`
	ConfirmDangerous bool   `json:"confirm_dangerous"`
}

// SavedQuery 是收藏的查询脚本。Language/Collection 让收藏可跨 SQL 与 MongoDB 复用。
// Folder 支持目录分组；Kind 标注数据库类型（mysql/redis 等），便于直观区分。
type SavedQuery struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Folder     string    `json:"folder,omitempty"`
	Kind       string    `json:"kind,omitempty"`
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
	// QueryHistory 仅用于兼容旧版 config.json；新数据一律写入 history.json。
	QueryHistory []QueryHistoryEntry `json:"query_history,omitempty"`
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

// HistoryStore 是查询历史的独立持久化端口。历史高频写入，单独存文件可避免
// 每次查询都重写连接、收藏与偏好所在的 config.json。
type HistoryStore interface {
	Load() ([]QueryHistoryEntry, error)
	Save(items []QueryHistoryEntry) error
	Path() string
}

// FileStore 使用 JSON 文件持久化配置。
type FileStore struct {
	path string
	mu   sync.Mutex
}

// DefaultDir 返回默认数据目录 ~/.db-fly。
// 兼容升级：若旧版 ~/.dbclient 存在而新目录尚不存在，则把旧目录整体迁移到
// ~/.db-fly，避免已有连接配置与凭证库丢失，同时保证所有入口使用同一目录。
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法获取用户目录: %w", err)
	}
	dir := filepath.Join(home, ".db-fly")
	if _, err := os.Stat(dir); err == nil {
		return dir, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("检查数据目录失败: %w", err)
	}

	legacy := filepath.Join(home, ".dbclient")
	if info, err := os.Stat(legacy); err == nil && info.IsDir() {
		if err := os.Rename(legacy, dir); err != nil {
			// 另一个进程可能刚完成迁移；重新检查目标目录即可。
			if _, statErr := os.Stat(dir); statErr == nil {
				return dir, nil
			}
			return "", fmt.Errorf("迁移旧数据目录 %s 到 %s 失败: %w", legacy, dir, err)
		}
	}
	return dir, nil
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
	s := &FileStore{path: filepath.Join(dir, "config.json")}
	if err := s.migrateHistoryToFile(); err != nil {
		return nil, fmt.Errorf("迁移查询历史失败: %w", err)
	}
	return s, nil
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

	// 查询历史已拆到 history.json；即使调用方持有旧结构也不再写回 config.json。
	clean := *cfg
	clean.QueryHistory = nil
	b, err := json.MarshalIndent(&clean, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(s.path, b, 0o600)
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// HistoryFileStore 用 history.json 独立持久化查询历史。
// 高频历史写入不再触碰连接、收藏与偏好所在的 config.json。
type HistoryFileStore struct {
	path string
	mu   sync.Mutex
}

// NewHistoryStore 创建查询历史存储；dir 为空时回退到默认数据目录。
func NewHistoryStore(dir string) *HistoryFileStore {
	if dir == "" {
		if resolved, err := DefaultDir(); err == nil {
			dir = resolved
		} else {
			dir = "."
		}
	}
	return &HistoryFileStore{path: filepath.Join(dir, "history.json")}
}

func (s *HistoryFileStore) Path() string { return s.path }

func (s *HistoryFileStore) Load() ([]QueryHistoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return []QueryHistoryEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取查询历史失败: %w", err)
	}
	var items []QueryHistoryEntry
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, fmt.Errorf("查询历史格式错误 (%s): %w", s.path, err)
	}
	if items == nil {
		items = []QueryHistoryEntry{}
	}
	return items, nil
}

func (s *HistoryFileStore) Save(items []QueryHistoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if items == nil {
		items = []QueryHistoryEntry{}
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("创建查询历史目录失败: %w", err)
	}
	b, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeFileAtomic(s.path, b)
}

// migrateHistoryToFile 把旧版 config.json 中的 query_history 一次性迁到 history.json，
// 并从 config.json 中清除该字段，避免后续配置保存再次带回历史数据。
func (s *FileStore) migrateHistoryToFile() error {
	cfg, err := s.Load()
	if err != nil {
		return err
	}
	if len(cfg.QueryHistory) == 0 {
		return nil
	}
	history := NewHistoryStore(filepath.Dir(s.path))
	existing, err := history.Load()
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		if err := history.Save(cfg.QueryHistory); err != nil {
			return err
		}
	}
	cfg.QueryHistory = nil
	return s.Save(cfg)
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

// UpsertSavedQuery 按 ID 或（目录+名称）新增/更新收藏，目录内名称唯一。
func UpsertSavedQuery(cfg *Config, q SavedQuery) {
	for i := range cfg.SavedQueries {
		if cfg.SavedQueries[i].ID == q.ID ||
			(cfg.SavedQueries[i].Folder == q.Folder && cfg.SavedQueries[i].Name == q.Name) {
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

// AppendHistory 兼容旧调用：把一条历史写入 Config 的迁移字段。
// 新代码应使用 AppendHistoryEntries 直接操作独立历史列表。
func AppendHistory(cfg *Config, e QueryHistoryEntry, limit int) {
	cfg.QueryHistory = AppendHistoryEntries(cfg.QueryHistory, e, limit)
}

// AppendHistoryEntries 把一条历史插到最前面，并按上限裁剪（最新在前）。
func AppendHistoryEntries(items []QueryHistoryEntry, e QueryHistoryEntry, limit int) []QueryHistoryEntry {
	if limit <= 0 {
		limit = 200
	}
	out := append([]QueryHistoryEntry{e}, items...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
