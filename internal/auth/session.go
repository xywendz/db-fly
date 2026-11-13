package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type sessionEntry struct {
	userID  string
	expires time.Time
}

// Sessions 是进程内会话表：token 只存在内存，服务重启后需重新登录。
type Sessions struct {
	mu    sync.Mutex
	ttl   time.Duration
	items map[string]sessionEntry
}

func NewSessions(ttl time.Duration) *Sessions {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &Sessions{ttl: ttl, items: make(map[string]sessionEntry)}
}

func (s *Sessions) Create(userID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked()
	s.items[token] = sessionEntry{userID: userID, expires: time.Now().Add(s.ttl)}
	return token, nil
}

func (s *Sessions) UserID(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked()
	e, ok := s.items[token]
	if !ok {
		return "", false
	}
	return e.userID, true
}

func (s *Sessions) Delete(token string) {
	s.mu.Lock()
	delete(s.items, token)
	s.mu.Unlock()
}

func (s *Sessions) purgeLocked() {
	now := time.Now()
	for token, e := range s.items {
		if now.After(e.expires) {
			delete(s.items, token)
		}
	}
}
