package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dylaris-core/models"
	"dylaris-core/storage"

	"github.com/gorilla/mux"
)

type TicketAttachmentsHandler struct {
	state    *AppState
	provider storage.StorageProvider
}

func NewTicketAttachmentsHandler(state *AppState) *TicketAttachmentsHandler {
	return &TicketAttachmentsHandler{
		state:    state,
		provider: buildAttachmentProvider(),
	}
}

// buildAttachmentProvider sets up the storage backend for ticket attachments.
// Lives next to the library under dylaris_data/ticket-attachments so the two
// don't co-mingle. Always LocalProvider for now; later phases can wire S3 via
// settings the same way library does.
func buildAttachmentProvider() storage.StorageProvider {
	baseDir, _ := os.Getwd()
	root := filepath.Join(baseDir, "dylaris_data", "ticket-attachments")
	os.MkdirAll(root, 0755)
	p, err := storage.NewProvider("path", root, nil)
	if err != nil {
		p = &storage.LocalProvider{BasePath: root}
	}
	return p
}

// ── Helpers ──────────────────────────────────────────────────────────

// canAttach gates upload + delete. Mirrors canReply from tickets.go but
// without the "user reply → may not internal" carve-out — internal-ness
// belongs to the message, not the attachment.
func (h *TicketAttachmentsHandler) canAttach(t *models.Ticket, perms EffectivePermissions, userID string, isWatcher, watcherCanReply bool) bool {
	if perms.IsAdmin || perms.IsSupport {
		return true
	}
	if t.UserID == userID {
		return true
	}
	if isWatcher && watcherCanReply {
		return true
	}
	return false
}

func (h *TicketAttachmentsHandler) loadTicketAndGate(w http.ResponseWriter, r *http.Request) (*models.Ticket, EffectivePermissions, string, bool) {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil || id <= 0 {
		sendJSONError(w, "Invalid ticket id", http.StatusBadRequest)
		return nil, EffectivePermissions{}, "", false
	}
	t, err := h.state.Store.GetTicket(id)
	if err != nil || t == nil {
		sendJSONError(w, "Ticket not found", http.StatusNotFound)
		return nil, EffectivePermissions{}, "", false
	}
	userID, _ := r.Context().Value("userID").(string)
	perms := LoadEffectivePermissions(h.state, userID)
	return t, perms, userID, true
}

// randomAttachmentID returns 16 hex chars. Used as part of the storage key
// so two uploads with the same filename don't collide.
func randomAttachmentID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// sanitizeAttachmentFilename strips path separators and trims to a sensible
// length. Kept separate from the file-handler's sanitizeFilename helper to
// avoid coupling two unrelated subsystems through a shared identifier.
func sanitizeAttachmentFilename(in string) string {
	in = strings.TrimSpace(in)
	in = strings.ReplaceAll(in, "/", "_")
	in = strings.ReplaceAll(in, "\\", "_")
	in = strings.ReplaceAll(in, "..", "_")
	if len(in) > 200 {
		in = in[:200]
	}
	if in == "" {
		in = "file"
	}
	return in
}

// ── Endpoints ────────────────────────────────────────────────────────

// UploadAttachment POST /api/tickets/{id}/attachments
// Multipart with field name "file". Optional form field "messageId" links
// the attachment to a specific message (useful when posting a new reply
// with attachments — frontend posts message first, then attaches by id).
func (h *TicketAttachmentsHandler) UploadAttachment(w http.ResponseWriter, r *http.Request) {
	t, perms, userID, ok := h.loadTicketAndGate(w, r)
	if !ok {
		return
	}
	isWatcher, _ := h.state.Store.IsTicketWatcher(t.ID, userID)
	watcherCanReply := false
	if isWatcher {
		watchers, _ := h.state.Store.ListTicketWatchers(t.ID)
		for _, w := range watchers {
			if w.UserID == userID {
				watcherCanReply = w.CanReply
				break
			}
		}
	}
	if !h.canAttach(t, perms, userID, isWatcher, watcherCanReply) {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}

	settings := LoadTicketSettings(h.state)

	// Multipart parse with a generous max-memory; the file is streamed to
	// disk regardless.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		sendJSONError(w, "Failed to parse upload", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		sendJSONError(w, "Missing 'file' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	filename := sanitizeAttachmentFilename(header.Filename)
	mime := header.Header.Get("Content-Type")
	if mime == "" {
		mime = "application/octet-stream"
	}
	size := header.Size

	// Quota checks before writing.
	maxFile := int64(settings.MaxFileSizeMB) * 1024 * 1024
	if maxFile > 0 && size > maxFile {
		sendJSONError(w, fmt.Sprintf("File exceeds the %d MB per-file limit", settings.MaxFileSizeMB), http.StatusRequestEntityTooLarge)
		return
	}
	maxTicket := int64(settings.MaxTicketSizeMB) * 1024 * 1024
	if maxTicket > 0 {
		current, _ := h.state.Store.SumAttachmentBytesByTicket(t.ID)
		if current+size > maxTicket {
			sendJSONError(w, fmt.Sprintf("Adding this file would exceed the %d MB per-ticket limit", settings.MaxTicketSizeMB), http.StatusRequestEntityTooLarge)
			return
		}
	}
	maxUser := int64(settings.MaxUserSizeMB) * 1024 * 1024
	if maxUser > 0 {
		current, _ := h.state.Store.SumAttachmentBytesByUser(userID)
		if current+size > maxUser {
			sendJSONError(w, fmt.Sprintf("Adding this file would exceed your %d MB attachment quota", settings.MaxUserSizeMB), http.StatusRequestEntityTooLarge)
			return
		}
	}

	// Storage key: tickets/<id>/<random>-<filename>. Avoids collisions and
	// keeps per-ticket pruning trivial when the ticket gets hard-deleted.
	attachID := randomAttachmentID()
	storageKey := fmt.Sprintf("tickets/%d/%s-%s", t.ID, attachID, filename)
	if err := h.provider.WriteFile(storageKey, file); err != nil {
		sendJSONError(w, "Failed to store file", http.StatusInternalServerError)
		return
	}

	// Optional message link from form.
	var msgID *int
	if v := strings.TrimSpace(r.FormValue("messageId")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			msgID = &n
		}
	}

	uid := userID
	a := &models.TicketAttachment{
		TicketID:   t.ID,
		MessageID:  msgID,
		Filename:   filename,
		Mime:       mime,
		SizeBytes:  size,
		StorageKey: storageKey,
		UploadedBy: &uid,
	}
	insertedID, err := h.state.Store.AddTicketAttachment(a)
	if err != nil {
		// Best-effort cleanup if the DB rejected after the file landed.
		_ = h.provider.DeletePath(storageKey)
		sendJSONError(w, "Failed to persist metadata", http.StatusInternalServerError)
		return
	}
	a.ID = insertedID

	aid := userID
	_ = h.state.Store.InsertTicketAudit(&models.TicketAuditEvent{
		TicketID:    t.ID,
		EventType:   TicketEventAttachmentAdded,
		ActorUserID: &aid,
		Metadata: map[string]interface{}{
			"filename": filename,
			"size":     size,
		},
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"attachment": a,
	})
}

// ListAttachments GET /api/tickets/{id}/attachments
func (h *TicketAttachmentsHandler) ListAttachments(w http.ResponseWriter, r *http.Request) {
	t, perms, userID, ok := h.loadTicketAndGate(w, r)
	if !ok {
		return
	}
	settings := LoadTicketSettings(h.state)
	isWatcher, _ := h.state.Store.IsTicketWatcher(t.ID, userID)
	myTeam := ""
	if me, err := h.state.Store.GetUserByID(userID); err == nil && me != nil {
		myTeam = me.SupportTeam
	}
	if !canSeeTicket(t, perms, userID, isWatcher, settings, myTeam) {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	atts, err := h.state.Store.ListTicketAttachments(t.ID)
	if err != nil {
		sendJSONError(w, "Failed to load attachments", http.StatusInternalServerError)
		return
	}
	if atts == nil {
		atts = []models.TicketAttachment{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"attachments": atts,
	})
}

// DownloadAttachment GET /api/tickets/{id}/attachments/{aid}/download
func (h *TicketAttachmentsHandler) DownloadAttachment(w http.ResponseWriter, r *http.Request) {
	t, perms, userID, ok := h.loadTicketAndGate(w, r)
	if !ok {
		return
	}
	settings := LoadTicketSettings(h.state)
	isWatcher, _ := h.state.Store.IsTicketWatcher(t.ID, userID)
	myTeam := ""
	if me, err := h.state.Store.GetUserByID(userID); err == nil && me != nil {
		myTeam = me.SupportTeam
	}
	if !canSeeTicket(t, perms, userID, isWatcher, settings, myTeam) {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	aid, err := strconv.Atoi(mux.Vars(r)["aid"])
	if err != nil || aid <= 0 {
		sendJSONError(w, "Invalid attachment id", http.StatusBadRequest)
		return
	}
	a, err := h.state.Store.GetTicketAttachment(aid)
	if err != nil || a == nil || a.TicketID != t.ID {
		sendJSONError(w, "Attachment not found", http.StatusNotFound)
		return
	}
	rc, err := h.provider.GetFile(a.StorageKey)
	if err != nil {
		sendJSONError(w, "File missing on storage", http.StatusGone)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", a.Mime)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", a.SizeBytes))
	w.Header().Set("Content-Disposition", `attachment; filename="`+a.Filename+`"`)
	io.Copy(w, rc)
}

// DeleteAttachment DELETE /api/tickets/{id}/attachments/{aid}
// Uploader OR support/admin OR ticket owner can delete.
func (h *TicketAttachmentsHandler) DeleteAttachment(w http.ResponseWriter, r *http.Request) {
	t, perms, userID, ok := h.loadTicketAndGate(w, r)
	if !ok {
		return
	}
	aid, _ := strconv.Atoi(mux.Vars(r)["aid"])
	a, err := h.state.Store.GetTicketAttachment(aid)
	if err != nil || a == nil || a.TicketID != t.ID {
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	allowed := perms.IsAdmin || perms.IsSupport || t.UserID == userID
	if !allowed && a.UploadedBy != nil && *a.UploadedBy == userID {
		allowed = true
	}
	if !allowed {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	if err := h.state.Store.DeleteTicketAttachment(aid); err != nil {
		sendJSONError(w, "Delete failed", http.StatusInternalServerError)
		return
	}
	_ = h.provider.DeletePath(a.StorageKey)
	_ = h.state.Store.InsertTicketAudit(&models.TicketAuditEvent{
		TicketID:    t.ID,
		EventType:   TicketEventAttachmentRemoved,
		ActorUserID: &userID,
		Metadata: map[string]interface{}{
			"filename": a.Filename,
		},
	})
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
