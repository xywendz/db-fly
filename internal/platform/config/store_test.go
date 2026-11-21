package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSavedQueryAndHistoryPersistence(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
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
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}

	history := NewHistoryStore(dir)
	var items []QueryHistoryEntry
	for i := 0; i < 3; i++ {
		items = AppendHistoryEntries(items, QueryHistoryEntry{
			ID: fmt.Sprintf("h%d", i), Action: "query",
			Text: fmt.Sprintf("SELECT %d", i), At: time.Now(),
		}, 2)
	}
	if err := history.Save(items); err != nil {
		t.Fatal(err)
	}

	reloaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.SavedQueries) != 1 || reloaded.SavedQueries[0].Text != "SELECT 1" {
		t.Fatalf("收藏未持久化: %+v", reloaded.SavedQueries)
	}
	if !RemoveSavedQuery(reloaded, "daily") {
		t.Fatal("按名称删除收藏失败")
	}
	if RemoveSavedQuery(reloaded, "q1") {
		t.Fatal("重复删除应返回 false")
	}

	historyItems, err := history.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(historyItems) != 2 || historyItems[0].ID != "h2" {
		t.Fatalf("历史上限/顺序错误: %+v", historyItems)
	}
	b, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "query_history") {
		t.Fatal("config.json 不应再写入查询历史")
	}
}

func TestHistoryMigratesFromLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	legacy := Default()
	legacy.QueryHistory = []QueryHistoryEntry{{ID: "old1", Text: "SELECT 1", At: time.Now()}}
	b, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.QueryHistory) != 0 {
		t.Fatalf("迁移后 config.json 不应保留历史: %+v", cfg.QueryHistory)
	}
	items, err := NewHistoryStore(dir).Load()
	if err != nil || len(items) != 1 || items[0].ID != "old1" {
		t.Fatalf("历史迁移失败: %+v / %v", items, err)
	}

	// 再次初始化不应重复迁移，也不能覆盖独立文件中的历史。
	if _, err := New(dir); err != nil {
		t.Fatal(err)
	}
	items, err = NewHistoryStore(dir).Load()
	if err != nil || len(items) != 1 || items[0].ID != "old1" {
		t.Fatalf("重复迁移应幂等: %+v / %v", items, err)
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

// TestDefaultDirMigratesLegacy 保证旧 ~/.dbclient 会被迁移到 ~/.db-fly。
func TestDefaultDirMigratesLegacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home) // Windows
	t.Setenv("HOME", home)        // Linux / macOS
	want := filepath.Join(home, ".db-fly")

	dir, err := DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != want {
		t.Fatalf("默认目录错误: got %q want %q", dir, want)
	}

	legacy := filepath.Join(home, ".dbclient")
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir, err = DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != want {
		t.Fatalf("迁移后目录错误: got %q want %q", dir, want)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("旧目录应已迁移: stat legacy = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("迁移后文件丢失: %v", err)
	}

	dir, err = DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != want {
		t.Fatalf("新目录存在时应保持不变: got %q want %q", dir, want)
	}
}
