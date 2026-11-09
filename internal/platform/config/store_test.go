package config

import (
	"fmt"
	"testing"
	"time"
)

func TestSavedQueryAndHistoryPersistence(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	UpsertSavedQuery(cfg, SavedQuery{
		ID: "q1", Name: "daily", Text: "SELECT 1",
		Language: "sql", UpdatedAt: time.Now(),
	})
	for i := 0; i < 3; i++ {
		AppendHistory(cfg, QueryHistoryEntry{
			ID: fmt.Sprintf("h%d", i), Action: "query",
			Text: fmt.Sprintf("SELECT %d", i), At: time.Now(),
		}, 2)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}

	reloaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.SavedQueries) != 1 || reloaded.SavedQueries[0].Text != "SELECT 1" {
		t.Fatalf("收藏未持久化: %+v", reloaded.SavedQueries)
	}
	if len(reloaded.QueryHistory) != 2 || reloaded.QueryHistory[0].ID != "h2" {
		t.Fatalf("历史上限/顺序错误: %+v", reloaded.QueryHistory)
	}
	if !RemoveSavedQuery(reloaded, "daily") {
		t.Fatal("按名称删除收藏失败")
	}
	if RemoveSavedQuery(reloaded, "q1") {
		t.Fatal("重复删除应返回 false")
	}
}

func TestUpsertSavedQueryByName(t *testing.T) {
	cfg := Default()
	UpsertSavedQuery(cfg, SavedQuery{ID: "q1", Name: "same", Text: "SELECT 1"})
	UpsertSavedQuery(cfg, SavedQuery{ID: "q1", Name: "same", Text: "SELECT 2"})
	if len(cfg.SavedQueries) != 1 || cfg.SavedQueries[0].Text != "SELECT 2" {
		t.Fatalf("按 ID 更新失败: %+v", cfg.SavedQueries)
	}
	UpsertSavedQuery(cfg, SavedQuery{ID: "q2", Name: "same", Text: "SELECT 3"})
	if len(cfg.SavedQueries) != 1 || cfg.SavedQueries[0].ID != "q2" {
		t.Fatalf("按名称更新失败: %+v", cfg.SavedQueries)
	}
}

func TestAppendHistoryKeepsNewestFirst(t *testing.T) {
	cfg := Default()
	for i := 0; i < 5; i++ {
		AppendHistory(cfg, QueryHistoryEntry{ID: fmt.Sprintf("h%d", i)}, 3)
	}
	if len(cfg.QueryHistory) != 3 || cfg.QueryHistory[0].ID != "h4" || cfg.QueryHistory[2].ID != "h2" {
		t.Fatalf("历史裁剪错误: %+v", cfg.QueryHistory)
	}
}
