package bridge

import (
	"github.com/db-fly/db-fly/internal/platform/config"
	"github.com/db-fly/db-fly/internal/provider"
)

// ListHistory 返回最近的查询/导入历史（最新在前），limit<=0 表示全部。
func (b *Bridge) ListHistory(limit int) ([]config.QueryHistoryEntry, error) {
	return b.app.ListHistory(limit)
}

// ClearHistory 清空全部查询历史。
func (b *Bridge) ClearHistory() (bool, error) {
	if err := b.app.ClearHistory(); err != nil {
		return false, err
	}
	return true, nil
}

// ListSavedQueries 返回全部收藏。
func (b *Bridge) ListSavedQueries() ([]config.SavedQuery, error) {
	return b.app.ListSavedQueries()
}

// SaveSavedQuery 新增或更新收藏，名称必须唯一。
func (b *Bridge) SaveSavedQuery(q config.SavedQuery) (config.SavedQuery, error) {
	return b.app.SaveSavedQuery(q)
}

// DeleteSavedQuery 按 ID 或名称删除收藏。
func (b *Bridge) DeleteSavedQuery(nameOrID string) (bool, error) {
	if err := b.app.DeleteSavedQuery(nameOrID); err != nil {
		return false, err
	}
	return true, nil
}

// ListPreferences 返回当前偏好。
func (b *Bridge) ListPreferences() (config.Preferences, error) {
	return b.app.Preferences()
}

// SavePreferences 持久化偏好。
func (b *Bridge) SavePreferences(p config.Preferences) (bool, error) {
	if err := b.app.SavePreferences(p); err != nil {
		return false, err
	}
	return true, nil
}

// Info 返回运行时信息。
func (b *Bridge) Info() (AppInfo, error) {
	kinds := make([]string, 0)
	for _, k := range provider.Kinds() {
		kinds = append(kinds, string(k))
	}
	return AppInfo{
		Name:        "db-fly",
		Version:     b.version,
		DataDir:     b.dataDir,
		SecretsName: b.secretsName,
		Kinds:       kinds,
	}, nil
}
