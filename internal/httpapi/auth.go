package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/db-fly/db-fly/internal/auth"
)

// Web 模式的用户会话：随机 token 只存服务端内存，cookie 为 HttpOnly。
const sessionCookie = "db_fly_session"

type authUserDTO struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	CreatedAt string `json:"created_at"`
}

func userDTO(u *auth.User) authUserDTO {
	return authUserDTO{ID: u.ID, Username: u.Username, CreatedAt: u.CreatedAt.Format(time.RFC3339)}
}

// publicPath 列出无需登录的路径：健康检查、鉴权端点、静态资源。
func publicPath(p string) bool {
	switch p {
	case "/api/health", "/login", "/logout",
		"/api/auth/register", "/api/auth/login", "/api/auth/logout", "/api/auth/me",
		"/logo.png", "/favicon.ico", "/index.html":
		return true
	}
	return strings.HasPrefix(p, "/assets/")
}

func (s *Server) withUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		rt := s.currentRuntime(r)
		if rt == nil {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
				return
			}
			target := "/login?next=" + url.QueryEscape(safeNext(r.URL.RequestURI()))
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		next.ServeHTTP(w, r.WithContext(withRuntime(r.Context(), rt)))
	})
}

func (s *Server) currentRuntime(r *http.Request) *auth.Runtime {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	u, err := s.auth.UserByToken(c.Value)
	if err != nil {
		return nil
	}
	rt, err := s.auth.RuntimeFor(u.ID)
	if err != nil {
		s.logf("httpapi: 加载用户 %s 的运行时失败: %v", u.Username, err)
		return nil
	}
	return rt
}

// safeNext 防止开放重定向：只接受站内绝对路径。
func safeNext(v string) string {
	if v == "" || !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || strings.ContainsAny(v, "\\\r\n") {
		return "/"
	}
	return v
}

func isJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Content-Type"), "application/json")
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
		MaxAge: int(ttl.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
}

type credForm struct {
	Username, Password, Confirm, Invite, Next string
}

// readCreds 同时兼容 JSON（前端 API）与表单（登录页）。
func readCreds(r *http.Request) (credForm, error) {
	var c credForm
	if isJSON(r) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Confirm  string `json:"confirm"`
			Invite   string `json:"invite"`
			Next     string `json:"next"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			return c, errors.New("请求参数格式错误")
		}
		c = credForm{
			Username: body.Username, Password: body.Password, Confirm: body.Confirm,
			Invite: body.Invite, Next: body.Next,
		}
	} else {
		if err := r.ParseForm(); err != nil {
			return c, errors.New("请求格式错误")
		}
		c = credForm{
			Username: r.FormValue("username"), Password: r.FormValue("password"),
			Confirm: r.FormValue("confirm"), Invite: r.FormValue("invite"),
			Next: r.FormValue("next"),
		}
	}
	c.Username = strings.TrimSpace(c.Username)
	c.Next = safeNext(c.Next)
	return c, nil
}

func statusFor(err error) int {
	if errors.Is(err, auth.ErrInvalidCredential) || errors.Is(err, auth.ErrInvalidInvite) {
		return http.StatusUnauthorized
	}
	return http.StatusBadRequest
}

func (s *Server) authFail(w http.ResponseWriter, r *http.Request, err error, c credForm) {
	status := statusFor(err)
	if isJSON(r) {
		code := "validation"
		if status == http.StatusUnauthorized {
			code = "unauthorized"
		}
		writeError(w, status, code, err.Error())
		return
	}
	s.renderAuthPage(w, status, err.Error(), c.Next)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	s.renderAuthPage(w, http.StatusOK, "", safeNext(r.URL.Query().Get("next")))
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	c, err := readCreds(r)
	if err != nil {
		s.authFail(w, r, errors.New("请求参数格式错误"), c)
		return
	}
	if c.Confirm != "" && c.Confirm != c.Password {
		s.authFail(w, r, errors.New("两次输入的密码不一致"), c)
		return
	}
	u, err := s.auth.Register(c.Username, c.Password, c.Invite)
	if err != nil {
		s.authFail(w, r, err, c)
		return
	}
	// 注册成功后自动登录。
	token, err := s.auth.CreateSession(u)
	if err != nil {
		s.authFail(w, r, errors.New("创建会话失败"), c)
		return
	}
	setSessionCookie(w, r, token, s.auth.SessionTTL())
	if isJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": userDTO(u)})
		return
	}
	http.Redirect(w, r, c.Next, http.StatusSeeOther)
}

func (s *Server) handleLoginAPI(w http.ResponseWriter, r *http.Request) {
	c, err := readCreds(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation", err.Error())
		return
	}
	u, token, err := s.auth.Login(c.Username, c.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "用户名或密码错误")
		return
	}
	setSessionCookie(w, r, token, s.auth.SessionTTL())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": userDTO(u)})
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	c, err := readCreds(r)
	if err != nil {
		s.renderAuthPage(w, http.StatusBadRequest, err.Error(), c.Next)
		return
	}
	_, token, err := s.auth.Login(c.Username, c.Password)
	if err != nil {
		s.renderAuthPage(w, http.StatusUnauthorized, "用户名或密码错误", c.Next)
		return
	}
	setSessionCookie(w, r, token, s.auth.SessionTTL())
	http.Redirect(w, r, c.Next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.auth.Logout(c.Value)
	}
	clearSessionCookie(w)
	if isJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	rt := s.currentRuntime(r)
	if rt == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "未登录")
		return
	}
	writeJSON(w, http.StatusOK, userDTO(rt.User))
}

func (s *Server) renderAuthPage(w http.ResponseWriter, status int, message, next string) {
	signup := ""
	if s.auth.SignupEnabled() {
		inviteField := ""
		if s.auth.InviteRequired() {
			inviteField = `<input type="password" name="invite" placeholder="注册邀请码" autocomplete="off" required />`
		}
		signup = strings.Replace(signupBlock, "__INVITE_FIELD__", inviteField, 1)
	}
	page := strings.Replace(authPage, "__SIGNUP_BLOCK__", signup, 1)
	page = strings.NewReplacer(
		"__MESSAGE__", html.EscapeString(message),
		"__NEXT__", html.EscapeString(next),
	).Replace(page)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprint(w, page)
}

const authPage = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>db-fly · 登录</title>
<style>
  * { box-sizing: border-box; }
  body { margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center; background: #0f1420; color: #e6e9f0; font: 14px/1.7 system-ui, sans-serif; }
  main { width: 360px; padding: 26px 28px; background: #161d2c; border: 1px solid #2a3448; border-radius: 12px; }
  h1 { margin: 0 0 4px; font-size: 18px; }
  h2 { margin: 0 0 10px; font-size: 13px; font-weight: 600; color: #c9d3e6; }
  p { margin: 0 0 16px; color: #9aa6bd; font-size: 12px; }
  input { width: 100%; margin-bottom: 8px; padding: 9px 10px; border-radius: 7px; border: 1px solid #35425c; background: #0f1624; color: #e6e9f0; }
  button { width: 100%; padding: 9px; border: 0; border-radius: 7px; background: #4c7dff; color: #fff; cursor: pointer; }
  hr { border: 0; border-top: 1px solid #2a3448; margin: 20px 0 16px; }
  .err { margin-bottom: 10px; color: #ff8b8b; font-size: 12px; min-height: 1px; }
  .hint { font-size: 11px; color: #7e8aa3; margin: 8px 0 0; }
</style>
</head>
<body>
<main>
  <h1>db-fly Web</h1>
  <p>连接、收藏与历史按账号独立保存</p>
  <div class="err">__MESSAGE__</div>
  <h2>登录</h2>
  <form method="post" action="/login">
    <input type="hidden" name="next" value="__NEXT__" />
    <input name="username" placeholder="用户名" autocomplete="username" required autofocus />
    <input type="password" name="password" placeholder="密码" autocomplete="current-password" required />
    <button type="submit">登录</button>
  </form>
  __SIGNUP_BLOCK__
  <p class="hint">会话仅保存在服务端内存，重启服务后需重新登录。</p>
</main>
</body>
</html>`

const signupBlock = `<hr />
  <h2>注册新账号</h2>
  <form method="post" action="/api/auth/register">
    <input type="hidden" name="next" value="__NEXT__" />
    <input name="username" placeholder="用户名（3-32 位字母/数字/_-）" autocomplete="username" required />
    <input type="password" name="password" placeholder="密码（至少 8 位）" autocomplete="new-password" required />
    <input type="password" name="confirm" placeholder="确认密码" autocomplete="new-password" required />
    __INVITE_FIELD__
    <button type="submit">注册并登录</button>
  </form>`
