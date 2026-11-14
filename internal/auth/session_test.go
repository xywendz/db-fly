package auth

import (
	"testing"
	"time"
)

func TestSessionsDefaultTTLIsSevenDays(t *testing.T) {
	s := NewSessions(0)
	if s.ttl != DefaultSessionTTL {
		t.Fatalf("默认会话有效期应为 7 天，实际: %v", s.ttl)
	}
}

func TestSessionsTouchExtendsExpiry(t *testing.T) {
	s := NewSessions(time.Hour)
	token, err := s.Create("u1")
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	if id, ok := s.UserID(token); !ok || id != "u1" {
		t.Fatalf("会话校验失败: %s %v", id, ok)
	}
	// Touch 之后会话应仍然有效（滑动过期，不立即失效）。
	s.Touch(token)
	if _, ok := s.UserID(token); !ok {
		t.Fatal("Touch 后会话应仍然有效")
	}
	// 删除后失效。
	s.Delete(token)
	if _, ok := s.UserID(token); ok {
		t.Fatal("删除后会话应失效")
	}
}

func TestSessionsExpiresAfterTTL(t *testing.T) {
	s := NewSessions(time.Hour)
	token, _ := s.Create("u1")
	// 直接篡改过期时间为过去，purge 应在下次访问时清理。
	s.mu.Lock()
	if e, ok := s.items[token]; ok {
		e.expires = time.Now().Add(-time.Minute)
		s.items[token] = e
	}
	s.mu.Unlock()
	if _, ok := s.UserID(token); ok {
		t.Fatal("过期会话应被清理")
	}
}
