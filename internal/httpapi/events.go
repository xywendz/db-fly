package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/db-fly/db-fly/internal/bridge"
)

// eventHub 把 GUI 模式的 wails 事件总线等价映射为 SSE 广播。
// 多用户模式下每个连接带 userID，事件只发给所属用户，避免连接名跨账号泄露。
type eventHub struct {
	mu      sync.Mutex
	clients map[chan string]string // chan -> userID
}

func newEventHub() *eventHub {
	return &eventHub{clients: make(map[chan string]string)}
}

func (h *eventHub) add(ch chan string, userID string) {
	h.mu.Lock()
	h.clients[ch] = userID
	h.mu.Unlock()
}

func (h *eventHub) remove(ch chan string) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

func (h *eventHub) emitFor(userID, name string, data ...any) {
	payload, err := json.Marshal(map[string]any{"name": name, "data": data})
	if err != nil {
		return
	}
	msg := "data: " + string(payload) + "\n\n"
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, owner := range h.clients {
		if owner != userID {
			continue
		}
		select {
		case ch <- msg:
		default: // 慢客户端不阻塞业务线程
		}
	}
}

// userEmitter 把某个用户的 Bridge 事件绑定到该用户的 SSE 连接集合。
type userEmitter struct {
	hub    *eventHub
	userID string
}

var _ bridge.EventEmitter = userEmitter{}

func (e userEmitter) Emit(name string, data ...any) {
	e.hub.emitFor(e.userID, name, data...)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	rt, ok := runtimeFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "当前响应不支持 SSE")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ch := make(chan string, 16)
	s.events.add(ch, rt.User.ID)
	defer s.events.remove(ch)

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			_, _ = fmt.Fprint(w, msg)
			flusher.Flush()
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
