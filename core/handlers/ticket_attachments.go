package handlers

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dylaris-core/models"
	"dylaris-core/storage"

	"github.com/gorilla/mux"
)

type TicketAttachmentsHandler struct {
	state    *AppState
	provider storage.StorageProvider
	scanner  AttachmentScanner
}

func NewTicketAttachmentsHandler(state *AppState) *TicketAttachmentsHandler {
	return &TicketAttachmentsHandler{
		state:    state,
		provider: buildAttachmentProvider(state),
		scanner:  newAttachmentScanner(),
	}
}

// buildAttachmentProvider builds the attachment-scoped provider from the
// shared Core file storage config (falls back to the legacy
// dylaris_data/ticket-attachments dir while the config is unset, so existing
// installs keep serving already-uploaded attachments).
func buildAttachmentProvider(state *AppState) storage.StorageProvider {
	p, err := state.buildCoreStorageProvider(CoreStoragePrefixAttachments)
	if err != nil {
		baseDir, _ := os.Getwd()
		root := filepath.Join(baseDir, "dylaris_data", CoreStoragePrefixAttachments)
		os.MkdirAll(root, 0755)
		return &storage.LocalProvider{BasePath: root}
	}
	return p
}

// allowedAttachmentExts maps an accepted lower-case file extension to the
// sniff family it must belong to. "text" means the sniffed type must be
// text/* or the generic octet-stream fallback (DetectContentType has no
// dedicated signature for plain text/log files); "application/" families
// accept any application/* sniff result (archives sniff loosely - see
// self-review note in task-7-report.md).
var allowedAttachmentExts = map[string]string{
	".png":  "image/",
	".jpg":  "image/",
	".jpeg": "image/",
	".gif":  "image/",
	".webp": "image/",
	".pdf":  "application/pdf",
	".txt":  "text",
	".log":  "text",
	".zip":  "application/zip",
	".gz":   "application/",
	".tar":  "application/",
	".json": "text",
}

// attachmentAllowed enforces the extension allowlist AND that the sniffed
// bytes do not contradict it (defence against a .png that is actually
// something else). The declared client MIME (the multipart part's
// Content-Type header) is advisory only and is never trusted on its own -
// only the extension and the http.DetectContentType sniff of the actual
// leading bytes decide the outcome.
func attachmentAllowed(filename, declaredMIME string, head []byte) error {
	ext := strings.ToLower(filepath.Ext(filename))
	family, ok := allowedAttachmentExts[ext]
	if !ok {
		return fmt.Errorf("file type %q is not allowed", ext)
	}
	sniff := http.DetectContentType(head) // e.g. "image/png; charset=..."
	// Reject anything the sniffer flags as a binary executable outright.
	// (Go's stdlib DetectContentType has no dedicated executable signature
	// today - kept as forward-compatible defence-in-depth; the actual
	// enforcement for a disguised executable is the family-mismatch check
	// below, since a genuine PE/ELF/Mach-O payload will not sniff as the
	// declared family either.)
	if strings.HasPrefix(sniff, "application/x-msdownload") ||
		strings.HasPrefix(sniff, "application/x-elf") ||
		strings.HasPrefix(sniff, "application/x-mach-binary") ||
		strings.HasPrefix(sniff, "application/x-executable") {
		return fmt.Errorf("file content looks like an executable and is not allowed")
	}
	switch {
	case family == "text":
		// DetectContentType returns "text/plain..." or "application/octet-stream"
		// for arbitrary text/logs; accept text/* and octet-stream, reject a
		// recognised non-text binary type sneaking in under a .txt/.log name.
		if strings.HasPrefix(sniff, "text/") || strings.HasPrefix(sniff, "application/octet-stream") {
			return nil
		}
		return fmt.Errorf("content does not match the %s file type", ext)
	case family == "application/":
		if strings.HasPrefix(sniff, "application/") || strings.HasPrefix(sniff, "text/plain") {
			return nil
		}
		return fmt.Errorf("content does not match the %s file type", ext)
	default:
		if strings.HasPrefix(sniff, family) {
			return nil
		}
		return fmt.Errorf("content does not match the declared %s type", ext)
	}
}

// AttachmentScanner is the optional AV hook. A nil scanner means no scanning
// (default). A hit (or any scan error - see newAttachmentScanner) returns a
// non-nil error.
type AttachmentScanner interface {
	Scan(r io.Reader) error
}

// newAttachmentScanner returns a clamd INSTREAM scanner when CLAMAV_ADDR is
// set, otherwise nil (scanning off by default; enabling it is a config
// flip, no code change). When nil, UploadAttachment skips the scan step
// entirely: no dial attempt, no added latency, no failure mode.
func newAttachmentScanner() AttachmentScanner {
	addr := strings.TrimSpace(os.Getenv("CLAMAV_ADDR"))
	if addr == "" {
		return nil
	}
	return &clamdScanner{addr: addr}
}

// clamdScanner streams an upload to clamd via the INSTREAM command and
// rejects on a virus hit. Kept small and dependency-free (raw TCP INSTREAM
// framing, no clamd client library).
//
// Fail-closed by design: Scan returns a non-nil error both on an actual
// virus hit AND on any infrastructure failure (dial timeout, write/read
// error, malformed reply). UploadAttachment treats every non-nil error from
// Scan identically - reject the upload. An operator who deliberately set
// CLAMAV_ADDR opted into scanning; if clamd is down we must not silently let
// unscanned uploads through.
type clamdScanner struct{ addr string }

func (c *clamdScanner) Scan(r io.Reader) error {
	conn, err := net.DialTimeout("tcp", c.addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("clamav dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return fmt.Errorf("clamav write cmd: %w", err)
	}
	buf := make([]byte, 32*1024)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			var sz [4]byte
			sz[0] = byte(n >> 24)
			sz[1] = byte(n >> 16)
			sz[2] = byte(n >> 8)
			sz[3] = byte(n)
			if _, err := conn.Write(sz[:]); err != nil {
				return fmt.Errorf("clamav chunk size: %w", err)
			}
			if _, err := conn.Write(buf[:n]); err != nil {
				return fmt.Errorf("clamav chunk: %w", err)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("clamav read source: %w", rerr)
		}
	}
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil { // zero-length ends stream
		return fmt.Errorf("clamav end stream: %w", err)
	}
	resp := make([]byte, 512)
	n, _ := conn.Read(resp)
	out := string(resp[:n])
	if strings.Contains(out, "FOUND") {
		return fmt.Errorf("attachment rejected by virus scan: %s", strings.TrimSpace(out))
	}
	return nil
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

	// Peek the first bytes for a content sniff, then rebuild the reader so the
	// full file still streams to storage - the sniff peek must never consume
	// bytes from what actually gets stored.
	head := make([]byte, 512)
	hn, _ := io.ReadFull(file, head)
	head = head[:hn]
	if err := attachmentAllowed(filename, mime, head); err != nil {
		sendJSONError(w, err.Error(), http.StatusUnsupportedMediaType)
		return
	}
	body := io.MultiReader(bytes.NewReader(head), file)

	// Optional AV scan: when a scanner is configured, spool to a temp file so
	// the whole object can be scanned before it lands in storage. Disabled
	// (h.scanner == nil) by default: no temp file, no dial, no latency.
	if h.scanner != nil {
		tmp, err := os.CreateTemp("", "dylaris-att-*")
		if err != nil {
			sendJSONError(w, "Failed to buffer upload", http.StatusInternalServerError)
			return
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()
		if _, err := io.Copy(tmp, body); err != nil {
			sendJSONError(w, "Failed to buffer upload", http.StatusInternalServerError)
			return
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			sendJSONError(w, "Failed to re-read upload", http.StatusInternalServerError)
			return
		}
		// Fail-closed: any non-nil error from Scan (a genuine hit OR an
		// infrastructure failure such as clamd being unreachable) rejects the
		// upload. See the clamdScanner doc comment for the rationale.
		if serr := h.scanner.Scan(tmp); serr != nil {
			sendJSONError(w, serr.Error(), http.StatusUnsupportedMediaType)
			return
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			sendJSONError(w, "Failed to re-read upload", http.StatusInternalServerError)
			return
		}
		body = tmp // stream the scanned temp file to storage
	}

	// Storage key: tickets/<id>/<random>-<filename>. Avoids collisions and
	// keeps per-ticket pruning trivial when the ticket gets hard-deleted.
	attachID := randomAttachmentID()
	storageKey := fmt.Sprintf("tickets/%d/%s-%s", t.ID, attachID, filename)
	if err := h.provider.WriteFile(storageKey, body); err != nil {
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
