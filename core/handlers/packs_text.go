package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"

	"dylaris-core/models"
	"dylaris-core/storage/modpack"

	"github.com/gorilla/mux"
)

// maxEditableTextBytes bounds how much text the in-panel editor will load/save.
const maxEditableTextBytes = 1 << 20 // 1 MiB

// loadOwnedContentModversion resolves the ownership-checked build, the
// modversion (verified attached to that build, not a foreign id), and the
// storage provider. It does NOT check Frozen (callers gate writes on it).
func (h *PacksHandler) loadOwnedContentModversion(r *http.Request) (*models.PackBuild, *models.Modversion, modpack.ModpackStorageProvider, bool) {
	b, ok := h.loadOwnedBuild(r)
	if !ok {
		return nil, nil, nil, false
	}
	mvID, _ := strconv.Atoi(mux.Vars(r)["modversionId"])
	if ok, _ := h.state.Store.IsModversionInBuild(b.ID, mvID); !ok {
		return nil, nil, nil, false
	}
	mv, err := h.state.Store.GetModversion(mvID)
	if err != nil || mv == nil {
		return nil, nil, nil, false
	}
	prov, err := modpack.NewProviderFromSettings(h.state.Store.GetSetting)
	if err != nil || prov == nil {
		return nil, nil, nil, false
	}
	return b, mv, prov, true
}

// GetContentText returns the UTF-8 text of a single stored content entry (e.g.
// a config file) so the panel can edit it in place. Modrinth-reference /
// non-text / oversized entries are refused.
func (h *PacksHandler) GetContentText(w http.ResponseWriter, r *http.Request) {
	_, mv, prov, ok := h.loadOwnedContentModversion(r)
	if !ok {
		sendJSONError(w, "Content not found", http.StatusNotFound)
		return
	}
	if mv.StorageKey == "" {
		sendJSONError(w, "This content has no editable file (Modrinth reference)", http.StatusUnprocessableEntity)
		return
	}
	raw, err := prov.Get(mv.StorageKey)
	if err != nil {
		sendJSONError(w, "Failed to read content", http.StatusInternalServerError)
		return
	}
	data, found := modpack.ReadZipEntry(raw, mv.TargetPath)
	if !found {
		sendJSONError(w, "No editable file at the content path", http.StatusUnprocessableEntity)
		return
	}
	if len(data) > maxEditableTextBytes || !utf8.Valid(data) {
		sendJSONError(w, "Content is not editable text", http.StatusUnsupportedMediaType)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "text": string(data), "targetPath": mv.TargetPath})
}

type setContentTextRequest struct {
	Text string `json:"text"`
}

// SetContentText overwrites a stored text content entry with edited bytes,
// re-wrapping into the Solder zip at the same key and re-hashing. Editing an
// entry that was Modrinth-linked unlinks it (the file now diverges from the
// upstream version).
func (h *PacksHandler) SetContentText(w http.ResponseWriter, r *http.Request) {
	b, mv, prov, ok := h.loadOwnedContentModversion(r)
	if !ok {
		sendJSONError(w, "Content not found", http.StatusNotFound)
		return
	}
	if b.Frozen {
		sendJSONError(w, "Build is frozen", http.StatusConflict)
		return
	}
	if mv.StorageKey == "" {
		sendJSONError(w, "This content has no editable file (Modrinth reference)", http.StatusUnprocessableEntity)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxEditableTextBytes+1))
	if err != nil {
		sendJSONError(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	var req setContentTextRequest
	if err := json.Unmarshal(body, &req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	newBytes := []byte(req.Text)
	if len(newBytes) > maxEditableTextBytes {
		sendJSONError(w, "Text too large", http.StatusRequestEntityTooLarge)
		return
	}
	zipBytes, err := modpack.BuildSolderContentZip(mv.TargetPath, newBytes)
	if err != nil {
		sendJSONError(w, "Failed to wrap content", http.StatusInternalServerError)
		return
	}
	if err := prov.Put(mv.StorageKey, zipBytes); err != nil {
		sendJSONError(w, "Storage put failed", http.StatusInternalServerError)
		return
	}
	md5hex, _, _ := modpack.Hashes(zipBytes)
	_, innerSha1, innerSha512 := modpack.Hashes(newBytes)
	mv.Filesize = int64(len(zipBytes))
	mv.MD5 = md5hex
	mv.SHA1 = innerSha1
	mv.SHA512 = innerSha512
	// An edited file diverges from its upstream Modrinth version -> unlink.
	mv.Source = models.SourceUpload
	mv.ModrinthProjectID = ""
	mv.ModrinthVersionID = ""
	mv.ModrinthVersionNumber = ""
	mv.ModrinthGameVersions = ""
	mv.ModrinthDownloadURL = ""
	mv.ModrinthLatestVersionID = ""
	mv.ModrinthLastChecked = nil
	if err := h.state.Store.UpdateModversion(mv); err != nil {
		sendJSONError(w, "Failed to save content", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "pack_content.changed", map[string]interface{}{"buildId": b.ID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
