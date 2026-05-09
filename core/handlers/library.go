package handlers

import (
	"dylaris-core/storage"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type LibraryHandler struct {
	state    *AppState
	provider storage.StorageProvider
}

func NewLibraryHandler(state *AppState) *LibraryHandler {
	return &LibraryHandler{state: state, provider: buildProvider(state)}
}

// buildProvider reads the library configuration from the DB and creates the appropriate provider
func buildProvider(state *AppState) storage.StorageProvider {
	libType := ""
	libPath := ""

	if state.Store != nil {
		libType, _ = state.Store.GetSetting("library_type")
		libPath, _ = state.Store.GetSetting("library_path")
	}

	if libPath == "" {
		// Default: dylaris_data/library next to the working directory
		baseDir, _ := os.Getwd()
		libPath = filepath.Join(baseDir, "dylaris_data", "library")
	}

	os.MkdirAll(libPath, 0755)
	return storage.NewProvider(libType, libPath, nil)
}

// RefreshProvider is called when the library settings are changed
func (h *LibraryHandler) RefreshProvider() {
	h.provider = buildProvider(h.state)
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

	files, err := h.provider.ListFiles(path)
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

// ToggleLibraryPathHandler POST /api/library/toggle (Admin only)
// Body: { "path": "...", "enabled": false }
func (h *LibraryHandler) ToggleLibraryPathHandler(w http.ResponseWriter, r *http.Request) {
	isAdmin, _ := r.Context().Value("isAdmin").(bool)
	if !isAdmin {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
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

// DeleteLibraryHandler POST /api/library/delete (Admin only)
func (h *LibraryHandler) DeleteLibraryHandler(w http.ResponseWriter, r *http.Request) {
	isAdmin := r.Context().Value("isAdmin").(bool)
	if !isAdmin {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := h.provider.DeletePath(req.Path); err != nil {
		sendJSONError(w, "Delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// MkdirLibraryHandler POST /api/library/mkdir (Admin only)
func (h *LibraryHandler) MkdirLibraryHandler(w http.ResponseWriter, r *http.Request) {
	isAdmin := r.Context().Value("isAdmin").(bool)
	if !isAdmin {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := h.provider.CreateDir(req.Path); err != nil {
		sendJSONError(w, "Create dir failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// UploadLibraryHandler POST /api/library/upload (Admin only)
func (h *LibraryHandler) UploadLibraryHandler(w http.ResponseWriter, r *http.Request) {
	isAdmin := r.Context().Value("isAdmin").(bool)
	if !isAdmin {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

	if err := r.ParseMultipartForm(2 << 30); err != nil { // 2GB
		sendJSONError(w, "File too large", http.StatusBadRequest)
		return
	}

	path := r.FormValue("path")
	if path == "" {
		path = "/"
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

		if err := h.provider.WriteFile(destPath, f); err != nil {
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

	rc, err := h.provider.GetFile(path)
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
