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
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			s.unauthorized(w, r)
			return
		}
		u, err := s.auth.UserByToken(c.Value)
		if err != nil {
			s.unauthorized(w, r)
			return
		}
		rt, err := s.auth.RuntimeFor(u.ID)
		if err != nil {
			s.logf("httpapi: 加载用户 %s 的运行时失败: %v", u.Username, err)
			s.unauthorized(w, r)
			return
		}
		// 滑动会话：每次有效访问都把过期时间顺延至「现在 + TTL」（默认 7 天），
		// 并刷新浏览器 cookie，使其同样 7 天从最近一次活动起算。
		s.auth.Refresh(c.Value)
		setSessionCookie(w, r, c.Value, s.auth.SessionTTL())
		next.ServeHTTP(w, r.WithContext(withRuntime(r.Context(), rt)))
	})
}

// unauthorized 按请求类型返回未登录响应：API 返回 401 JSON，页面跳登录页。
func (s *Server) unauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
		return
	}
	target := "/login?next=" + url.QueryEscape(safeNext(r.URL.RequestURI()))
	http.Redirect(w, r, target, http.StatusFound)
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

func (s *Server) authFail(w http.ResponseWriter, r *http.Request, err error, c credForm, mode string) {
	status := statusFor(err)
	if isJSON(r) {
		code := "validation"
		if status == http.StatusUnauthorized {
			code = "unauthorized"
		}
		writeError(w, status, code, err.Error())
		return
	}
	s.renderAuthPage(w, status, err.Error(), c.Next, mode)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	mode := "login"
	if r.URL.Query().Get("mode") == "signup" {
		mode = "signup"
	}
	s.renderAuthPage(w, http.StatusOK, "", safeNext(r.URL.Query().Get("next")), mode)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	c, err := readCreds(r)
	if err != nil {
		s.authFail(w, r, errors.New("请求参数格式错误"), c, "signup")
		return
	}
	if c.Confirm != "" && c.Confirm != c.Password {
		s.authFail(w, r, errors.New("两次输入的密码不一致"), c, "signup")
		return
	}
	u, err := s.auth.Register(c.Username, c.Password, c.Invite)
	if err != nil {
		s.authFail(w, r, err, c, "signup")
		return
	}
	// 注册成功后自动登录。
	token, err := s.auth.CreateSession(u)
	if err != nil {
		s.authFail(w, r, errors.New("创建会话失败"), c, "signup")
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
		s.renderAuthPage(w, http.StatusBadRequest, err.Error(), c.Next, "login")
		return
	}
	_, token, err := s.auth.Login(c.Username, c.Password)
	if err != nil {
		s.renderAuthPage(w, http.StatusUnauthorized, "用户名或密码错误", c.Next, "login")
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

func (s *Server) renderAuthPage(w http.ResponseWriter, status int, message, next, mode string) {
	if mode != "signup" {
		mode = "login"
	}
	signupPanel := ""
	if s.auth.SignupEnabled() {
		inviteField := ""
		if s.auth.InviteRequired() {
			inviteField = `<label class="field-label">注册邀请码</label><input type="password" name="invite" placeholder="请输入邀请码" autocomplete="off" required />`
		}
		signupPanel = strings.NewReplacer(
			"__INVITE_FIELD__", inviteField,
			"__NEXT__", html.EscapeString(next),
		).Replace(signupPanelBlock)
	}
	page := strings.NewReplacer(
		"__SIGNUP_PANEL__", signupPanel,
		"__MESSAGE__", html.EscapeString(message),
		"__NEXT__", html.EscapeString(next),
		"__MODE__", mode,
	).Replace(authPage)
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
  body { margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center; background: linear-gradient(135deg, #0f1420 0%, #162033 100%); color: #e6e9f0; font: 14px/1.7 system-ui, -apple-system, "Segoe UI", sans-serif; }
  main { width: 380px; padding: 32px 30px; background: #161d2c; border: 1px solid #2a3448; border-radius: 14px; box-shadow: 0 12px 40px rgba(0,0,0,.45); }
  .brand { display: flex; align-items: center; gap: 10px; margin-bottom: 4px; }
  .brand .logo { width: 30px; height: 30px; border-radius: 8px; background: linear-gradient(135deg, #4c7dff, #6f5bff); display: flex; align-items: center; justify-content: center; font-size: 13px; font-weight: 700; color: #fff; }
  h1 { margin: 0; font-size: 19px; }
  .subtitle { margin: 2px 0 18px 40px; color: #9aa6bd; font-size: 12px; }
  h2 { margin: 0 0 14px; font-size: 15px; font-weight: 600; color: #c9d3e6; }
  .err { margin-bottom: 12px; color: #ff8b8b; font-size: 12px; min-height: 16px; }
  .field-label { display: block; margin: 12px 0 5px; font-size: 12px; color: #9aa6bd; }
  input { width: 100%; padding: 10px 12px; border-radius: 8px; border: 1px solid #35425c; background: #0f1624; color: #e6e9f0; font-size: 14px; outline: none; transition: border-color .15s; }
  input:focus { border-color: #4c7dff; }
  .btn-primary { width: 100%; margin-top: 18px; padding: 11px; border: 0; border-radius: 8px; background: linear-gradient(135deg, #4c7dff, #6f5bff); color: #fff; font-size: 15px; cursor: pointer; letter-spacing: 4px; }
  .btn-primary:hover { opacity: .92; }
  .switch { margin-top: 16px; text-align: center; font-size: 12px; color: #9aa6bd; }
  .link { background: none; border: 0; color: #4c7dff; cursor: pointer; font-size: 12px; padding: 0 2px; }
  .link:hover { text-decoration: underline; }
  .hint { font-size: 11px; color: #7e8aa3; margin: 18px 0 0; text-align: center; line-height: 1.6; }
  [hidden] { display: none !important; }
</style>
</head>
<body>
<main>
  <div class="brand">
    <div class="logo">db</div>
    <h1>db-fly</h1>
  </div>
  <p class="subtitle">统一的数据库连接与查询工作台</p>
  <div class="err">__MESSAGE__</div>

  <section id="login-panel">
    <h2>账号登录</h2>
    <form method="post" action="/login">
      <input type="hidden" name="next" value="__NEXT__" />
      <label class="field-label">用户名</label>
      <input name="username" placeholder="请输入用户名" autocomplete="username" required autofocus />
      <label class="field-label">密码</label>
      <input type="password" name="password" placeholder="请输入密码" autocomplete="current-password" required />
      <button type="submit" class="btn-primary">登 录</button>
    </form>
    <div class="switch">还没有账号？<button type="button" class="link" data-tab="signup">立即注册</button></div>
  </section>

  __SIGNUP_PANEL__

  <p class="hint">登录会话有效期 7 天，期间有访问会自动顺延；重启服务后需重新登录。</p>
</main>
<script>
  (function () {
    var switches = document.querySelectorAll('[data-tab]');
    function show(name) {
      var lp = document.getElementById('login-panel');
      var sp = document.getElementById('signup-panel');
      if (lp) lp.hidden = name !== 'login';
      if (sp) sp.hidden = name !== 'signup';
      if (name === 'signup' && sp) {
        var first = sp.querySelector('input');
        if (first) first.focus();
      }
    }
    switches.forEach(function (b) {
      b.addEventListener('click', function () { show(b.dataset.tab); });
    });
    var params = new URLSearchParams(location.search);
    show(params.get('mode') === 'signup' ? 'signup' : '__MODE__');
  })();
</script>
</body>
</html>`

const signupPanelBlock = `<section id="signup-panel" hidden>
  <h2>注册新账号</h2>
  <form method="post" action="/api/auth/register">
    <input type="hidden" name="next" value="__NEXT__" />
    <label class="field-label">用户名</label>
    <input name="username" placeholder="3-32 位字母 / 数字 / _-" autocomplete="username" required />
    <label class="field-label">密码</label>
    <input type="password" name="password" placeholder="至少 8 位字符" autocomplete="new-password" required />
    <label class="field-label">确认密码</label>
    <input type="password" name="confirm" placeholder="请再次输入密码" autocomplete="new-password" required />
    __INVITE_FIELD__
    <button type="submit" class="btn-primary">注 册</button>
  </form>
  <div class="switch">已有账号？<button type="button" class="link" data-tab="login">返回登录</button></div>
</section>`
