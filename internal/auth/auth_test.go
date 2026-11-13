package auth

import (
	"errors"
	"testing"
	"time"
)

func TestStoreCreateAndAuthenticate(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Create("alice", "password123")
	if err != nil || len(u.ID) != 32 {
		t.Fatalf("Create = %v / %+v", err, u)
	}
	if _, err := s.Create("ALICE", "password123"); !errors.Is(err, ErrUserExists) {
		t.Fatalf("duplicate username = %v", err)
	}
	if _, err := s.Create("bob", "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak password = %v", err)
	}
	if _, err := s.Create("bad name", "password123"); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("bad username = %v", err)
	}
	authed, err := s.Authenticate("alice", "password123")
	if err != nil || authed.ID != u.ID {
		t.Fatalf("Authenticate = %v / %+v", err, authed)
	}
	if _, err := s.Authenticate("alice", "wrong"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("wrong password = %v", err)
	}
	if _, err := s.Authenticate("nobody", "password123"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("unknown user = %v", err)
	}
	byID, err := s.ByID(u.ID)
	if err != nil || byID.Username != "alice" {
		t.Fatalf("ByID = %v / %+v", err, byID)
	}
	if n, err := s.Count(); err != nil || n != 1 {
		t.Fatalf("Count = %d / %v", n, err)
	}
}

func TestSessionsExpireAndDelete(t *testing.T) {
	s := NewSessions(20 * time.Millisecond)
	token, err := s.Create("user-1")
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := s.UserID(token); !ok || id != "user-1" {
		t.Fatalf("UserID = %s / %v", id, ok)
	}
	time.Sleep(40 * time.Millisecond)
	if _, ok := s.UserID(token); ok {
		t.Fatal("过期会话仍然有效")
	}
	token2, _ := s.Create("user-2")
	s.Delete(token2)
	if _, ok := s.UserID(token2); ok {
		t.Fatal("登出后会话仍然有效")
	}
}

func TestManagerPolicyAndRuntimeIsolation(t *testing.T) {
	closed, err := New(Options{DataDir: t.TempDir(), AllowSignup: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := closed.Register("alice", "password123", ""); !errors.Is(err, ErrSignupDisabled) {
		t.Fatalf("closed signup = %v", err)
	}
	_ = closed.Close()

	m, err := New(Options{DataDir: t.TempDir(), AllowSignup: true, SignupCode: "code123"})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Register("alice", "password123", "bad"); !errors.Is(err, ErrInvalidInvite) {
		t.Fatalf("wrong invite = %v", err)
	}
	a, err := m.Register("alice", "password123", "code123")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Register("bob", "password123", "code123")
	if err != nil {
		t.Fatal(err)
	}
	ra, err := m.RuntimeFor(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := m.RuntimeFor(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ra.App == rb.App || ra.DataDir == rb.DataDir || ra.UploadDir == rb.UploadDir {
		t.Fatal("两个用户共享了业务内核或数据目录")
	}
	ra2, err := m.RuntimeFor(a.ID)
	if err != nil || ra2 != ra {
		t.Fatal("同一用户未复用业务内核")
	}
	if _, token, err := m.Login("bob", "password123"); err != nil || token == "" {
		t.Fatalf("Login = %v", err)
	}
}
