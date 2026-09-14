package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dladmin-go/internal/store"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	publicDir := filepath.Join(root, "public")
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DL_STATE_DIR", stateDir)
	t.Setenv("DL_ADMIN_USERNAME", "release-admin")
	t.Setenv("DL_ADMIN_PASSWORD", "a strong test password")
	t.Setenv("DL_COOKIE_SECURE", "false")
	app, err := newApp(publicDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.store.Close() })
	return app
}

func TestStartupRejectsMissingAdministrator(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DL_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("DL_ADMIN_PASSWORD", "")
	if _, err := newApp(root); err == nil || !strings.Contains(err.Error(), "未配置管理员") {
		t.Fatalf("expected explicit bootstrap error, got %v", err)
	}
}

func TestPublicDirectoryUsesOfficialBrandWithoutAdminEntry(t *testing.T) {
	app := newTestApp(t)
	request := httptest.NewRequest(http.MethodGet, "http://dl.test/", nil)
	result := httptest.NewRecorder()
	app.handler().ServeHTTP(result, request)
	body := result.Body.String()
	if result.Code != http.StatusOK || !strings.Contains(body, "https://www.100ask.net/_nuxt/logo.") {
		t.Fatalf("official public branding missing: status=%d", result.Code)
	}
	if strings.Contains(body, `href="/admin"`) || strings.Contains(body, "管理后台") {
		t.Fatal("public directory disclosed the administration entry")
	}
}

func TestReadOnlyAuditorCannotMutatePublicFiles(t *testing.T) {
	app := newTestApp(t)
	request := httptest.NewRequest(http.MethodPost, "http://dl.test/api/mkdir", strings.NewReader(`{"parent_path":"","name":"forbidden"}`))
	request.Header.Set("Content-Type", "application/json")
	session := store.Session{User: store.User{ID: 99, Username: "auditor", Role: "auditor"}}
	request = request.WithContext(context.WithValue(request.Context(), sessionContextKey{}, session))
	result := httptest.NewRecorder()
	app.handleMkdirAPI(result, request)
	if result.Code != http.StatusForbidden {
		t.Fatalf("auditor mutation status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestLoginCSRFAndAuditFlow(t *testing.T) {
	app := newTestApp(t)
	handler := app.handler()
	form := url.Values{"username": {"release-admin"}, "password": {"a strong test password"}}
	login := httptest.NewRequest(http.MethodPost, "http://dl.test/login", strings.NewReader(form.Encode()))
	login.Host = "dl.test"
	login.RemoteAddr = "127.0.0.1:12345"
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	login.Header.Set("Origin", "http://dl.test")
	login.Header.Set("X-Forwarded-Proto", "http")
	loginResult := httptest.NewRecorder()
	handler.ServeHTTP(loginResult, login)
	if loginResult.Code != http.StatusSeeOther {
		t.Fatalf("login status=%d body=%s", loginResult.Code, loginResult.Body.String())
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range loginResult.Result().Cookies() {
		switch cookie.Name {
		case sessionCookieName:
			sessionCookie = cookie
		case csrfCookieName:
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || csrfCookie == nil || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("secure session cookies missing: %#v %#v", sessionCookie, csrfCookie)
	}
	sessionRefresh := httptest.NewRequest(http.MethodGet, "http://dl.test/api/session", nil)
	sessionRefresh.Host = "dl.test"
	sessionRefresh.RemoteAddr = "127.0.0.1:12345"
	sessionRefresh.AddCookie(sessionCookie)
	sessionRefresh.AddCookie(csrfCookie)
	sessionRefreshResult := httptest.NewRecorder()
	handler.ServeHTTP(sessionRefreshResult, sessionRefresh)
	if sessionRefreshResult.Code != http.StatusOK || !strings.Contains(sessionRefreshResult.Body.String(), csrfCookie.Value) {
		t.Fatalf("session refresh status=%d body=%s", sessionRefreshResult.Code, sessionRefreshResult.Body.String())
	}

	request := func(method, path string, body io.Reader, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://dl.test"+path, body)
		r.Host = "dl.test"
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Origin", "http://dl.test")
		r.Header.Set("X-Forwarded-Proto", "http")
		r.AddCookie(sessionCookie)
		r.AddCookie(csrfCookie)
		if csrf {
			r.Header.Set("X-CSRF-Token", csrfCookie.Value)
		}
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, r)
		return result
	}

	admin := request(http.MethodGet, "/admin", nil, false)
	if admin.Code != http.StatusOK || !strings.Contains(admin.Body.String(), "release-admin") {
		t.Fatalf("admin status=%d body=%s", admin.Code, admin.Body.String())
	}
	blocked := request(http.MethodPost, "/api/mkdir", strings.NewReader(`{"parent_path":"","name":"Tools"}`), false)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	allowedRequest := httptest.NewRequest(http.MethodPost, "http://dl.test/api/mkdir", strings.NewReader(`{"parent_path":"","name":"Tools"}`))
	allowedRequest.Header.Set("Content-Type", "application/json")
	allowedRequest.Host = "dl.test"
	allowedRequest.RemoteAddr = "127.0.0.1:12345"
	allowedRequest.Header.Set("Origin", "http://dl.test")
	allowedRequest.Header.Set("X-Forwarded-Proto", "http")
	allowedRequest.Header.Set("X-CSRF-Token", csrfCookie.Value)
	allowedRequest.AddCookie(sessionCookie)
	allowedRequest.AddCookie(csrfCookie)
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusOK {
		t.Fatalf("valid CSRF status=%d body=%s", allowed.Code, allowed.Body.String())
	}
	if _, err := os.Stat(filepath.Join(app.baseDir, "Tools")); err != nil {
		t.Fatal(err)
	}
	audit := request(http.MethodGet, "/api/audit", nil, false)
	if audit.Code != http.StatusOK || !strings.Contains(audit.Body.String(), `"login"`) || !strings.Contains(audit.Body.String(), `"csrf"`) {
		t.Fatalf("audit status=%d body=%s", audit.Code, audit.Body.String())
	}
}

func TestLoginParsesOnlyThePostBody(t *testing.T) {
	app := newTestApp(t)
	form := url.Values{"username": {"release-admin"}, "password": {"a strong test password"}}
	request := httptest.NewRequest(http.MethodPost, "http://dl.test/login?broken;query", strings.NewReader(form.Encode()))
	request.Host = "dl.test"
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	request.Header.Set("Origin", "http://dl.test")
	request.Header.Set("X-Forwarded-Proto", "http")
	result := httptest.NewRecorder()
	app.handler().ServeHTTP(result, request)
	if result.Code != http.StatusSeeOther {
		t.Fatalf("login status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestLoginReportsOriginAndFormFailuresSeparately(t *testing.T) {
	app := newTestApp(t)
	request := httptest.NewRequest(http.MethodPost, "http://dl.test/login", strings.NewReader("{}"))
	request.Host = "dl.test"
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://dl.test")
	request.Header.Set("X-Forwarded-Proto", "http")
	result := httptest.NewRecorder()
	app.handler().ServeHTTP(result, request)
	if result.Code != http.StatusBadRequest || !strings.Contains(result.Body.String(), "登录表单格式无效") {
		t.Fatalf("form failure status=%d body=%s", result.Code, result.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "http://dl.test/login", strings.NewReader("username=x&password=y"))
	request.Host = "dl.test"
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://other.test")
	request.Header.Set("X-Forwarded-Proto", "http")
	result = httptest.NewRecorder()
	app.handler().ServeHTTP(result, request)
	if result.Code != http.StatusBadRequest || !strings.Contains(result.Body.String(), "登录请求来源无效") {
		t.Fatalf("origin failure status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestManagedPathsRejectSymlinksAndTrashIsRecoverable(t *testing.T) {
	app := newTestApp(t)
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "secret"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(app.baseDir, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.resolvePath("linked/secret"); err == nil {
		t.Fatal("managed path followed a symbolic link outside the public root")
	}
	file := filepath.Join(app.baseDir, "manual.pdf")
	if err := os.WriteFile(file, []byte("document"), 0o644); err != nil {
		t.Fatal(err)
	}
	trashID, err := app.moveToTrash(file, "manual.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("public file still exists: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(app.trashDir, trashID, "manual.pdf")); err != nil || string(data) != "document" {
		t.Fatalf("trash copy missing: data=%q err=%v", data, err)
	}
	if app.shouldAllowPublicFile("shell.sh") {
		t.Fatal("deployment shell scripts must never be public")
	}
}

func TestReleaseDirectoryIsReserved(t *testing.T) {
	for _, path := range []string{"releases", "releases/lynx/stable", `/releases\\lynx`} {
		if !isReleaseManagedPath(path) {
			t.Fatalf("release path was not protected: %q", path)
		}
	}
	for _, path := range []string{"release-notes", "downloads/releases.zip", ""} {
		if isReleaseManagedPath(path) {
			t.Fatalf("unrelated path was protected: %q", path)
		}
	}
}
