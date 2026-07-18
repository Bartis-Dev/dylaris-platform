package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"dylaris-core/models"
	"dylaris-core/storage"
	"dylaris-core/store"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/mux"
	"github.com/pquerna/otp/totp"
)

// memProvider is a minimal in-memory StorageProvider fixture used to pin the
// provider contract the backup handlers are rewritten against. Local to this
// file; never redeclared elsewhere.
type memProvider struct{ m map[string][]byte }

func newMemProvider() *memProvider { return &memProvider{m: map[string][]byte{}} }

func (p *memProvider) ListFiles(path string) ([]storage.FileInfo, error) {
	var out []storage.FileInfo
	for k, v := range p.m {
		out = append(out, storage.FileInfo{Name: k, Size: int64(len(v)), Enabled: true})
	}
	return out, nil
}
func (p *memProvider) GetFile(path string) (io.ReadCloser, error) {
	b, ok := p.m[path]
	if !ok {
		// Must be errors.Is(err, fs.ErrNotExist)-comparable: migrateLocalDirToProvider
		// (core_storage.go) only treats a recognised not-found as "copy it",
		// and this fake is the destination in the migrate tests that seed a
		// fresh dst and expect the copy to proceed.
		return nil, os.ErrNotExist
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}
func (p *memProvider) DeletePath(path string) error { delete(p.m, path); return nil }
func (p *memProvider) CreateDir(string) error       { return nil }
func (p *memProvider) CopyToLocal(string, string) error {
	return nil
}
func (p *memProvider) WriteFile(path string, r io.Reader) error {
	b, _ := io.ReadAll(r)
	p.m[path] = b
	return nil
}
func (p *memProvider) DownloadURL(string, time.Duration) (string, error) { return "", nil }

func TestBackupProvider_WriteListReadDelete(t *testing.T) {
	p := newMemProvider()
	if err := p.WriteFile("tickets-20260718-101010.json", strings.NewReader(`{"ok":true}`)); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	files, _ := p.ListFiles("/")
	if len(files) != 1 || files[0].Name != "tickets-20260718-101010.json" {
		t.Fatalf("ListFiles = %+v, want the one backup", files)
	}
	rc, err := p.GetFile("tickets-20260718-101010.json")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != `{"ok":true}` {
		t.Errorf("GetFile = %q", got)
	}
	if err := p.DeletePath("tickets-20260718-101010.json"); err != nil {
		t.Fatalf("DeletePath: %v", err)
	}
	if files, _ := p.ListFiles("/"); len(files) != 0 {
		t.Errorf("backup still present after delete: %+v", files)
	}
}

// TestNewTicketMigrationHandler_HasProvider pins that backups now route
// through the shared storage.StorageProvider abstraction instead of raw
// os.* calls against h.rootDir (which no longer exists).
func TestNewTicketMigrationHandler_HasProvider(t *testing.T) {
	// buildBackupProvider falls back to a cwd-relative dylaris_data/ticket-backups
	// dir (buildCoreStorageProvider, since AppState{} has no configured
	// storage) and os.MkdirAll's it as a side effect. t.Chdir into a private
	// temp dir so that side effect lands somewhere t.TempDir() auto-cleans,
	// instead of leaking into (and poisoning) this package's own directory.
	t.Chdir(t.TempDir())

	h := NewTicketMigrationHandler(&AppState{})
	if h.provider == nil {
		t.Fatal("TicketMigrationHandler.provider is nil; backups must route through the shared provider")
	}
}

// ── ticketBackupMemProvider: configurable fake for handler-level tests ──

// ticketBackupMemProvider is a configurable in-memory storage.StorageProvider
// fake: normal WriteFile/GetFile/ListFiles/DeletePath operate on an in-memory
// map, while the err/downloadURL fields let a test force each individual
// failure or redirect branch the handlers must handle. Distinct from
// memProvider above (the brief's fixed minimal fixture) and from urlProvider
// (library_serve_test.go, library-scoped, not reused across subsystems).
type ticketBackupMemProvider struct {
	mu sync.Mutex
	m  map[string][]byte

	listErr     error
	getErr      error
	deleteErr   error
	downloadURL string
	downloadErr error
}

func newTicketBackupMemProvider() *ticketBackupMemProvider {
	return &ticketBackupMemProvider{m: map[string][]byte{}}
}

func (p *ticketBackupMemProvider) ListFiles(string) ([]storage.FileInfo, error) {
	if p.listErr != nil {
		return nil, p.listErr
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]storage.FileInfo, 0, len(p.m))
	for k, v := range p.m {
		out = append(out, storage.FileInfo{Name: k, Size: int64(len(v)), Enabled: true})
	}
	return out, nil
}

func (p *ticketBackupMemProvider) GetFile(path string) (io.ReadCloser, error) {
	if p.getErr != nil {
		return nil, p.getErr
	}
	p.mu.Lock()
	b, ok := p.m[path]
	p.mu.Unlock()
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (p *ticketBackupMemProvider) DeletePath(path string) error {
	if p.deleteErr != nil {
		return p.deleteErr
	}
	p.mu.Lock()
	delete(p.m, path)
	p.mu.Unlock()
	return nil
}

func (p *ticketBackupMemProvider) CreateDir(string) error           { return nil }
func (p *ticketBackupMemProvider) CopyToLocal(string, string) error { return nil }

func (p *ticketBackupMemProvider) WriteFile(path string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.m[path] = b
	p.mu.Unlock()
	return nil
}

func (p *ticketBackupMemProvider) DownloadURL(string, time.Duration) (string, error) {
	return p.downloadURL, p.downloadErr
}

// ── ticketBackupFakeStore: the small store surface CreateBackup/InitRestore/
// ExecuteRestore touch ──

// ticketBackupFakeStore covers exactly the store.Store methods the backup +
// restore handlers call: DumpTicketTable (CreateBackup), GetUserByID
// (InitRestore/ExecuteRestore's 2FA gate), RawDB (wipeAndReload's raw SQL
// access) and InsertAuditIdentity (the audit log on a successful restore).
// Distinct from every other fake in this package.
type ticketBackupFakeStore struct {
	store.Store
	user       *models.User
	db         *sql.DB
	dumpErr    error
	auditCalls int
}

func (f *ticketBackupFakeStore) DumpTicketTable(table string) ([]map[string]interface{}, error) {
	if f.dumpErr != nil {
		return nil, f.dumpErr
	}
	return nil, nil
}

func (f *ticketBackupFakeStore) GetUserByID(id string) (*models.User, error) {
	if f.user != nil && f.user.ID == id {
		return f.user, nil
	}
	return nil, nil
}

func (f *ticketBackupFakeStore) RawDB() *sql.DB { return f.db }

func (f *ticketBackupFakeStore) InsertAuditIdentity(ev *models.AuditEventIdentity) error {
	f.auditCalls++
	return nil
}

func newTicketBackupHandler(state *AppState, prov storage.StorageProvider) *TicketMigrationHandler {
	return &TicketMigrationHandler{
		state:    state,
		provider: prov,
		tokens:   make(map[string]*restoreToken),
	}
}

// ── Round trip: create -> list -> download -> restore ──

// TestTicketBackups_CreateListDownloadRestore_RoundTrip is the central
// non-regression test for this task: every backup operation must now flow
// through the shared storage.StorageProvider, and a backup created that way
// must still be listable, downloadable byte-for-byte, and restorable end to
// end (2FA + cooldown + confirmation phrase gates all still enforced).
func TestTicketBackups_CreateListDownloadRestore_RoundTrip(t *testing.T) {
	secret := freshTOTPSecret(t) // reused from totp_verify_test.go
	user := &models.User{ID: "admin-1", Is2FAEnabled: true, TOTPSecret: secret}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	fs := &ticketBackupFakeStore{user: user, db: db}
	prov := newTicketBackupMemProvider()
	h := newTicketBackupHandler(&AppState{Store: fs}, prov)

	// 1) Create.
	createRW := httptest.NewRecorder()
	h.CreateBackup(createRW, httptest.NewRequest(http.MethodPost, "/api/admin/tickets/backup", nil))
	if createRW.Code != http.StatusOK {
		t.Fatalf("CreateBackup status = %d, want 200 (%s)", createRW.Code, createRW.Body.String())
	}
	var createResp struct {
		Success bool          `json:"success"`
		Backup  backupSummary `json:"backup"`
	}
	if err := json.Unmarshal(createRW.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode CreateBackup response: %v", err)
	}
	name := createResp.Backup.Name
	if name == "" {
		t.Fatal("CreateBackup returned no backup name")
	}
	if !strings.HasPrefix(name, "tickets-") || !strings.HasSuffix(name, ".json") {
		t.Fatalf("unexpected backup name shape: %q", name)
	}

	prov.mu.Lock()
	wantBody := append([]byte{}, prov.m[name]...)
	prov.mu.Unlock()
	if len(wantBody) == 0 {
		t.Fatalf("CreateBackup did not write anything to the provider under %q", name)
	}

	// 2) List.
	listRW := httptest.NewRecorder()
	h.ListBackups(listRW, httptest.NewRequest(http.MethodGet, "/api/admin/tickets/backups", nil))
	if listRW.Code != http.StatusOK {
		t.Fatalf("ListBackups status = %d, want 200 (%s)", listRW.Code, listRW.Body.String())
	}
	var listResp struct {
		Success bool            `json:"success"`
		Backups []backupSummary `json:"backups"`
	}
	if err := json.Unmarshal(listRW.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode ListBackups response: %v", err)
	}
	if len(listResp.Backups) != 1 || listResp.Backups[0].Name != name {
		t.Fatalf("ListBackups = %+v, want exactly the just-created backup %q", listResp.Backups, name)
	}

	// 3) Download - the memProvider returns ("", nil) so this must stream,
	// not redirect, and the streamed bytes must equal what was written.
	dlReq := httptest.NewRequest(http.MethodGet, "/api/admin/tickets/backups/"+name+"/download", nil)
	dlReq = mux.SetURLVars(dlReq, map[string]string{"name": name})
	dlRW := httptest.NewRecorder()
	h.DownloadBackup(dlRW, dlReq)
	if dlRW.Code != http.StatusOK {
		t.Fatalf("DownloadBackup status = %d, want 200 (%s)", dlRW.Code, dlRW.Body.String())
	}
	if !bytes.Equal(dlRW.Body.Bytes(), wantBody) {
		t.Fatalf("downloaded body does not match what was written to the provider")
	}

	// 4) InitRestore.
	initBody, _ := json.Marshal(restoreInitRequest{Name: name})
	initReq := httptest.NewRequest(http.MethodPost, "/api/admin/tickets/restore/init", bytes.NewReader(initBody))
	initReq = initReq.WithContext(context.WithValue(initReq.Context(), "userID", user.ID))
	initRW := httptest.NewRecorder()
	h.InitRestore(initRW, initReq)
	if initRW.Code != http.StatusOK {
		t.Fatalf("InitRestore status = %d, want 200 (%s)", initRW.Code, initRW.Body.String())
	}
	var initResp struct {
		Success            bool   `json:"success"`
		Token              string `json:"token"`
		ConfirmationPhrase string `json:"confirmationPhrase"`
	}
	if err := json.Unmarshal(initRW.Body.Bytes(), &initResp); err != nil {
		t.Fatalf("decode InitRestore response: %v", err)
	}

	// Bypass the 15s human cooldown deliberately (white-box, same package):
	// it is a UX safety net unrelated to what this task changed. Everything
	// else - token/2FA/phrase checks and the eventual provider read - still
	// runs for real below.
	h.tokensMu.Lock()
	if tok, ok := h.tokens[initResp.Token]; ok {
		tok.MinExecuteAfter = time.Now().Add(-time.Second)
	}
	h.tokensMu.Unlock()

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	// wipeAndReload wipes every ticket table (reverse insert order) then
	// reloads from the decoded backup. DumpTicketTable's fake returns zero
	// rows for every table, so the backup's "tables" payload decodes to
	// nil/empty slices and only the DELETE half of wipeAndReload runs.
	mock.ExpectBegin()
	tables := store.TicketTablesInOrder()
	for i := len(tables) - 1; i >= 0; i-- {
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM " + tables[i])).WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectCommit()

	execBody, _ := json.Marshal(restoreExecuteRequest{
		Token:              initResp.Token,
		TOTPCode:           code,
		ConfirmationPhrase: initResp.ConfirmationPhrase,
	})
	execReq := httptest.NewRequest(http.MethodPost, "/api/admin/tickets/restore/execute", bytes.NewReader(execBody))
	execReq = execReq.WithContext(context.WithValue(execReq.Context(), "userID", user.ID))
	execRW := httptest.NewRecorder()
	h.ExecuteRestore(execRW, execReq)
	if execRW.Code != http.StatusOK {
		t.Fatalf("ExecuteRestore status = %d, want 200 (%s)", execRW.Code, execRW.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations not met: %v", err)
	}
	if fs.auditCalls != 1 {
		t.Fatalf("audit calls = %d, want 1 (restore success must be audited)", fs.auditCalls)
	}
}

// ── ListBackups: ordering + provider error surfacing ──

// TestListBackups_OrderingIsChronological verifies the accepted trade-off
// documented on ListBackups: with no per-file CreatedAt available from the
// provider, ordering relies purely on the timestamped filename
// (tickets-YYYYMMDD-HHMMSS.json) sorting lexicographically the same as
// chronologically. Names are deliberately written out of order and span a
// year/month/day boundary so a byte-order bug would show up immediately.
func TestListBackups_OrderingIsChronological(t *testing.T) {
	prov := newTicketBackupMemProvider()
	seed := []string{
		"tickets-20260101-000000.json",
		"tickets-20261231-235959.json",
		"tickets-20260630-120000.json",
	}
	for _, n := range seed {
		if err := prov.WriteFile(n, strings.NewReader("{}")); err != nil {
			t.Fatalf("seed %q: %v", n, err)
		}
	}
	h := newTicketBackupHandler(&AppState{}, prov)

	rw := httptest.NewRecorder()
	h.ListBackups(rw, httptest.NewRequest(http.MethodGet, "/api/admin/tickets/backups", nil))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}
	var resp struct {
		Backups []backupSummary `json:"backups"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := []string{
		"tickets-20261231-235959.json", // newest
		"tickets-20260630-120000.json",
		"tickets-20260101-000000.json", // oldest
	}
	if len(resp.Backups) != len(want) {
		t.Fatalf("got %d backups, want %d (%+v)", len(resp.Backups), len(want), resp.Backups)
	}
	for i, name := range want {
		if resp.Backups[i].Name != name {
			t.Fatalf("position %d = %q, want %q (full order: %+v)", i, resp.Backups[i].Name, name, resp.Backups)
		}
	}
}

// TestListBackups_ProviderErrorSurfaces pins that a provider failure comes
// back as a clear 500 with a non-empty error body - never a silent empty
// 200 that would make a broken backend look like "no backups exist yet".
func TestListBackups_ProviderErrorSurfaces(t *testing.T) {
	prov := newTicketBackupMemProvider()
	prov.listErr = errors.New("boom: bucket unreachable")
	h := newTicketBackupHandler(&AppState{}, prov)

	rw := httptest.NewRecorder()
	h.ListBackups(rw, httptest.NewRequest(http.MethodGet, "/api/admin/tickets/backups", nil))
	if rw.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rw.Code)
	}
	if rw.Body.Len() == 0 {
		t.Fatal("empty response body on a provider error")
	}
	var resp struct {
		Success bool `json:"success"`
	}
	_ = json.Unmarshal(rw.Body.Bytes(), &resp)
	if resp.Success {
		t.Fatal("success=true on a provider error")
	}
}

// ── DownloadBackup: redirect vs stream, and not-found ──

// TestDownloadBackup_RedirectsWhenProviderSignsURL covers the Task 6 pattern:
// a non-empty DownloadURL with a nil error must redirect the browser
// straight to the signed URL rather than streaming through Core.
func TestDownloadBackup_RedirectsWhenProviderSignsURL(t *testing.T) {
	prov := newTicketBackupMemProvider()
	prov.downloadURL = "https://signed.example/ticket-backups/tickets-x.json"
	h := newTicketBackupHandler(&AppState{}, prov)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/tickets/backups/tickets-x.json/download", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "tickets-x.json"})
	rw := httptest.NewRecorder()
	h.DownloadBackup(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rw.Code)
	}
	if loc := rw.Header().Get("Location"); loc != prov.downloadURL {
		t.Errorf("Location = %q, want %q", loc, prov.downloadURL)
	}
}

// TestDownloadBackup_StreamsWhenProviderDoesNotSignURL locks in the highest-
// value non-regression from the task brief: the ("", nil) sentinel (path
// backend/LocalProvider - "no URL, stream it yourself") and a genuine
// DownloadURL error must BOTH fall through to streaming, and must never be
// conflated with each other.
func TestDownloadBackup_StreamsWhenProviderDoesNotSignURL(t *testing.T) {
	cases := []struct {
		name        string
		downloadURL string
		downloadErr error
	}{
		{"empty-string sentinel: no presigned URL, stream the file", "", nil},
		{"DownloadURL error is not conflated with the sentinel: still stream", "", errors.New("presign not supported")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov := newTicketBackupMemProvider()
			prov.downloadURL = tc.downloadURL
			prov.downloadErr = tc.downloadErr
			body := []byte(`{"backup_at":"2026-07-18T00:00:00Z"}`)
			if err := prov.WriteFile("tickets-x.json", bytes.NewReader(body)); err != nil {
				t.Fatalf("seed: %v", err)
			}
			h := newTicketBackupHandler(&AppState{}, prov)

			req := httptest.NewRequest(http.MethodGet, "/api/admin/tickets/backups/tickets-x.json/download", nil)
			req = mux.SetURLVars(req, map[string]string{"name": "tickets-x.json"})
			rw := httptest.NewRecorder()
			h.DownloadBackup(rw, req)

			if rw.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (stream, not redirect) (%s)", rw.Code, rw.Body.String())
			}
			if loc := rw.Header().Get("Location"); loc != "" {
				t.Errorf("Location = %q, want empty (no redirect when streaming)", loc)
			}
			if !bytes.Equal(rw.Body.Bytes(), body) {
				t.Errorf("body = %q, want %q", rw.Body.Bytes(), body)
			}
			if ct := rw.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}

// TestDownloadBackup_NotFoundWhenMissing pins that a missing key surfaces as
// 404, not a silent 200 with an empty body.
func TestDownloadBackup_NotFoundWhenMissing(t *testing.T) {
	prov := newTicketBackupMemProvider()
	h := newTicketBackupHandler(&AppState{}, prov)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/tickets/backups/tickets-missing.json/download", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "tickets-missing.json"})
	rw := httptest.NewRecorder()
	h.DownloadBackup(rw, req)

	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rw.Code, rw.Body.String())
	}
}

// ── Path-backend (unconfigured) non-regression ──

// TestTicketBackups_PathBackendUnchangedBehavior is the top non-regression
// requirement: a backup that already exists on disk under the legacy
// dylaris_data/ticket-backups layout (as if written by the pre-rewrite code,
// or by an install that never configured Core file storage) must remain
// listable and downloadable byte-for-byte, at the exact same filename and
// on-disk location, through the LocalProvider fallback.
func TestTicketBackups_PathBackendUnchangedBehavior(t *testing.T) {
	dir := t.TempDir()
	legacyName := "tickets-20260101-093000.json"
	legacyBody := []byte(`{"backup_at":"2026-01-01T09:30:00Z","counts":{},"tables":{}}`)
	if err := os.WriteFile(filepath.Join(dir, legacyName), legacyBody, 0640); err != nil {
		t.Fatalf("seed legacy backup: %v", err)
	}

	h := newTicketBackupHandler(&AppState{}, &storage.LocalProvider{BasePath: dir})

	listRW := httptest.NewRecorder()
	h.ListBackups(listRW, httptest.NewRequest(http.MethodGet, "/api/admin/tickets/backups", nil))
	if listRW.Code != http.StatusOK {
		t.Fatalf("ListBackups status = %d, want 200 (%s)", listRW.Code, listRW.Body.String())
	}
	var listResp struct {
		Backups []backupSummary `json:"backups"`
	}
	if err := json.Unmarshal(listRW.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listResp.Backups) != 1 || listResp.Backups[0].Name != legacyName {
		t.Fatalf("ListBackups = %+v, want the pre-existing legacy backup %q", listResp.Backups, legacyName)
	}

	dlReq := httptest.NewRequest(http.MethodGet, "/api/admin/tickets/backups/"+legacyName+"/download", nil)
	dlReq = mux.SetURLVars(dlReq, map[string]string{"name": legacyName})
	dlRW := httptest.NewRecorder()
	h.DownloadBackup(dlRW, dlReq)
	if dlRW.Code != http.StatusOK {
		t.Fatalf("DownloadBackup status = %d, want 200 (%s)", dlRW.Code, dlRW.Body.String())
	}
	if !bytes.Equal(dlRW.Body.Bytes(), legacyBody) {
		t.Fatalf("downloaded legacy backup body mismatch: got %q want %q", dlRW.Body.Bytes(), legacyBody)
	}

	if _, err := os.Stat(filepath.Join(dir, legacyName)); err != nil {
		t.Fatalf("legacy backup file missing from its original on-disk location: %v", err)
	}
}

// ── DeleteBackup ──

func TestDeleteBackup_Success(t *testing.T) {
	prov := newTicketBackupMemProvider()
	if err := prov.WriteFile("tickets-x.json", strings.NewReader("{}")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := newTicketBackupHandler(&AppState{}, prov)

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/tickets/backups/tickets-x.json", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "tickets-x.json"})
	rw := httptest.NewRecorder()
	h.DeleteBackup(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rw.Code, rw.Body.String())
	}
	prov.mu.Lock()
	_, stillThere := prov.m["tickets-x.json"]
	prov.mu.Unlock()
	if stillThere {
		t.Fatal("backup still present in the provider after delete")
	}
}

func TestDeleteBackup_ProviderErrorSurfaces(t *testing.T) {
	prov := newTicketBackupMemProvider()
	prov.deleteErr = errors.New("permission denied")
	h := newTicketBackupHandler(&AppState{}, prov)

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/tickets/backups/tickets-x.json", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "tickets-x.json"})
	rw := httptest.NewRecorder()
	h.DeleteBackup(rw, req)

	if rw.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (%s)", rw.Code, rw.Body.String())
	}
}

// ── InitRestore: provider-backed existence probe ──

// TestInitRestore_NotFoundWhenProviderMisses covers the os.Stat -> provider
// GetFile probe swap: a name that doesn't exist in the provider must 404
// before any token is issued.
func TestInitRestore_NotFoundWhenProviderMisses(t *testing.T) {
	prov := newTicketBackupMemProvider()
	fs := &ticketBackupFakeStore{user: &models.User{ID: "admin-1", Is2FAEnabled: true, TOTPSecret: "JBSWY3DPEHPK3PXP"}}
	h := newTicketBackupHandler(&AppState{Store: fs}, prov)

	body, _ := json.Marshal(restoreInitRequest{Name: "tickets-missing.json"})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/tickets/restore/init", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), "userID", "admin-1"))
	rw := httptest.NewRecorder()
	h.InitRestore(rw, req)

	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rw.Code, rw.Body.String())
	}
	if len(h.tokens) != 0 {
		t.Fatalf("a restore token was issued for a nonexistent backup: %d tokens", len(h.tokens))
	}
}
