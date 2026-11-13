package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/db-fly/db-fly/internal/auth"
	_ "github.com/db-fly/db-fly/internal/provider/all"
)

func newTestServer(t *testing.T, allowSignup bool, signupCode string) (*Server, http.Handler, *auth.Manager) {
	t.Helper()
	mgr, err := auth.New(auth.Options{
		DataDir: t.TempDir(), AllowSignup: allowSignup,
		SignupCode: signupCode, SessionTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	static := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>db-fly</html>")},
		"logo.png":   &fstest.MapFile{Data: []byte("PNG")},
	}
	s, err := New(mgr, Options{Version: "test", Static: static})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		_ = mgr.Close()
	})
	return s, s.Handler(), mgr
}

func jsonRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func register(t *testing.T, h http.Handler, username, password, invite string) *http.Cookie {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, "/api/auth/register", map[string]string{
		"username": username, "password": password, "invite": invite,
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("register %s = %d %s", username, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatal("register 未下发会话 cookie")
	return nil
}

func postRPC(t *testing.T, h http.Handler, cookie *http.Cookie, method, args string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/rpc/"+method,
		strings.NewReader(`{"args":`+args+`}`))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func rpcResult(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("RPC = %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Result
}

func TestPublicPathsAndLoginGuard(t *testing.T) {
	_, h, _ := newTestServer(t, true, "")

	health := httptest.NewRecorder()
	h.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"multi_user":true`) {
		t.Fatalf("health = %d %s", health.Code, health.Body.String())
	}
	if rec := postRPC(t, h, nil, "Info", `[]`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated RPC = %d", rec.Code)
	}
	index := httptest.NewRecorder()
	h.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	if index.Code != http.StatusFound || !strings.Contains(index.Header().Get("Location"), "/login") {
		t.Fatalf("unauthenticated page = %d %s", index.Code, index.Header().Get("Location"))
	}
	loginPage := httptest.NewRecorder()
	h.ServeHTTP(loginPage, httptest.NewRequest(http.MethodGet, "/login", nil))
	if loginPage.Code != http.StatusOK || !strings.Contains(loginPage.Body.String(), "注册新账号") {
		t.Fatalf("login page = %d %s", loginPage.Code, loginPage.Body.String())
	}
	logo := httptest.NewRecorder()
	h.ServeHTTP(logo, httptest.NewRequest(http.MethodGet, "/logo.png", nil))
	if logo.Code != http.StatusOK {
		t.Fatalf("public static asset = %d", logo.Code)
	}
}

func TestSignupPolicyAndSession(t *testing.T) {
	_, h, _ := newTestServer(t, true, "invite123")

	bad := []struct {
		name, user, pass, invite string
		want                     int
	}{
		{"wrong invite", "alice", "password123", "bad", 401},
		{"weak password", "alice", "short", "invite123", 400},
		{"bad username", "a b", "password123", "invite123", 400},
	}
	for _, tc := range bad {
		req := jsonRequest(t, http.MethodPost, "/api/auth/register", map[string]string{
			"username": tc.user, "password": tc.pass, "invite": tc.invite,
		})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("%s = %d %s (want %d)", tc.name, rec.Code, rec.Body.String(), tc.want)
		}
	}
	alice := register(t, h, "alice", "password123", "invite123")
	if rec := postRPC(t, h, alice, "Info", `[]`); rec.Code != http.StatusOK {
		t.Fatalf("authenticated RPC = %d %s", rec.Code, rec.Body.String())
	}
	me := httptest.NewRecorder()
	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(alice)
	h.ServeHTTP(me, meReq)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"username":"alice"`) {
		t.Fatalf("me = %d %s", me.Code, me.Body.String())
	}
	// 重名与错误密码。
	dup := jsonRequest(t, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "ALICE", "password": "password123", "invite": "invite123",
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, dup)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate username = %d", rec.Code)
	}
	wrong := jsonRequest(t, http.MethodPost, "/api/auth/login", map[string]string{
		"username": "alice", "password": "wrong-password",
	})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, wrong)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "用户名或密码错误") {
		t.Fatalf("wrong password = %d %s", rec.Code, rec.Body.String())
	}
	// 登出后会话立即失效。
	out := jsonRequest(t, http.MethodPost, "/api/auth/logout", map[string]string{})
	out.AddCookie(alice)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, out)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout = %d", rec.Code)
	}
	if rec := postRPC(t, h, alice, "Info", `[]`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session after logout = %d", rec.Code)
	}
}

func TestPerUserDataIsolation(t *testing.T) {
	s, h, mgr := newTestServer(t, true, "")
	alice := register(t, h, "alice", "password123", "")
	bob := register(t, h, "bob", "password123", "")

	connArgs := `[{"name":"alice-db","kind":"mysql","host":"127.0.0.1","port":3306},"",false]`
	if rec := postRPC(t, h, alice, "SaveConnection", connArgs); rec.Code != http.StatusOK {
		t.Fatalf("alice SaveConnection = %d %s", rec.Code, rec.Body.String())
	}
	favArgs := `[{"name":"alice-fav","text":"select 1"}]`
	if rec := postRPC(t, h, alice, "SaveSavedQuery", favArgs); rec.Code != http.StatusOK {
		t.Fatalf("alice SaveSavedQuery = %d %s", rec.Code, rec.Body.String())
	}

	aliceConns := postRPC(t, h, alice, "ListConnections", `[]`)
	bobConns := postRPC(t, h, bob, "ListConnections", `[]`)
	if !strings.Contains(aliceConns.Body.String(), "alice-db") {
		t.Fatalf("alice 看不到自己的连接: %s", aliceConns.Body.String())
	}
	if strings.Contains(bobConns.Body.String(), "alice-db") {
		t.Fatalf("bob 看到了 alice 的连接: %s", bobConns.Body.String())
	}
	if rec := postRPC(t, h, bob, "ListSavedQueries", `[]`); strings.Contains(rec.Body.String(), "alice-fav") {
		t.Fatalf("bob 看到了 alice 的收藏: %s", rec.Body.String())
	}

	// 磁盘层面：两个用户各有独立 config.json。
	me := httptest.NewRecorder()
	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(alice)
	h.ServeHTTP(me, meReq)
	var who struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &who); err != nil {
		t.Fatal(err)
	}
	aliceCfg, err := os.ReadFile(filepath.Join(mgr.UsersDir(), who.ID, "config.json"))
	if err != nil || !strings.Contains(string(aliceCfg), "alice-db") {
		t.Fatalf("alice config.json 异常: %v / %s", err, aliceCfg)
	}

	// 上传文件也按用户隔离：bob 不能引用 alice 上传的文件路径。
	upload := func(cookie *http.Cookie) uploadResponse {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		part, err := mw.CreateFormFile("file", "users.csv")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write([]byte("name,age\nalice,20\n"))
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("upload = %d %s", rec.Code, rec.Body.String())
		}
		var out uploadResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	aliceFile := upload(alice)
	bobFile := upload(bob)
	if filepath.Dir(aliceFile.Path) == filepath.Dir(bobFile.Path) {
		t.Fatal("两个用户共用上传目录")
	}
	importArgs := func(path string) string {
		b, _ := json.Marshal([]any{map[string]any{
			"conn": "missing", "ref": map[string]any{"database": "db", "name": "t"},
			"path": path, "format": "csv", "dry_run": true,
		}})
		return string(b)
	}
	blocked := postRPC(t, h, bob, "ImportFile", importArgs(aliceFile.Path))
	if blocked.Code != http.StatusBadRequest || !strings.Contains(blocked.Body.String(), "上传接口") {
		t.Fatalf("bob 可以引用 alice 的上传文件: %d %s", blocked.Code, blocked.Body.String())
	}
	accepted := postRPC(t, h, alice, "ImportFile", importArgs(aliceFile.Path))
	if accepted.Code != http.StatusBadRequest || strings.Contains(accepted.Body.String(), "上传接口") {
		t.Fatalf("alice 无法使用自己的上传文件: %d %s", accepted.Code, accepted.Body.String())
	}

	// 事件按用户分发。
	aliceCh := make(chan string, 1)
	bobCh := make(chan string, 1)
	s.events.add(aliceCh, "alice-id")
	s.events.add(bobCh, "bob-id")
	s.events.emitFor("alice-id", "query:started", map[string]any{"conn": "secret"})
	select {
	case <-aliceCh:
	default:
		t.Fatal("alice 未收到自己的事件")
	}
	select {
	case <-bobCh:
		t.Fatal("bob 收到了 alice 的事件")
	default:
	}
}

func TestRPCDispatchWithSession(t *testing.T) {
	_, h, _ := newTestServer(t, true, "")
	cookie := register(t, h, "alice", "password123", "")

	info := rpcResult(t, postRPC(t, h, cookie, "Info", `[]`))
	if info["version"] != "test" {
		t.Fatalf("version = %v", info["version"])
	}
	if rec := postRPC(t, h, cookie, "ListKinds", `[]`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "mysql") {
		t.Fatalf("ListKinds = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postRPC(t, h, cookie, "NotAMethod", `[]`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown method = %d", rec.Code)
	}
	if rec := postRPC(t, h, cookie, "ListDatabases", `[123]`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad argument = %d %s", rec.Code, rec.Body.String())
	}
}
