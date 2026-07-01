package main

import (
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed web/*.html
var templateFS embed.FS

type Link struct {
	Href string `json:"href"`
	Text string `json:"text"`
	Icon string `json:"icon,omitempty"`
}

type Feature struct {
	Icon string `json:"icon"`
	Text string `json:"text"`
}

type IconInfo struct {
	Icon  string `json:"icon"`
	Color string `json:"color"`
}

type BreadcrumbConfig struct {
	FallbackLevels int    `json:"fallback_levels"`
	StartFrom      string `json:"start_from"`
}

type ExcludeConfig struct {
	Extensions []string `json:"extensions"`
	Files      []string `json:"files"`
	Hidden     bool     `json:"hidden"`
}

type SiteConfig struct {
	Language string `json:"language"`
	LogoText string `json:"logo_text"`
	Subtitle string `json:"subtitle"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

type HeaderConfig struct {
	NavLinks []Link `json:"nav_links"`
}

type IntroConfig struct {
	Content  string    `json:"content"`
	Enabled  bool      `json:"enabled"`
	Features []Feature `json:"features"`
	Title    string    `json:"title"`
}

type FooterConfig struct {
	Copyright string `json:"copyright"`
	Links     []Link `json:"links"`
}

type StatsConfig struct {
	Enabled bool   `json:"enabled"`
	LogFile string `json:"log_file"`
}

type Config struct {
	Breadcrumb BreadcrumbConfig    `json:"breadcrumb"`
	Exclude    ExcludeConfig       `json:"exclude"`
	FileIcons  map[string]IconInfo `json:"file_icons"`
	Footer     FooterConfig        `json:"footer"`
	Header     HeaderConfig        `json:"header"`
	Intro      IntroConfig         `json:"intro"`
	Site       SiteConfig          `json:"site"`
	Stats      StatsConfig         `json:"stats"`
}

type AuthConfig struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

type StatsData struct {
	TotalVisits int            `json:"total_visits"`
	Daily       map[string]int `json:"daily"`
	Pages       map[string]int `json:"pages"`
	Downloads   map[string]int `json:"downloads"`
}

type App struct {
	baseDir    string
	configPath string
	authPath   string
	statsPath  string
	templates  *template.Template

	mu       sync.RWMutex
	config   Config
	auth     AuthConfig
	stats    StatsData
	sessions map[string]time.Time
}

type loginPageData struct {
	Error string
}

type adminPageData struct {
	Username string
}

type breadcrumbItem struct {
	Text    string
	Href    string
	Current bool
}

type publicEntry struct {
	Name            string
	NameLower       string
	Href            string
	Type            string
	Icon            string
	SizeDisplay     string
	ModifiedDisplay string
	SortSize        int64
	SortTime        int64
}

type directoryPageData struct {
	Site            SiteConfig
	PageTitle       string
	PageHeading     string
	IntroText       string
	DirDisplay      string
	HeaderLinks     []Link
	FooterLinks     []Link
	FooterCopyright string
	Breadcrumbs     []breadcrumbItem
	Entries         []publicEntry
	Readme          string
}

type apiEntry struct {
	Name        string `json:"name"`
	RelativePath string `json:"relative_path"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	SizeDisplay string `json:"size_display"`
	ModifiedAt  string `json:"modified_at"`
}

type directoryPayload struct {
	Success     bool       `json:"success"`
	CurrentPath string     `json:"current_path"`
	CurrentLabel string    `json:"current_label"`
	ParentPath  string     `json:"parent_path"`
	EntryCount  int        `json:"entry_count"`
	Entries     []apiEntry `json:"entries"`
}

func defaultConfig() Config {
	return Config{
		Breadcrumb: BreadcrumbConfig{FallbackLevels: 2, StartFrom: "AppBaseCode"},
		Exclude: ExcludeConfig{
			Extensions: []string{".tmp", ".temp", ".bak", ".swp", ".py", ".json", ".txt", ".html"},
			Files:      []string{"Thumbs.db", "desktop.ini", ".DS_Store", "index.html"},
			Hidden:     true,
		},
		FileIcons: map[string]IconInfo{
			"default": {Icon: "file", Color: "#95a5a6"},
			"folder":  {Icon: "folder", Color: "#f39c12"},
			".zip":    {Icon: "archive", Color: "#8e44ad"},
			".7z":     {Icon: "archive", Color: "#8e44ad"},
			".pdf":    {Icon: "pdf", Color: "#c0392b"},
			".png":    {Icon: "image", Color: "#1abc9c"},
			".jpg":    {Icon: "image", Color: "#1abc9c"},
			".jpeg":   {Icon: "image", Color: "#1abc9c"},
			".gz":     {Icon: "archive", Color: "#8e44ad"},
			".tar.gz": {Icon: "archive", Color: "#8e44ad"},
			".xz":     {Icon: "archive", Color: "#8e44ad"},
			".img":    {Icon: "disc", Color: "#e74c3c"},
			".bin":    {Icon: "binary", Color: "#7f8c8d"},
			".md":     {Icon: "text", Color: "#95a5a6"},
		},
		Footer: FooterConfig{
			Copyright: "© 深圳百问科技有限公司 All Rights Reserved",
			Links: []Link{
				{Href: "/", Text: "资源下载中心"},
				{Href: "https://beian.miit.gov.cn/", Text: "粤ICP备13035650号"},
			},
		},
		Header: HeaderConfig{
			NavLinks: []Link{
				{Href: "/", Text: "首页", Icon: "home"},
				{Href: "/Hardware/", Text: "硬件资源", Icon: "chip"},
				{Href: "/Video/", Text: "视频教程", Icon: "video"},
			},
		},
		Intro: IntroConfig{
			Content: "提供嵌入式开发板系统镜像、原理图、工具软件等资源下载。",
			Enabled: true,
			Features: []Feature{
				{Icon: "📦", Text: "系统镜像与工具下载"},
				{Icon: "📋", Text: "原理图与数据手册"},
				{Icon: "🔍", Text: "文件搜索与筛选"},
				{Icon: "📊", Text: "多维度排序"},
			},
			Title: "✨ 百问科技资源下载中心",
		},
		Site: SiteConfig{
			Language: "zh-CN",
			LogoText: "100ASK DL",
			Subtitle: "百问科技资源下载站",
			Title:    "系统镜像工具 原理图 软件下载中心",
			URL:      "https://dl.100ask.net",
		},
		Stats: StatsConfig{
			Enabled: true,
			LogFile: "access_stats.json",
		},
	}
}

func (c *Config) applyDefaults() {
	def := defaultConfig()
	if c.Site.Title == "" {
		c.Site.Title = def.Site.Title
	}
	if c.Site.LogoText == "" {
		c.Site.LogoText = def.Site.LogoText
	}
	if c.Site.Language == "" {
		c.Site.Language = def.Site.Language
	}
	if c.Site.URL == "" {
		c.Site.URL = def.Site.URL
	}
	if c.Intro.Title == "" {
		c.Intro.Title = def.Intro.Title
	}
	if c.Intro.Content == "" {
		c.Intro.Content = def.Intro.Content
	}
	if len(c.Header.NavLinks) == 0 {
		c.Header.NavLinks = def.Header.NavLinks
	}
	if len(c.Footer.Links) == 0 {
		c.Footer.Links = def.Footer.Links
	}
	if c.Footer.Copyright == "" {
		c.Footer.Copyright = def.Footer.Copyright
	}
	if len(c.Exclude.Extensions) == 0 {
		c.Exclude.Extensions = def.Exclude.Extensions
	}
	if len(c.Exclude.Files) == 0 {
		c.Exclude.Files = def.Exclude.Files
	}
	if c.FileIcons == nil {
		c.FileIcons = def.FileIcons
	}
}

func defaultAuth() AuthConfig {
	return AuthConfig{
		Username:     "admin",
		PasswordHash: hashPassword("admin123"),
	}
}

func defaultStats() StatsData {
	return StatsData{
		Daily:     map[string]int{},
		Pages:     map[string]int{},
		Downloads: map[string]int{},
	}
}

func hashPassword(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}

func loadJSONFile[T any](filePath string, fallback T) (T, error) {
	if _, err := os.Stat(filePath); errors.Is(err, os.ErrNotExist) {
		if err := writeJSONFile(filePath, fallback); err != nil {
			return fallback, err
		}
		return fallback, nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fallback, err
	}
	if len(data) == 0 {
		return fallback, nil
	}
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		return fallback, err
	}
	return result, nil
}

func writeJSONFile(filePath string, value any) error {
	data, err := json.MarshalIndent(value, "", "    ")
	if err != nil {
		return err
	}
	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, filePath)
}

func newApp(baseDir string) (*App, error) {
	baseDir, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}
	tpl, err := template.ParseFS(templateFS, "web/*.html")
	if err != nil {
		return nil, err
	}

	app := &App{
		baseDir:    baseDir,
		configPath: filepath.Join(baseDir, "config.json"),
		authPath:   filepath.Join(baseDir, "admin_auth.json"),
		statsPath:  filepath.Join(baseDir, "access_stats.json"),
		templates:  tpl,
		sessions:   map[string]time.Time{},
	}

	cfg, err := loadJSONFile(app.configPath, defaultConfig())
	if err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	app.config = cfg

	auth, err := loadJSONFile(app.authPath, defaultAuth())
	if err != nil {
		return nil, err
	}
	if auth.Username == "" {
		auth = defaultAuth()
	}
	app.auth = auth

	stats, err := loadJSONFile(app.statsPath, defaultStats())
	if err != nil {
		return nil, err
	}
	if stats.Daily == nil {
		stats.Daily = map[string]int{}
	}
	if stats.Pages == nil {
		stats.Pages = map[string]int{}
	}
	if stats.Downloads == nil {
		stats.Downloads = map[string]int{}
	}
	app.stats = stats

	return app, nil
}

func main() {
	host := strings.TrimSpace(os.Getenv("HOST"))
	if host == "" {
		host = "0.0.0.0"
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "5000"
	}

	app, err := newApp(".")
	if err != nil {
		fmt.Println("启动失败:", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/login", app.handleLogin)
	mux.HandleFunc("/logout", app.handleLogout)
	mux.HandleFunc("/admin", app.requireLogin(app.handleAdmin))
	mux.HandleFunc("/api/config", app.requireLogin(app.handleConfigAPI))
	mux.HandleFunc("/api/stats", app.requireLogin(app.handleStatsAPI))
	mux.HandleFunc("/api/files", app.requireLogin(app.handleFilesAPI))
	mux.HandleFunc("/api/mkdir", app.requireLogin(app.handleMkdirAPI))
	mux.HandleFunc("/api/upload", app.requireLogin(app.handleUploadAPI))
	mux.HandleFunc("/api/delete", app.requireLogin(app.handleDeleteAPI))
	mux.HandleFunc("/api/generate", app.requireLogin(app.handleGenerateAPI))
	mux.HandleFunc("/api/change-password", app.requireLogin(app.handleChangePasswordAPI))
	mux.HandleFunc("/api/visit", app.handleVisitTrackAPI)
	mux.HandleFunc("/api/download", app.handleDownloadTrackAPI)
	mux.HandleFunc("/", app.handlePublic)

	addr := host + ":" + port
	fmt.Println("==================================================")
	fmt.Println("  dladmin-go - 纯 Go 下载站后台")
	fmt.Println("  站点地址: http://" + addr)
	fmt.Println("  管理后台: http://" + addr + "/admin")
	fmt.Println("  管理员账号: admin")
	fmt.Println("  默认密码: admin123（请及时修改）")
	fmt.Println("==================================================")

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Println("服务退出:", err)
		os.Exit(1)
	}
}

func (a *App) requireLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.isLoggedIn(r) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "error": "未登录"})
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func (a *App) isLoggedIn(r *http.Request) bool {
	cookie, err := r.Cookie("dladmin_session")
	if err != nil || cookie.Value == "" {
		return false
	}
	a.mu.RLock()
	expireAt, ok := a.sessions[cookie.Value]
	a.mu.RUnlock()
	return ok && expireAt.After(time.Now())
}

func (a *App) createSession(w http.ResponseWriter) error {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return err
	}
	token := hex.EncodeToString(tokenBytes)
	a.mu.Lock()
	a.sessions[token] = time.Now().Add(24 * time.Hour)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     "dladmin_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})
	return nil
}

func (a *App) clearSession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("dladmin_session"); err == nil {
		a.mu.Lock()
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "dladmin_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if a.isLoggedIn(r) {
			http.Redirect(w, r, "/admin", http.StatusFound)
			return
		}
		a.renderTemplate(w, "login.html", loginPageData{})
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderTemplate(w, "login.html", loginPageData{Error: "请求数据无效"})
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	a.mu.RLock()
	auth := a.auth
	a.mu.RUnlock()

	if username != auth.Username || hashPassword(password) != auth.PasswordHash {
		a.renderTemplate(w, "login.html", loginPageData{Error: "用户名或密码错误"})
		return
	}
	if err := a.createSession(w); err != nil {
		a.renderTemplate(w, "login.html", loginPageData{Error: "创建会话失败"})
		return
	}
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	a.clearSession(w, r)
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (a *App) handleAdmin(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	auth := a.auth
	a.mu.RUnlock()
	a.renderTemplate(w, "admin.html", adminPageData{Username: auth.Username})
}

func (a *App) handleConfigAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.mu.RLock()
		cfg := a.config
		a.mu.RUnlock()
		writeJSON(w, http.StatusOK, cfg)
	case http.MethodPost:
		var cfg Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "配置 JSON 无效"})
			return
		}
		cfg.applyDefaults()
		if err := writeJSONFile(a.configPath, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "保存配置失败"})
			return
		}
		a.mu.Lock()
		a.config = cfg
		a.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "配置已保存"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) handleStatsAPI(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	stats := a.stats
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, stats)
}

func (a *App) handleFilesAPI(w http.ResponseWriter, r *http.Request) {
	dirPath, relPath, err := a.resolvePath(r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}
	info, err := os.Stat(dirPath)
	if err != nil || !info.IsDir() {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "目录不存在"})
		return
	}
	payload, err := a.buildDirectoryPayload(dirPath, relPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "目录读取失败"})
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *App) handleMkdirAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		ParentPath string `json:"parent_path"`
		Name       string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "请求数据无效"})
		return
	}
	parentDir, _, err := a.resolvePath(payload.ParentPath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}
	name, err := sanitizeName(payload.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}
	target := filepath.Join(parentDir, name)
	if err := ensureSubPath(a.baseDir, target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "目录路径非法"})
		return
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "创建目录失败"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":         true,
		"message":         fmt.Sprintf("目录“%s”已创建", name),
		"path":            a.toRelativePath(target),
		"generated_count": 0,
	})
}

func (a *App) handleUploadAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "上传表单无效"})
		return
	}
	targetDir, _, err := a.resolvePath(r.FormValue("target_path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}
	overwrite := strings.EqualFold(r.FormValue("overwrite"), "true")
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "未选择文件"})
		return
	}

	saved := make([]string, 0, len(files))
	skipped := make([]string, 0)

	for _, header := range files {
		name := filepath.Base(strings.TrimSpace(header.Filename))
		if name == "" || name == "." || name == ".." {
			continue
		}
		targetPath := filepath.Join(targetDir, name)
		if err := ensureSubPath(a.baseDir, targetPath); err != nil {
			skipped = append(skipped, name)
			continue
		}
		if !overwrite {
			if _, err := os.Stat(targetPath); err == nil {
				skipped = append(skipped, name)
				continue
			}
		}
		src, err := header.Open()
		if err != nil {
			skipped = append(skipped, name)
			continue
		}
		if err := writeUploadedFile(targetPath, src); err != nil {
			skipped = append(skipped, name)
			_ = src.Close()
			continue
		}
		_ = src.Close()
		saved = append(saved, name)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":         true,
		"message":         "上传完成",
		"saved":           saved,
		"skipped":         skipped,
		"generated_count": 0,
		"current_path":    a.toRelativePath(targetDir),
	})
}

func (a *App) handleDeleteAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "请求数据无效"})
		return
	}
	target, _, err := a.resolvePath(payload.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}
	if filepath.Clean(target) == filepath.Clean(a.baseDir) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "不能删除站点根目录"})
		return
	}
	info, err := os.Stat(target)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "目标不存在"})
		return
	}
	if info.IsDir() {
		err = os.RemoveAll(target)
	} else {
		err = os.Remove(target)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "删除失败"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":         true,
		"message":         "删除成功",
		"generated_count": 0,
	})
}

func (a *App) handleGenerateAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"count":   0,
		"message": "Go 版本为动态目录展示，无需生成静态页",
	})
}

func (a *App) handleChangePasswordAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "请求数据无效"})
		return
	}
	if len(payload.NewPassword) < 6 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "新密码至少 6 位"})
		return
	}
	a.mu.RLock()
	auth := a.auth
	a.mu.RUnlock()
	if hashPassword(payload.CurrentPassword) != auth.PasswordHash {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "当前密码错误"})
		return
	}
	auth.PasswordHash = hashPassword(payload.NewPassword)
	if err := writeJSONFile(a.authPath, auth); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "保存密码失败"})
		return
	}
	a.mu.Lock()
	a.auth = auth
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "密码已修改，请重新登录"})
}

func (a *App) handleVisitTrackAPI(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Page string `json:"page"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)
	if payload.Page != "" {
		a.recordVisit(payload.Page)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (a *App) handleDownloadTrackAPI(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Filename string `json:"filename"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)
	if payload.Filename != "" {
		a.recordDownload(payload.Filename)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (a *App) handlePublic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/.well-known/") {
		a.serveRawFile(w, r, strings.TrimPrefix(r.URL.Path, "/"))
		return
	}

	fullPath, relPath, err := a.resolvePath(r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if info.IsDir() {
		if relPath != "" && !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
			return
		}
		a.recordVisit("/" + relPath)
		a.renderDirectoryPage(w, r, fullPath, relPath)
		return
	}

	if !a.shouldAllowPublicFile(info.Name()) {
		http.NotFound(w, r)
		return
	}
	a.recordDownload("/" + relPath)
	http.ServeFile(w, r, fullPath)
}

func (a *App) renderDirectoryPage(w http.ResponseWriter, r *http.Request, dirPath, relPath string) {
	entries, err := a.buildPublicEntries(dirPath)
	if err != nil {
		http.Error(w, "读取目录失败", http.StatusInternalServerError)
		return
	}

	a.mu.RLock()
	cfg := a.config
	a.mu.RUnlock()

	readme := loadReadmeText(dirPath)
	pageTitle := cfg.Site.Title
	pageHeading := cfg.Site.Title
	dirDisplay := "/"
	if relPath != "" {
		baseName := path.Base(relPath)
		pageTitle = baseName + " - " + cfg.Site.Title
		pageHeading = baseName
		dirDisplay = "/" + relPath
	}
	introText := cfg.Intro.Content
	if introText == "" {
		introText = "提供系统镜像、原理图、工具软件等资源下载。"
	}
	data := directoryPageData{
		Site:            cfg.Site,
		PageTitle:       pageTitle,
		PageHeading:     pageHeading,
		IntroText:       introText,
		DirDisplay:      dirDisplay,
		HeaderLinks:     cfg.Header.NavLinks,
		FooterLinks:     cfg.Footer.Links,
		FooterCopyright: cfg.Footer.Copyright,
		Breadcrumbs:     buildBreadcrumbs(relPath),
		Entries:         entries,
		Readme:          readme,
	}
	a.renderTemplate(w, "directory.html", data)
}

func (a *App) buildDirectoryPayload(dirPath, relPath string) (directoryPayload, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return directoryPayload{}, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	items := make([]apiEntry, 0, len(entries))
	for _, entry := range entries {
		if !a.shouldIncludeName(entry.Name(), entry.IsDir()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		rel := a.toRelativePath(filepath.Join(dirPath, entry.Name()))
		items = append(items, apiEntry{
			Name:         entry.Name(),
			RelativePath: rel,
			Type:         map[bool]string{true: "dir", false: "file"}[entry.IsDir()],
			Size:         info.Size(),
			SizeDisplay:  formatSize(info.Size()),
			ModifiedAt:   info.ModTime().Format("2006-01-02 15:04:05"),
		})
	}

	parentPath := ""
	if relPath != "" {
		parentPath = path.Dir(relPath)
		if parentPath == "." {
			parentPath = ""
		}
	}
	currentLabel := "/"
	if relPath != "" {
		currentLabel = "/" + relPath
	}
	return directoryPayload{
		Success:      true,
		CurrentPath:  relPath,
		CurrentLabel: currentLabel,
		ParentPath:   parentPath,
		EntryCount:   len(items),
		Entries:      items,
	}, nil
}

func (a *App) buildPublicEntries(dirPath string) ([]publicEntry, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	result := make([]publicEntry, 0, len(entries))
	for _, entry := range entries {
		if !a.shouldIncludeName(entry.Name(), entry.IsDir()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		rel := a.toRelativePath(filepath.Join(dirPath, entry.Name()))
		href := "/" + escapeURLPath(rel)
		if entry.IsDir() {
			href += "/"
		}
		result = append(result, publicEntry{
			Name:            entry.Name(),
			NameLower:       strings.ToLower(entry.Name()),
			Href:            href,
			Type:            map[bool]string{true: "dir", false: "file"}[entry.IsDir()],
			Icon:            a.iconFor(entry.Name(), entry.IsDir()),
			SizeDisplay:     map[bool]string{true: "-", false: formatSize(info.Size())}[entry.IsDir()],
			ModifiedDisplay: info.ModTime().Format("2006-01-02 15:04:05"),
			SortSize:        info.Size(),
			SortTime:        info.ModTime().Unix(),
		})
	}
	return result, nil
}

func (a *App) shouldAllowPublicFile(name string) bool {
	return a.shouldIncludeName(name, false)
}

func (a *App) shouldIncludeName(name string, isDir bool) bool {
	a.mu.RLock()
	cfg := a.config
	a.mu.RUnlock()

	if name == "" {
		return false
	}
	if name == ".well-known" {
		return true
	}
	if strings.HasPrefix(name, "_") || strings.HasPrefix(name, "~") {
		return false
	}
	if cfg.Exclude.Hidden && strings.HasPrefix(name, ".") {
		return false
	}

	lower := strings.ToLower(name)
	for _, fileName := range cfg.Exclude.Files {
		if lower == strings.ToLower(fileName) {
			return false
		}
	}
	if !isDir {
		for _, ext := range cfg.Exclude.Extensions {
			if strings.HasSuffix(lower, strings.ToLower(ext)) {
				return false
			}
		}
	}
	return true
}

func (a *App) resolvePath(raw string) (string, string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	cleanURLPath := path.Clean("/" + normalized)
	if cleanURLPath == "/" {
		return a.baseDir, "", nil
	}
	rel := strings.TrimPrefix(cleanURLPath, "/")
	fullPath := filepath.Clean(filepath.Join(a.baseDir, filepath.FromSlash(rel)))
	if err := ensureSubPath(a.baseDir, fullPath); err != nil {
		return "", "", errors.New("路径非法")
	}
	return fullPath, rel, nil
}

func (a *App) toRelativePath(fullPath string) string {
	rel, err := filepath.Rel(a.baseDir, fullPath)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

func (a *App) renderTemplate(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) recordVisit(page string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.config.Stats.Enabled {
		return
	}
	dayKey := time.Now().Format("2006-01-02")
	a.stats.TotalVisits++
	a.stats.Daily[dayKey]++
	a.stats.Pages[page]++
	_ = writeJSONFile(a.statsPath, a.stats)
}

func (a *App) recordDownload(filename string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.config.Stats.Enabled {
		return
	}
	a.stats.Downloads[filename]++
	_ = writeJSONFile(a.statsPath, a.stats)
}

func (a *App) iconFor(name string, isDir bool) string {
	if isDir {
		return "📁"
	}
	ext := strings.ToLower(filepath.Ext(name))
	a.mu.RLock()
	iconMap := a.config.FileIcons
	a.mu.RUnlock()
	iconKey := ext
	if iconMap == nil {
		iconMap = defaultConfig().FileIcons
	}
	if ext == "" && strings.EqualFold(name, "Makefile") {
		iconKey = "Makefile"
	}
	icon := iconMap[iconKey].Icon
	if icon == "" {
		icon = iconMap["default"].Icon
	}
	switch icon {
	case "folder":
		return "📁"
	case "archive":
		return "📦"
	case "disc":
		return "💿"
	case "pdf":
		return "📕"
	case "code":
		return "💻"
	case "image":
		return "🖼️"
	case "data":
		return "📊"
	case "model":
		return "🧠"
	case "binary":
		return "⚙️"
	case "doc":
		return "📘"
	case "xls":
		return "📗"
	case "ppt":
		return "📙"
	case "text":
		return "📝"
	case "build":
		return "🔧"
	case "3d":
		return "🧊"
	case "package":
		return "📦"
	case "executable":
		return "⚡"
	default:
		return "📄"
	}
}

func (a *App) serveRawFile(w http.ResponseWriter, r *http.Request, rel string) {
	fullPath := filepath.Clean(filepath.Join(a.baseDir, filepath.FromSlash(rel)))
	if err := ensureSubPath(a.baseDir, fullPath); err != nil {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(fullPath)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, fullPath)
}

func sanitizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("名称不能为空")
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return "", errors.New("名称不能包含路径分隔符")
	}
	if name == "." || name == ".." {
		return "", errors.New("名称非法")
	}
	return name, nil
}

func ensureSubPath(baseDir, target string) error {
	baseDir = filepath.Clean(baseDir)
	target = filepath.Clean(target)
	if baseDir == target {
		return nil
	}
	if !strings.HasPrefix(target, baseDir+string(os.PathSeparator)) {
		return errors.New("路径越界")
	}
	return nil
}

func writeUploadedFile(targetPath string, src io.Reader) error {
	tmpPath := targetPath + ".upload"
	dst, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return os.Rename(tmpPath, targetPath)
}

func loadReadmeText(dirPath string) string {
	for _, name := range []string{"README.md", "readme.md", "Readme.md", "README", "readme"} {
		filePath := filepath.Join(dirPath, name)
		data, err := os.ReadFile(filePath)
		if err == nil && len(data) > 0 {
			return string(data)
		}
	}
	return ""
}

func buildBreadcrumbs(relPath string) []breadcrumbItem {
	if relPath == "" {
		return nil
	}
	parts := strings.Split(relPath, "/")
	items := make([]breadcrumbItem, 0, len(parts))
	current := ""
	for i, part := range parts {
		if current == "" {
			current = part
		} else {
			current += "/" + part
		}
		item := breadcrumbItem{
			Text:    part,
			Href:    "/" + escapeURLPath(current) + "/",
			Current: i == len(parts)-1,
		}
		items = append(items, item)
	}
	return items
}

func escapeURLPath(rel string) string {
	if rel == "" {
		return ""
	}
	parts := strings.Split(strings.ReplaceAll(rel, "\\", "/"), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func formatSize(size int64) string {
	if size < 0 {
		return "-"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(size)
	idx := 0
	for value >= 1024 && idx < len(units)-1 {
		value /= 1024
		idx++
	}
	if idx == 0 || value >= 10 {
		return fmt.Sprintf("%.0f %s", value, units[idx])
	}
	return fmt.Sprintf("%.1f %s", value, units[idx])
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
