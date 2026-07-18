package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type LibraryHandler struct {
	state *AppState
}

func NewLibraryHandler(state *AppState) *LibraryHandler {
	return &LibraryHandler{state: state}
}

// GetLibraryHandler GET /api/library?path=
//
// Admins see all entries with their `enabled` flag set per-path so the UI can
// render a toggle. Non-admins:
//   - get an empty list when the requested path itself OR any ancestor is
//     marked disabled (strict AND semantics — "all the way up must be on")
//   - have disabled children silently filtered out of the listing
func (h *LibraryHandler) GetLibraryHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}

	prov, err := h.state.buildCoreStorageProvider(CoreStoragePrefixLibrary)
	if err != nil {
		coreStorageUnavailableResponse(w)
		return
	}

	files, err := prov.ListFiles(path)
	if err != nil {
		sendJSONError(w, "Could not list library files: "+err.Error(), http.StatusInternalServerError)
		return
	}

	disabledSet := h.disabledPathSet()
	isAdmin, _ := r.Context().Value("isAdmin").(bool)

	if !isAdmin && isPathBlocked(path, disabledSet) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"files":   []interface{}{},
		})
		return
	}

	out := files[:0]
	for _, f := range files {
		full := joinLibraryPath(path, f.Name)
		_, isDisabled := disabledSet[normalizeLibraryPath(full)]
		if isDisabled {
			if !isAdmin {
				continue
			}
			f.Enabled = false
		}
		out = append(out, f)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"files":   out,
	})
}

// ToggleLibraryPathHandler POST /api/library/toggle
// Body: { "path": "...", "enabled": false }
//
// Route-gated by RequireCap("settings.write") (Phase 4 Task 20); the former
// in-handler `if !isAdmin` block is now the chokepoint's job.
func (h *LibraryHandler) ToggleLibraryPathHandler(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Path    string `json:"path"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.Path = normalizeLibraryPath(req.Path)
	if req.Path == "" || req.Path == "/" {
		sendJSONError(w, "Cannot toggle the library root", http.StatusBadRequest)
		return
	}

	if err := h.state.Store.SetLibraryPathDisabled(req.Path, !req.Enabled); err != nil {
		sendJSONError(w, "Toggle failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// disabledPathSet loads all disabled library paths into a lookup set,
// keyed by the normalized path string.
func (h *LibraryHandler) disabledPathSet() map[string]struct{} {
	set := map[string]struct{}{}
	if h.state.Store == nil {
		return set
	}
	paths, err := h.state.Store.ListDisabledLibraryPaths()
	if err != nil {
		return set
	}
	for _, p := range paths {
		set[normalizeLibraryPath(p)] = struct{}{}
	}
	return set
}

// normalizeLibraryPath strips leading/trailing slashes so paths from
// different sources (admin clicks, listing joins, DB rows) compare equal.
func normalizeLibraryPath(p string) string {
	p = strings.TrimSpace(p)
	for strings.HasPrefix(p, "/") {
		p = p[1:]
	}
	for strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	return p
}

// joinLibraryPath joins a parent listing path with a child entry name,
// producing a normalized library-relative path.
func joinLibraryPath(parent, name string) string {
	parent = normalizeLibraryPath(parent)
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

// isPathBlocked returns true when `path` itself or any ancestor (excluding
// root) is in the disabled set — meaning a non-admin should see nothing here.
func isPathBlocked(path string, disabled map[string]struct{}) bool {
	cur := normalizeLibraryPath(path)
	if cur == "" {
		return false // root itself is never blockable (toggle handler refuses it)
	}
	for cur != "" {
		if _, ok := disabled[cur]; ok {
			return true
		}
		idx := strings.LastIndex(cur, "/")
		if idx < 0 {
			break
		}
		cur = cur[:idx]
	}
	return false
}

// DeleteLibraryHandler POST /api/library/delete
//
// Route-gated by RequireCap("settings.write") (Phase 4 Task 20); the former
// in-handler `if !isAdmin` block is now the chokepoint's job.
func (h *LibraryHandler) DeleteLibraryHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	prov, err := h.state.buildCoreStorageProvider(CoreStoragePrefixLibrary)
	if err != nil {
		coreStorageUnavailableResponse(w)
		return
	}
	if err := prov.DeletePath(req.Path); err != nil {
		sendJSONError(w, "Delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// MkdirLibraryHandler POST /api/library/mkdir
//
// Route-gated by RequireCap("settings.write") (Phase 4 Task 20); the former
// in-handler `if !isAdmin` block is now the chokepoint's job.
func (h *LibraryHandler) MkdirLibraryHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	prov, err := h.state.buildCoreStorageProvider(CoreStoragePrefixLibrary)
	if err != nil {
		coreStorageUnavailableResponse(w)
		return
	}
	if err := prov.CreateDir(req.Path); err != nil {
		sendJSONError(w, "Create dir failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// UploadLibraryHandler POST /api/library/upload
//
// Route-gated by RequireCap("settings.write") (Phase 4 Task 20); the former
// in-handler `if !isAdmin` block is now the chokepoint's job.
func (h *LibraryHandler) UploadLibraryHandler(w http.ResponseWriter, r *http.Request) {
	// 32MiB in memory; larger files spill to a temp file on disk. Passing 2GiB
	// here would buffer the whole upload in RAM.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		sendJSONError(w, "File too large", http.StatusBadRequest)
		return
	}

	path := r.FormValue("path")
	if path == "" {
		path = "/"
	}
	if strings.Contains(path, "..") {
		sendJSONError(w, "Invalid path", http.StatusBadRequest)
		return
	}

	prov, err := h.state.buildCoreStorageProvider(CoreStoragePrefixLibrary)
	if err != nil {
		coreStorageUnavailableResponse(w)
		return
	}

	files := r.MultipartForm.File["files"]
	for _, fh := range files {
		cleanName := sanitizeFilename(fh.Filename)
		if cleanName == "" {
			continue
		}
		destPath := strings.TrimSuffix(path, "/") + "/" + cleanName

		f, err := fh.Open()
		if err != nil {
			sendJSONError(w, "Could not open file", http.StatusInternalServerError)
			return
		}
		defer f.Close()

		if err := prov.WriteFile(destPath, f); err != nil {
			sendJSONError(w, fmt.Sprintf("Upload failed for %s: %v", cleanName, err), http.StatusInternalServerError)
			return
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("%d files uploaded", len(files)),
	})
}

// DownloadLibraryHandler GET /api/library/download?path=
func (h *LibraryHandler) DownloadLibraryHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		sendJSONError(w, "Path required", http.StatusBadRequest)
		return
	}
	if strings.Contains(path, "..") {
		sendJSONError(w, "Invalid path", http.StatusBadRequest)
		return
	}

	// Mirror the browse handler's access model: a non-admin must not download
	// a file that lives under an admin-disabled path (the browse listing hides
	// these, but download took the path verbatim).
	if isAdmin, _ := r.Context().Value("isAdmin").(bool); !isAdmin {
		disabledSet := h.disabledPathSet()
		if _, blocked := disabledSet[normalizeLibraryPath(path)]; blocked || isPathBlocked(path, disabledSet) {
			sendJSONError(w, "Access denied", http.StatusForbidden)
			return
		}
	}

	prov, err := h.state.buildCoreStorageProvider(CoreStoragePrefixLibrary)
	if err != nil {
		coreStorageUnavailableResponse(w)
		return
	}

	// Prefer a short-lived pre-signed URL when the backend supports it (S3):
	// redirect the browser straight to object storage instead of streaming
	// every byte through Core.
	if url, err := prov.DownloadURL(path, 5*time.Minute); err == nil && url != "" {
		http.Redirect(w, r, url, http.StatusFound)
		return
	}

	rc, err := prov.GetFile(path)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	defer rc.Close()

	filename := filepath.Base(path)
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	w.Header().Set("Content-Type", "application/octet-stream")

	buf := make([]byte, 32*1024)
	for {
		n, err := rc.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
}
