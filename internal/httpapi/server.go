// Package httpapi 为 db-fly 提供 B/S 模式的 HTTP 传输层。
//
// 它是 GUI 模式 Wails IPC 的等价替身：把 frontend 的 43 个门面调用映射到
// internal/bridge 的同一组 JSON DTO 方法；多用户模式下每次请求使用
// internal/auth 提供的独立业务内核，业务用例层完全不感知传输与租户。
package httpapi

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"

	"github.com/db-fly/db-fly/internal/auth"
	"github.com/db-fly/db-fly/internal/bridge"
)

const (
	// MaxRPCBody 限制单次 RPC 参数体（大 SQL 文本、批量编辑请求等）。
	MaxRPCBody int64 = 8 << 20
	// MaxUploadSize 限制单次导入文件上传体积。
	MaxUploadSize int64 = 256 << 20
)

// Options 是 B/S 服务的运行参数；Static 通常为 internal/webui.FS()。
type Options struct {
	Version string
	Static  fs.FS
}

// Server 聚合 HTTP 路由与进程级资源（SSE 客户端集合）。
type Server struct {
	auth    *auth.Manager
	version string
	static  fs.FS
	events  *eventHub
	logf    func(string, ...any)
}

// New 创建 B/S 服务。用户业务内核由 auth.Manager 按登录用户懒加载。
func New(mgr *auth.Manager, opt Options) (*Server, error) {
	if mgr == nil {
		return nil, errors.New("httpapi: 多用户管理器未初始化")
	}
	if opt.Static == nil {
		return nil, errors.New("httpapi: 未配置前端静态资源")
	}
	// 多用户模式不使用包级默认事件发射器，改由每次请求按用户注入。
	bridge.SetDefaultEmitter(nil)
	return &Server{
		auth: mgr, version: opt.Version, static: opt.Static,
		events: newEventHub(), logf: log.Printf,
	}, nil
}

// Handler 返回完整的 HTTP 处理器：用户会话 → 异常恢复 → 路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/rpc/{method}", s.handleRPC)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("POST /api/upload", s.handleUpload)
	mux.HandleFunc("POST /api/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/auth/login", s.handleLoginAPI)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.handleMe)
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLoginForm)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.Handle("/", s.staticHandler())
	return s.withUser(s.withRecovery(mux))
}

// Close 保留给入口统一调用；用户业务内核与上传目录由 auth.Manager 回收。
func (s *Server) Close() error { return nil }

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "mode": "web", "version": s.version, "multi_user": true,
	})
}

func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logf("httpapi: 处理 %s %s 时发生 panic: %v", r.Method, r.URL.Path, rec)
				writeError(w, http.StatusInternalServerError, "internal", "服务端内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Code: code, Message: message})
}
