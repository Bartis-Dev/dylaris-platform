package handlers

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
// validation family attachmentAllowed applies to it:
//   - "image/", "application/pdf", "application/zip" - tight families: the
//     http.DetectContentType sniff of the real bytes must start with exactly
//     that string.
//   - "text" - the sniff must start with "text/" and must NOT be "text/html"
//     (an HTML/script body is not what a .txt/.log/.json extension means).
//   - "gzip" - the peeked head must start with the real gzip magic
//     (\x1F\x8B), checked on the raw bytes rather than the sniff string.
//   - "tar" - the peeked head must carry the real POSIX ustar magic
//     ("ustar") at its fixed offset (257), checked on the raw bytes. A sniff
//     family check alone cannot do this job: Go's DetectContentType has no
//     tar signature, so a genuine tar entry AND an ELF/PE payload both sniff
//     as the same application/octet-stream - only the actual header magic
//     tells them apart.
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
	".gz":   "gzip",
	".tar":  "tar",
	".json": "text",
}

// attachmentAllowed enforces the extension allowlist AND that the sniffed
// bytes actually belong to the family that extension requires (defence
// against a .png that is actually something else). It returns the sniffed
// content type on success so the caller can persist and later serve THAT
// value rather than the client's claim.
//
// The client's declared MIME (the multipart part's Content-Type header) is
// deliberately not a parameter here and is not consulted anywhere in this
// file: browsers and upload clients routinely send an inaccurate
// Content-Type (e.g. plain "application/octet-stream" for a real PNG), so
// trusting - or even cross-checking against - that value would either
// falsely reject legitimate uploads or let an attacker's own claim stand in
// as ground truth. Only the extension and http.DetectContentType's sniff of
// the actual leading bytes decide the outcome.
func attachmentAllowed(filename string, head []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	family, ok := allowedAttachmentExts[ext]
	if !ok {
		return "", fmt.Errorf("file type %q is not allowed", ext)
	}
	sniff := http.DetectContentType(head) // e.g. "image/png" or "text/plain; charset=utf-8"

	switch family {
	case "text":
		// text/* only - the previous application/octet-stream escape hatch
		// let a raw ELF/PE/Mach-O payload named payload.txt/.log/.json
		// through, since Go's sniffer has no executable signature and falls
		// back to octet-stream for arbitrary binary content. text/html is
		// excluded too, so an HTML/script body uploaded as notes.txt is
		// rejected rather than stored and later served back to a browser.
		if !strings.HasPrefix(sniff, "text/") || strings.HasPrefix(sniff, "text/html") {
			return "", fmt.Errorf("content does not match the %s file type", ext)
		}
		return sniff, nil
	case "gzip":
		// Real gzip magic on the raw bytes, not a loose sniff-string check -
		// the previous "application/* or text/plain" rule accepted a shell
		// script as payload.gz.
		if len(head) < 2 || head[0] != 0x1F || head[1] != 0x8B {
			return "", fmt.Errorf("content does not match the %s file type", ext)
		}
		return sniff, nil
	case "tar":
		// The ustar magic sits at a fixed 257-byte offset within the first
		// (and, for any archive whose first entry uses a full POSIX/GNU
		// header block, only) 512-byte block - well within our 512-byte
		// peek. A pre-POSIX (legacy V7) tar without this magic, or anything
		// shorter than the offset, is rejected: correctness of the check
		// (catching a disguised ELF/PE) takes priority over accepting every
		// historical tar variant.
		const ustarOffset = 257
		if len(head) < ustarOffset+5 || string(head[ustarOffset:ustarOffset+5]) != "ustar" {
			return "", fmt.Errorf("content does not match the %s file type", ext)
		}
		return sniff, nil
	default:
		// Tight families: image/*, application/pdf, application/zip.
		if strings.HasPrefix(sniff, family) {
			return sniff, nil
		}
		return "", fmt.Errorf("content does not match the declared %s type", ext)
	}
}

// AttachmentScanner is the optional AV hook. A nil scanner means no scanning
// (default). A hit (or any scan error - see newAttachmentScanner) returns a
// non-nil error. A genuine virus hit wraps errScanHit (via errors.Is); any
// other error signals an infrastructure failure (dial/read/write/malformed
// reply) - UploadAttachment rejects the upload in both cases (fail-closed)
// but returns a different HTTP status for each.
type AttachmentScanner interface {
	Scan(r io.Reader) error
}

// errScanHit is the sentinel a scanner wraps into its returned error when it
// reports an actual virus hit (as opposed to being unable to complete the
// scan at all). Used with errors.Is so UploadAttachment can return 415 for a
// real hit and 503 for a scanner/infrastructure problem, while still
// rejecting the upload in both cases.
var errScanHit = errors.New("attachment rejected by virus scan")

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
		return fmt.Errorf("%w: %s", errScanHit, strings.TrimSpace(out))
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

	// The multipart part's declared Content-Type header is deliberately never
	// read here: it is neither validated against nor persisted (see the
	// attachmentAllowed doc comment) - only the extension and the sniffed
	// bytes decide what gets accepted and what gets stored as the Mime.
	filename := sanitizeAttachmentFilename(header.Filename)
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
	hn, err := io.ReadFull(file, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		// A short file (fewer than 512 bytes) legitimately yields EOF or
		// ErrUnexpectedEOF - that's fine, hn still holds however many bytes
		// were read. Any other error is a genuine read failure; continuing
		// would sniff an empty/truncated head as text/plain and could let
		// content through on zero observed bytes.
		log.Printf("ticket-attachments: peek read for ticket %d failed: %v", t.ID, err)
		sendJSONError(w, "Failed to read upload", http.StatusInternalServerError)
		return
	}
	head = head[:hn]
	sniffedMime, err := attachmentAllowed(filename, head)
	if err != nil {
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
		// upload either way. See the clamdScanner doc comment for the
		// rationale. The HTTP status and message differ though: a real hit is
		// a client-content problem (415), an infra failure is a server-side
		// condition (503) - and the raw error (which can contain internal
		// host/port details) is logged server-side only, never returned to
		// the ticket user.
		if serr := h.scanner.Scan(tmp); serr != nil {
			if errors.Is(serr, errScanHit) {
				sendJSONError(w, "File rejected by virus scan", http.StatusUnsupportedMediaType)
			} else {
				log.Printf("ticket-attachments: scan failed for ticket %d: %v", t.ID, serr)
				sendJSONError(w, "Attachment scanning is temporarily unavailable, please try again later", http.StatusServiceUnavailable)
			}
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
		Mime:       sniffedMime,
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

	// The stored Mime is the sniffed type from upload time (never the
	// client's declared claim - see attachmentAllowed), and nosniff plus
	// Content-Disposition: attachment together stop a browser from executing
	// or rendering the body regardless of what Content-Type it carries.
	w.Header().Set("Content-Type", a.Mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
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
