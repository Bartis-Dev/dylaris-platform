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

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"files":   files,
	})
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
