package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	appsecurity "dladmin-go/internal/security"
	"dladmin-go/internal/store"
)

const (
	sessionCookieName = "dladmin_session"
	csrfCookieName    = "dladmin_csrf"
	sessionLifetime   = 12 * time.Hour
)

type sessionContextKey struct{}

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`)

const maxLoginFormBytes = 4096

func (a *App) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", a.handleLogin)
	mux.HandleFunc("/logout", a.requireLogin(a.handleLogout))
	mux.HandleFunc("/admin", a.requireLogin(a.handleAdmin))
	mux.HandleFunc("/api/config", a.requireLogin(a.handleConfigAPI))
	mux.HandleFunc("/api/session", a.requireLogin(a.handleSessionAPI))
	mux.HandleFunc("/api/stats", a.requireLogin(a.handleStatsAPI))
	mux.HandleFunc("/api/files", a.requireLogin(a.handleFilesAPI))
	mux.HandleFunc("/api/mkdir", a.requireLogin(a.handleMkdirAPI))
	mux.HandleFunc("/api/upload", a.requireLogin(a.handleUploadAPI))
	mux.HandleFunc("/api/delete", a.requireLogin(a.handleDeleteAPI))
	mux.HandleFunc("/api/generate", a.requireLogin(a.handleGenerateAPI))
	mux.HandleFunc("/api/change-password", a.requireLogin(a.handleChangePasswordAPI))
	mux.HandleFunc("/api/audit", a.requireLogin(a.handleAuditAPI))
	mux.HandleFunc("/api/releases", a.requireLogin(a.handleReleasesAPI))
	mux.HandleFunc("/api/releases/upload", a.requireLogin(a.handleReleaseUploadAPI))
	mux.HandleFunc("/api/releases/import", a.requireLogin(a.handleReleaseImportAPI))
	mux.HandleFunc("/api/releases/publish", a.requireLogin(a.handleReleasePublishAPI))
	mux.HandleFunc("/api/v1/updates/", a.handleUpdateAPI)
	mux.HandleFunc("/api/v1/tauri/", a.handleTauriUpdateAPI)
	mux.HandleFunc("/", a.handlePublic)
	return securityHeaders(mux)
}

func migrateLegacyRuntimeFile(legacyPath, statePath string) error {
	if filepath.Clean(legacyPath) == filepath.Clean(statePath) {
		return nil
	}
	if _, err := os.Stat(statePath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查状态文件失败: %w", err)
	}
	data, err := os.ReadFile(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取旧状态文件失败: %w", err)
	}
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		return fmt.Errorf("迁移状态文件失败: %w", err)
	}
	return nil
}

func (a *App) bootstrapAdministrator(legacyAuthPath string) error {
	ctx := context.Background()
	count, err := a.store.UserCount(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	username := strings.TrimSpace(os.Getenv("DL_ADMIN_USERNAME"))
	if username == "" {
		username = "admin"
	}
	if !usernamePattern.MatchString(username) {
		return errors.New("DL_ADMIN_USERNAME 必须是 3-64 位字母、数字、点、下划线或短横线")
	}
	if password := os.Getenv("DL_ADMIN_PASSWORD"); password != "" {
		hash, err := appsecurity.HashPassword(password)
		if err != nil {
			return fmt.Errorf("DL_ADMIN_PASSWORD 不符合强度要求: %w", err)
		}
		if _, err := a.store.CreateUser(ctx, username, hash, "admin"); err != nil {
			return err
		}
		return a.store.AppendAudit(ctx, nil, "system", "bootstrap", "user", username, "local", "", "success", `{"source":"environment"}`)
	}

	data, err := os.ReadFile(legacyAuthPath)
	if err == nil {
		var legacy AuthConfig
		if json.Unmarshal(data, &legacy) == nil && usernamePattern.MatchString(legacy.Username) {
			if appsecurity.IsKnownDefaultLegacyHash(legacy.PasswordHash) {
				return errors.New("检测到公开默认管理员口令；请设置至少 12 位的 DL_ADMIN_PASSWORD 后启动")
			}
			hash, hashErr := appsecurity.LegacyPasswordHash(legacy.PasswordHash)
			if hashErr == nil {
				if _, createErr := a.store.CreateUser(ctx, legacy.Username, hash, "admin"); createErr != nil {
					return createErr
				}
				return a.store.AppendAudit(ctx, nil, "system", "migrate", "user", legacy.Username, "local", "", "success", `{"source":"legacy-auth"}`)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("读取旧管理员配置失败: %w", err)
	}
	return errors.New("未配置管理员；首次启动必须设置 DL_ADMIN_PASSWORD（至少 12 位）")
}

func (a *App) requestSession(r *http.Request) (store.Session, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return store.Session{}, store.ErrNotFound
	}
	session, err := a.store.Session(r.Context(), appsecurity.TokenHash(cookie.Value), time.Now())
	if err != nil || session.Disabled {
		return store.Session{}, store.ErrNotFound
	}
	return session, nil
}

func sessionFromContext(r *http.Request) (store.Session, bool) {
	session, ok := r.Context().Value(sessionContextKey{}).(store.Session)
	return session, ok
}

func (a *App) requireLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, err := a.requestSession(r)
		if err != nil {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "error": "登录已过期，请重新登录"})
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if !isSafeMethod(r.Method) {
			if !sameOrigin(r) || !appsecurity.TokenMatches(session.CSRFHash, r.Header.Get("X-CSRF-Token")) {
				a.audit(r, &session, "csrf", "request", r.URL.Path, "denied", `{}`)
				writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": "请求安全校验失败，请刷新后台后重试"})
				return
			}
		}
		ctx := context.WithValue(r.Context(), sessionContextKey{}, session)
		next(w, r.WithContext(ctx))
	}
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func sameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	scheme := "https"
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && net.ParseIP(host).IsLoopback() {
		if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded == "http" || forwarded == "https" {
			scheme = forwarded
		}
	} else if r.TLS == nil {
		scheme = "http"
	}
	return strings.EqualFold(parsed.Scheme, scheme) && strings.EqualFold(parsed.Host, r.Host)
}

func parseLoginForm(w http.ResponseWriter, r *http.Request) (url.Values, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return nil, errors.New("unsupported login form content type")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLoginFormBytes))
	if err != nil {
		return nil, err
	}
	return url.ParseQuery(string(body))
}

func (a *App) createSession(w http.ResponseWriter, r *http.Request, user store.User) error {
	sessionToken, err := appsecurity.RandomToken()
	if err != nil {
		return err
	}
	csrfToken, err := appsecurity.RandomToken()
	if err != nil {
		return err
	}
	expires := time.Now().Add(sessionLifetime)
	if err := a.store.PurgeExpiredSessions(r.Context(), time.Now()); err != nil {
		return err
	}
	if err := a.store.CreateSession(r.Context(), appsecurity.TokenHash(sessionToken), appsecurity.TokenHash(csrfToken), user.ID, expires); err != nil {
		return err
	}
	maxAge := int(sessionLifetime.Seconds())
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: sessionToken, Path: "/", HttpOnly: true,
		Secure: a.cookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge, Expires: expires,
	})
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookieName, Value: csrfToken, Path: "/", HttpOnly: true,
		Secure: a.cookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge, Expires: expires,
	})
	return nil
}

func (a *App) clearSession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		_ = a.store.DeleteSession(r.Context(), appsecurity.TokenHash(cookie.Value))
	}
	for _, name := range []string{sessionCookieName, csrfCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", HttpOnly: true, Secure: a.cookieSecure,
			SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0),
		})
	}
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if _, err := a.requestSession(r); err == nil {
			http.Redirect(w, r, "/admin", http.StatusFound)
			return
		}
		a.renderTemplate(w, "login.html", loginPageData{})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	remoteIP := clientIP(r)
	if allowed, retryAfter := a.loginLimit.Allow(remoteIP, time.Now()); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(maxInt(1, int(retryAfter.Seconds()))))
		w.WriteHeader(http.StatusTooManyRequests)
		a.renderTemplate(w, "login.html", loginPageData{Error: "登录失败次数过多，请稍后重试"})
		return
	}
	if !sameOrigin(r) {
		w.WriteHeader(http.StatusBadRequest)
		a.renderTemplate(w, "login.html", loginPageData{Error: "登录请求来源无效，请通过 dl.100ask.net 访问"})
		return
	}
	form, err := parseLoginForm(w, r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		a.renderTemplate(w, "login.html", loginPageData{Error: "登录表单格式无效，请刷新页面后重试"})
		return
	}
	username := strings.TrimSpace(form.Get("username"))
	password := form.Get("password")
	user, err := a.store.UserByUsername(r.Context(), username)
	stored := a.dummyPasswordHash
	if err == nil && !user.Disabled {
		stored = user.PasswordHash
	}
	valid, upgrade := appsecurity.VerifyPassword(stored, password)
	if err != nil || user.Disabled || !valid {
		a.loginLimit.Failure(remoteIP, time.Now())
		a.audit(r, nil, "login", "session", "", "failure", `{}`)
		w.WriteHeader(http.StatusUnauthorized)
		a.renderTemplate(w, "login.html", loginPageData{Error: "用户名或密码错误"})
		return
	}
	if upgrade {
		newHash, hashErr := appsecurity.HashPassword(password)
		if hashErr != nil || a.store.UpdatePassword(r.Context(), user.ID, newHash) != nil {
			w.WriteHeader(http.StatusInternalServerError)
			a.renderTemplate(w, "login.html", loginPageData{Error: "升级账号安全配置失败"})
			return
		}
	}
	if err := a.createSession(w, r, user); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		a.renderTemplate(w, "login.html", loginPageData{Error: "创建会话失败"})
		return
	}
	a.loginLimit.Success(remoteIP)
	a.audit(r, &store.Session{User: user}, "login", "session", "", "success", `{}`)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, _ := sessionFromContext(r)
	a.audit(r, &session, "logout", "session", "", "success", `{}`)
	a.clearSession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) handleAdmin(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFromContext(r)
	csrf, err := r.Cookie(csrfCookieName)
	if !ok || err != nil || !appsecurity.TokenMatches(session.CSRFHash, csrf.Value) {
		a.clearSession(w, r)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.renderTemplate(w, "admin.html", adminPageData{
		Username: session.Username, Role: session.Role, CSRFToken: csrf.Value,
	})
}

func (a *App) handleSessionAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, ok := sessionFromContext(r)
	csrf, err := r.Cookie(csrfCookieName)
	if !ok || err != nil || !appsecurity.TokenMatches(session.CSRFHash, csrf.Value) {
		a.clearSession(w, r)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "error": "登录已过期，请重新登录"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "csrf_token": csrf.Value})
}

func (a *App) handleChangePasswordAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, ok := sessionFromContext(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "error": "登录已过期"})
		return
	}
	var payload struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "请求数据无效"})
		return
	}
	valid, _ := appsecurity.VerifyPassword(session.PasswordHash, payload.CurrentPassword)
	if !valid {
		a.audit(r, &session, "change_password", "user", session.Username, "denied", `{}`)
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "当前密码错误"})
		return
	}
	newHash, err := appsecurity.HashPassword(payload.NewPassword)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "新密码至少 12 位且不能超过 256 位"})
		return
	}
	if err := a.store.UpdatePassword(r.Context(), session.ID, newHash); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "保存密码失败"})
		return
	}
	a.audit(r, &session, "change_password", "user", session.Username, "success", `{}`)
	_ = a.store.DeleteUserSessions(r.Context(), session.ID)
	a.clearSession(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "密码已修改，请重新登录"})
}

func (a *App) handleAuditAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, ok := sessionFromContext(r)
	if !ok || (session.Role != "admin" && session.Role != "auditor") {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": "权限不足"})
		return
	}
	events, err := a.store.ListAudit(r.Context(), 200)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "读取审计日志失败"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "events": events})
}

func (a *App) audit(r *http.Request, session *store.Session, action, objectType, objectID, outcome, details string) {
	actor := "anonymous"
	var actorID *int64
	if session != nil && session.ID != 0 {
		actor = session.Username
		id := session.ID
		actorID = &id
	}
	_ = a.store.AppendAudit(r.Context(), actorID, actor, action, objectType, objectID,
		clientIP(r), truncate(r.UserAgent(), 256), outcome, details)
}

func sessionPointer(r *http.Request) *store.Session {
	session, ok := sessionFromContext(r)
	if !ok {
		return nil
	}
	return &session
}

func requireSessionRole(w http.ResponseWriter, r *http.Request, roles ...string) (store.Session, bool) {
	session, ok := sessionFromContext(r)
	if ok {
		for _, role := range roles {
			if session.Role == role {
				return session, true
			}
		}
	}
	writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": "权限不足"})
	return store.Session{}, false
}

func (a *App) moveToTrash(target, relativePath string) (string, error) {
	if relativePath == "" || relativePath == "." {
		return "", errors.New("不能回收公开根目录")
	}
	token, err := appsecurity.RandomToken()
	if err != nil {
		return "", err
	}
	trashID := time.Now().UTC().Format("20060102T150405Z") + "-" + token[:16]
	destination := filepath.Join(a.trashDir, trashID, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return "", err
	}
	if err := os.Rename(target, destination); err != nil {
		return "", fmt.Errorf("移动到回收站失败（状态目录必须与公开目录位于同一文件系统）: %w", err)
	}
	return trashID, nil
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote := net.ParseIP(host)
	if remote != nil && remote.IsLoopback() {
		if forwarded := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); forwarded != nil {
			return forwarded.String()
		}
	}
	if remote == nil {
		return "unknown"
	}
	return remote.String()
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https://www.100ask.net; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		if r.URL.Path == "/admin" || (strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/api/v1/updates/")) {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
