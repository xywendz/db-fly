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

// DefaultSessionTTL 是会话默认有效期：7 天。采用滑动过期——只要 7 天内有任意有效访问，
// 会话就顺延 7 天；连续 7 天无任何访问才需要重新登录。
const DefaultSessionTTL = 7 * 24 * time.Hour

func NewSessions(ttl time.Duration) *Sessions {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
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

// Touch 顺延会话过期时间到「当前时间 + ttl」，实现滑动过期。
// 仅当 token 有效（未过期、未删除）时才更新；无效 token 静默忽略。
func (s *Sessions) Touch(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked()
	e, ok := s.items[token]
	if !ok {
		return
	}
	e.expires = time.Now().Add(s.ttl)
	s.items[token] = e
}

func (s *Sessions) purgeLocked() {
	now := time.Now()
	for token, e := range s.items {
		if now.After(e.expires) {
			delete(s.items, token)
		}
	}
}
