package config

import (
	"fmt"
	"os"
	"path/filepath"
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

// TestDefaultDirLegacyFallback 保证改名后已有 ~/.dbclient 配置不会失效。
func TestDefaultDirLegacyFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home) // Windows
	t.Setenv("HOME", home)        // Linux / macOS

	dir, err := DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".db-fly"); dir != want {
		t.Fatalf("应优先使用新目录: got %q want %q", dir, want)
	}

	legacy := filepath.Join(home, ".dbclient")
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err = DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != legacy {
		t.Fatalf("新目录不存在时应回退旧目录: got %q want %q", dir, legacy)
	}

	if err := os.Mkdir(filepath.Join(home, ".db-fly"), 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err = DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".db-fly"); dir != want {
		t.Fatalf("新目录存在后应切换回来: got %q want %q", dir, want)
	}
}
