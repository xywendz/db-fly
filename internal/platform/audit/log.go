// Package audit 提供操作审计，记录语句摘要与耗时，默认不记录参数明文。
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Entry 是一条审计记录。
type Entry struct {
	Time       time.Time `json:"time"`
	Owner      string    `json:"owner,omitempty"` // 执行操作的账号（Web 多用户模式）
	Connection string    `json:"connection"`
	Kind       string    `json:"kind"`
	Action     string    `json:"action"`
	Statement  string    `json:"statement,omitempty"`
	DurationMs int64     `json:"duration_ms"`
	Rows       int64     `json:"rows,omitempty"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
}

// Logger 是审计端口。
type Logger interface {
	Log(e Entry)
	Path() string
}

// FileLogger 以 JSON Lines 追加写入审计日志。
type FileLogger struct {
	path string
	mu   sync.Mutex
}

// Nop 是空实现，用于测试。
type Nop struct{}

func (Nop) Log(Entry)    {}
func (Nop) Path() string { return "" }

// New 创建审计日志器。
func New(dir string) (*FileLogger, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建审计目录失败: %w", err)
	}
	return &FileLogger{path: filepath.Join(dir, "audit.log")}, nil
}

func (l *FileLogger) Path() string { return l.path }

func (l *FileLogger) Log(e Entry) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Statement = Sanitize(e.Statement)
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

var (
	stringLiteral = regexp.MustCompile(`(?s)'(?:[^']|'')*'`)
	hexLiteral    = regexp.MustCompile(`(?i)\b0x[0-9a-f]{16,}\b`)
	passwordHint  = regexp.MustCompile(`(?i)(password|passwd|secret|token)\s*(\S+)`)
)

// Sanitize 生成可安全落盘的语句摘要：截断长度并脱敏字面量。
func Sanitize(s string) string {
	const maxLen = 1000
	if len(s) > maxLen {
		s = s[:maxLen] + "…"
	}
	s = stringLiteral.ReplaceAllString(s, "'?'")
	s = hexLiteral.ReplaceAllString(s, "0x?")
	s = passwordHint.ReplaceAllString(s, "$1 ?")
	return strings.TrimSpace(s)
}
