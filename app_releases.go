package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	appsecurity "dladmin-go/internal/security"
	"dladmin-go/internal/store"
)

const maxReleaseUploadBytes = 20 << 30

func releaseRoleAllowed(session store.Session, roles ...string) bool {
	for _, role := range roles {
		if session.Role == role {
			return true
		}
	}
	return false
}

func requireReleaseRole(w http.ResponseWriter, r *http.Request, roles ...string) (store.Session, bool) {
	session, ok := sessionFromContext(r)
	if !ok || !releaseRoleAllowed(session, roles...) {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": "权限不足"})
		return store.Session{}, false
	}
	return session, true
}

func (a *App) releaseReady(w http.ResponseWriter) bool {
	if a.releases != nil {
		return true
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"success": false, "error": "发布服务未启用：" + a.releaseConfigErr,
	})
	return false
}

func (a *App) handleReleasesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := requireReleaseRole(w, r, "admin", "release_manager", "uploader", "auditor"); !ok {
		return
	}
	if !a.releaseReady(w) {
		return
	}
	releases, err := a.store.ListReleases(r.Context(), 200)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "读取发布记录失败"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "releases": releases})
}

func (a *App) handleReleaseUploadAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, ok := requireReleaseRole(w, r, "admin", "release_manager", "uploader")
	if !ok || !a.releaseReady(w) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxReleaseUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "发布集上传表单无效或超过 20 GiB"})
		return
	}
	files := r.MultipartForm.File["files"]
	if len(files) < 2 || len(files) > 129 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "发布集必须包含 release-set.json 和 1-128 个资产"})
		return
	}
	token, err := appsecurity.RandomToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "创建上传任务失败"})
		return
	}
	incomingID := time.Now().UTC().Format("20060102T150405Z") + "-web-" + token[:16]
	directory := filepath.Join(a.releases.IncomingDir(), incomingID)
	if err := os.Mkdir(directory, 0o750); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "创建上传暂存目录失败"})
		return
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(directory)
		}
	}()
	seen := make(map[string]bool, len(files))
	for _, header := range files {
		name, nameErr := sanitizeName(filepath.Base(strings.TrimSpace(header.Filename)))
		if nameErr != nil || name != header.Filename || seen[name] {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "发布集含有非法或重复文件名"})
			return
		}
		seen[name] = true
		source, openErr := header.Open()
		if openErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "读取上传文件失败"})
			return
		}
		writeErr := writeUploadedFile(filepath.Join(directory, name), source)
		_ = source.Close()
		if writeErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "保存上传文件失败"})
			return
		}
		_ = os.Chmod(filepath.Join(directory, name), 0o640)
	}
	if !seen["release-set.json"] {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "发布集缺少 release-set.json"})
		return
	}
	record, err := a.releases.ImportIncoming(r.Context(), incomingID, session.ID)
	if err != nil {
		a.audit(r, &session, "upload", "release", incomingID, "failure", jsonDetail("error", err.Error()))
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "发布集校验失败：" + err.Error()})
		return
	}
	keep = true
	a.audit(r, &session, "stage", "release", strconv.FormatInt(record.ID, 10), "success", jsonDetail("version", record.Version))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "release": record, "message": "签名与哈希校验通过，发布集已暂存"})
}

func (a *App) handleReleaseImportAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, ok := requireReleaseRole(w, r, "admin", "release_manager")
	if !ok || !a.releaseReady(w) {
		return
	}
	var payload struct {
		IncomingID string `json:"incoming_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "请求数据无效"})
		return
	}
	record, err := a.releases.ImportIncoming(r.Context(), payload.IncomingID, session.ID)
	if err != nil {
		a.audit(r, &session, "import", "release", payload.IncomingID, "failure", jsonDetail("error", err.Error()))
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "导入失败：" + err.Error()})
		return
	}
	a.audit(r, &session, "stage", "release", strconv.FormatInt(record.ID, 10), "success", jsonDetail("version", record.Version))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "release": record})
}

func (a *App) handleReleasePublishAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, ok := requireReleaseRole(w, r, "admin", "release_manager")
	if !ok || !a.releaseReady(w) {
		return
	}
	var payload struct {
		ID int64 `json:"id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload) != nil || payload.ID < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "发布记录 ID 无效"})
		return
	}
	record, err := a.releases.Publish(r.Context(), payload.ID)
	if err != nil {
		a.audit(r, &session, "publish", "release", strconv.FormatInt(payload.ID, 10), "failure", jsonDetail("error", err.Error()))
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "发布失败：" + err.Error()})
		return
	}
	a.audit(r, &session, "publish", "release", strconv.FormatInt(record.ID, 10), "success", jsonDetail("version", record.Version))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "release": record, "message": "版本已原子发布"})
}

func (a *App) handleUpdateAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.releases == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"available": false, "error": "update service unavailable"})
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/updates/"), "/"), "/")
	if len(parts) != 4 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"available": false, "error": "expected product/channel/target/current-version"})
		return
	}
	for index := range parts {
		decoded, err := filepath.Rel(".", filepath.Clean(parts[index]))
		if err != nil || decoded != parts[index] || strings.ContainsAny(parts[index], `\\`) || parts[index] == "." || parts[index] == ".." {
			writeJSON(w, http.StatusBadRequest, map[string]any{"available": false, "error": "invalid update route"})
			return
		}
	}
	update, err := a.releases.SelectUpdate(r.Context(), parts[0], parts[1], parts[2], parts[3])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"available": false, "error": err.Error()})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, update)
}

func (a *App) handleTauriUpdateAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.releases == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "update service unavailable"})
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/tauri/"), "/"), "/")
	if len(parts) != 4 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "expected product/channel/target/current-version"})
		return
	}
	updateMode := strings.TrimSpace(r.Header.Get("X-Update-Mode"))
	if updateMode == "" {
		// Compatibility for LYNX clients released before the product-neutral API.
		updateMode = strings.TrimSpace(r.Header.Get("X-Lynx-Update-Mode"))
	}
	forceFull := strings.EqualFold(updateMode, "full")
	update, err := a.releases.SelectUpdate(r.Context(), parts[0], parts[1], parts[2], parts[3])
	if forceFull {
		update, err = a.releases.SelectFullUpdate(r.Context(), parts[0], parts[1], parts[2], parts[3])
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "X-Update-Mode, X-Lynx-Update-Mode")
	if !update.Available {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":            update.Version,
		"notes":              update.Notes,
		"pub_date":           update.PublishedAt,
		"url":                a.releaseBaseURL + update.Asset.URL,
		"signature":          update.Asset.Signature,
		"asset_size":         update.Asset.Size,
		"asset_sha256":       update.Asset.SHA256,
		"strategy":           update.Strategy,
		"fallback_available": update.Fallback != nil,
	})
}

func jsonDetail(key, value string) string {
	data, err := json.Marshal(map[string]string{key: value})
	if err != nil {
		return `{}`
	}
	return string(data)
}
