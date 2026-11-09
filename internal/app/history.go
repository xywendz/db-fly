package app

import (
	"strings"
	"time"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/platform/config"
)

const (
	maxHistoryEntries = 200
	maxHistoryText    = 4000
)

// recordHistory 保存可重放的完整语句。写入失败不影响查询本身的结果。
func (a *App) recordHistory(e config.QueryHistoryEntry) {
	if e.ID == "" {
		e.ID = NewID()
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if len(e.Text) > maxHistoryText {
		e.Text = e.Text[:maxHistoryText] + "…"
	}
	_ = a.mutateConfig(func(cfg *config.Config) error {
		config.AppendHistory(cfg, e, maxHistoryEntries)
		return nil
	})
}

// ListHistory 返回最近的查询/导入记录（最新在前）。limit<=0 表示全部。
func (a *App) ListHistory(limit int) ([]config.QueryHistoryEntry, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > len(cfg.QueryHistory) {
		limit = len(cfg.QueryHistory)
	}
	out := make([]config.QueryHistoryEntry, limit)
	copy(out, cfg.QueryHistory[:limit])
	return out, nil
}

// ClearHistory 清空全部查询历史。
func (a *App) ClearHistory() error {
	return a.mutateConfig(func(cfg *config.Config) error {
		cfg.QueryHistory = nil
		return nil
	})
}

// ListSavedQueries 返回全部收藏。
func (a *App) ListSavedQueries() ([]config.SavedQuery, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	out := make([]config.SavedQuery, len(cfg.SavedQueries))
	copy(out, cfg.SavedQueries)
	return out, nil
}

// SaveSavedQuery 新增或更新收藏，名称必须唯一且非空。
func (a *App) SaveSavedQuery(q config.SavedQuery) (config.SavedQuery, error) {
	q.Name = strings.TrimSpace(q.Name)
	if q.Name == "" {
		return q, domain.NewError(domain.CodeValidation, "saved_query", "收藏名称不能为空")
	}
	if strings.TrimSpace(q.Text) == "" {
		return q, domain.NewError(domain.CodeValidation, "saved_query", "收藏内容不能为空")
	}
	if q.Language == "" {
		q.Language = string(domain.LangSQL)
	}
	if q.ID == "" {
		q.ID = NewID()
	}
	q.UpdatedAt = time.Now()
	err := a.mutateConfig(func(cfg *config.Config) error {
		for _, existing := range cfg.SavedQueries {
			if existing.Name == q.Name && existing.ID != q.ID {
				return domain.NewError(domain.CodeValidation, "saved_query",
					"收藏名称 %q 已存在", q.Name)
			}
		}
		config.UpsertSavedQuery(cfg, q)
		return nil
	})
	if err != nil {
		return config.SavedQuery{}, err
	}
	return q, nil
}

// DeleteSavedQuery 按 ID 或名称删除收藏。
func (a *App) DeleteSavedQuery(nameOrID string) error {
	return a.mutateConfig(func(cfg *config.Config) error {
		if !config.RemoveSavedQuery(cfg, nameOrID) {
			return domain.NewError(domain.CodeNotFound, "saved_query",
				"收藏 %q 不存在", nameOrID)
		}
		return nil
	})
}

// SavePreferences 持久化 GUI/CLI 共享偏好。
func (a *App) SavePreferences(p config.Preferences) error {
	if p.MaxRows <= 0 {
		p.MaxRows = domain.DefaultMaxRows
	}
	return a.mutateConfig(func(cfg *config.Config) error {
		cfg.Preferences = p
		return nil
	})
}
