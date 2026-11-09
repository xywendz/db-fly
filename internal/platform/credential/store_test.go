package credential

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("conn:abc", "p@ss'word;中文"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("conn:abc")
	if err != nil {
		t.Fatal(err)
	}
	if got != "p@ss'word;中文" {
		t.Fatalf("凭证往返不一致: %q", got)
	}
	keys, err := store.Keys()
	if err != nil || len(keys) != 1 || keys[0] != "conn:abc" {
		t.Fatalf("键列表不正确: %v, %v", keys, err)
	}
	if err := store.Delete("conn:abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("conn:abc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后应返回 ErrNotFound, 实际: %v", err)
	}
}

func TestStoreEmptyPassword(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("conn:empty", ""); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("conn:empty")
	if err != nil || got != "" {
		t.Fatalf("空密码往返失败: %q, %v", got, err)
	}
}

func TestStoreCiphertextIsNotPlaintext(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("k", "super-secret-value"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "super-secret-value") {
		t.Fatal("凭证文件包含明文密码")
	}
}

func TestStoreReopenKeepsSecret(t *testing.T) {
	dir := t.TempDir()
	first, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Put("k", "keep-me"); err != nil {
		t.Fatal(err)
	}
	second, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := second.Get("k"); err != nil || got != "keep-me" {
		t.Fatalf("重新打开后无法解密: %q, %v", got, err)
	}
}
