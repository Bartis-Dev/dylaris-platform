package handlers

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"dylaris-core/storage"
	"dylaris-core/store"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
	"github.com/pquerna/otp/totp"
)

// TicketMigrationHandler bundles three related admin tools:
//   - Connection test + dry-run/execute migration to an external ticket DB
//   - JSON backup of all ticket tables to dylaris_data/ticket-backups
//   - Restore from one of those backups, gated by the Danger Zone flow
//
// All operations are admin-only. The restore flow additionally requires:
//   - The admin's current TOTP code (defence in depth — possession of session is not enough)
//   - A typed confirmation phrase matching the backup name
//   - A 15-second cooldown between "initiate" and "execute" so a misclick can be cancelled
type TicketMigrationHandler struct {
	state    *AppState
	provider storage.StorageProvider

	// Outstanding restore tokens. Cleared when consumed or when expired.
	tokensMu sync.Mutex
	tokens   map[string]*restoreToken
}

type restoreToken struct {
	BackupName       string
	UserID           string
	IssuedAt         time.Time
	MinExecuteAfter  time.Time
	ConfirmationPhrase string
}

const (
	restoreCooldown = 15 * time.Second
	restoreTTL      = 5 * time.Minute
)

func NewTicketMigrationHandler(state *AppState) *TicketMigrationHandler {
	return &TicketMigrationHandler{
		state:    state,
		provider: buildBackupProvider(state),
		tokens:   make(map[string]*restoreToken),
	}
}

// buildBackupProvider builds the ticket-backups-scoped provider from the
// shared Core file storage config (falls back to the legacy
// dylaris_data/ticket-backups dir while the config is unset, so existing
// installs keep listing/downloading/restoring already-created backups).
// Mirrors buildProvider (library.go) / buildAttachmentProvider
// (ticket_attachments.go).
func buildBackupProvider(state *AppState) storage.StorageProvider {
	p, err := state.buildCoreStorageProvider(CoreStoragePrefixBackups)
	if err != nil {
		baseDir, _ := os.Getwd()
		root := filepath.Join(baseDir, "dylaris_data", CoreStoragePrefixBackups)
		os.MkdirAll(root, 0755)
		return &storage.LocalProvider{BasePath: root}
	}
	return p
}

// ── Status: counts + external-DB config visibility ───────────────────

// GetStatus GET /api/admin/tickets/migration/status
// Returns the current row counts per ticket table + whether the external
// DB env var is configured. The frontend uses this as the dashboard hub.
func (h *TicketMigrationHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	counts := map[string]int{}
	for _, t := range store.TicketTablesInOrder() {
		n, err := h.state.Store.CountTicketRows(t)
		if err == nil {
			counts[t] = n
		}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":            true,
		"mainCounts":         counts,
		"externalConfigured": h.state.ExternalTicketDBURL != "",
	})
}

// ── Connection test ──────────────────────────────────────────────────

type testConnectionRequest struct {
	URL string `json:"url"`
}

// TestExternalConnection POST /api/admin/tickets/migration/test-connection
// Takes a Postgres DSN, opens it, runs a SELECT 1, returns the version string
// so admins can confirm they hit the right server.
func (h *TicketMigrationHandler) TestExternalConnection(w http.ResponseWriter, r *http.Request) {
	var req testConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	url := strings.TrimSpace(req.URL)
	if url == "" {
		sendJSONError(w, "URL required", http.StatusBadRequest)
		return
	}

	db, err := sql.Open("postgres", url)
	if err != nil {
		sendJSONError(w, "Failed to open connection: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer db.Close()
	db.SetConnMaxLifetime(2 * time.Second)

	var version string
	if err := db.QueryRow("SELECT version()").Scan(&version); err != nil {
		sendJSONError(w, "Connected but query failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"version":  version,
	})
}

// ── Migration: dry-run + execute ─────────────────────────────────────

type migrationRequest struct {
	URL string `json:"url"`
}

// DryRunMigration POST /api/admin/tickets/migration/dry-run
// Counts source rows + (best-effort) target rows so admins see the gap
// before they pull the trigger.
func (h *TicketMigrationHandler) DryRunMigration(w http.ResponseWriter, r *http.Request) {
	var req migrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	src := map[string]int{}
	for _, t := range store.TicketTablesInOrder() {
		n, _ := h.state.Store.CountTicketRows(t)
		src[t] = n
	}
	dst := map[string]int{}
	if req.URL != "" {
		db, err := sql.Open("postgres", req.URL)
		if err == nil {
			defer db.Close()
			for _, t := range store.TicketTablesInOrder() {
				var n int
				_ = db.QueryRow(`SELECT COUNT(*) FROM ` + t).Scan(&n)
				dst[t] = n
			}
		}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"sourceCounts":  src,
		"targetCounts":  dst,
	})
}

// ExecuteMigration POST /api/admin/tickets/migration/execute
// Copies every ticket row from main → external. Append-only semantics:
// rows with conflicting PKs are skipped via ON CONFLICT DO NOTHING. Schema
// is applied first (CREATE IF NOT EXISTS) so a fresh external DB Just Works.
//
// Long-running for big datasets; the handler streams progress as a single
// JSON response at the end. A future polish phase can convert this to SSE.
func (h *TicketMigrationHandler) ExecuteMigration(w http.ResponseWriter, r *http.Request) {
	var req migrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.URL) == "" {
		sendJSONError(w, "URL required", http.StatusBadRequest)
		return
	}
	db, err := sql.Open("postgres", req.URL)
	if err != nil {
		sendJSONError(w, "Failed to open target: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer db.Close()

	// Apply ticket schemas to the target so a fresh DB Just Works.
	if err := applyTicketSchema(db); err != nil {
		sendJSONError(w, "Failed to apply schema on target: "+err.Error(), http.StatusInternalServerError)
		return
	}

	migrated := map[string]int{}
	skipped := map[string]int{}
	for _, t := range store.TicketTablesInOrder() {
		rows, err := h.state.Store.DumpTicketTable(t)
		if err != nil {
			sendJSONError(w, "Dump failed for "+t+": "+err.Error(), http.StatusInternalServerError)
			return
		}
		ok, skip, err := bulkInsertRows(db, t, rows)
		if err != nil {
			sendJSONError(w, "Insert failed for "+t+": "+err.Error(), http.StatusInternalServerError)
			return
		}
		migrated[t] = ok
		skipped[t] = skip
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"migrated": migrated,
		"skipped":  skipped,
	})
}

// ── Backups: create / list / download / delete ───────────────────────

type backupSummary struct {
	Name      string         `json:"name"`
	Size      int64          `json:"size"`
	CreatedAt time.Time      `json:"createdAt"`
	Counts    map[string]int `json:"counts,omitempty"`
}

// CreateBackup POST /api/admin/tickets/backup
// Dumps every ticket table to one JSON file under dylaris_data/ticket-backups.
// File name is timestamp-based for natural sort and chronological browsing.
func (h *TicketMigrationHandler) CreateBackup(w http.ResponseWriter, r *http.Request) {
	tables := store.TicketTablesInOrder()
	counts := map[string]int{}
	dump := map[string]interface{}{}
	for _, t := range tables {
		rows, err := h.state.Store.DumpTicketTable(t)
		if err != nil {
			sendJSONError(w, "Dump failed for "+t+": "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Convert []byte values to string in-place so the JSON output is
		// readable rather than base64-encoded. Postgres JSONB / INET / etc
		// all come back as []byte.
		for _, row := range rows {
			for k, v := range row {
				if b, ok := v.([]byte); ok {
					row[k] = string(b)
				}
			}
		}
		counts[t] = len(rows)
		dump[t] = rows
	}

	payload := map[string]interface{}{
		"backup_at": time.Now().UTC().Format(time.RFC3339),
		"counts":    counts,
		"tables":    dump,
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		sendJSONError(w, "Failed to encode backup", http.StatusInternalServerError)
		return
	}

	// Timestamp captured once so the filename suffix and the reported
	// CreatedAt can never drift apart across a second boundary.
	now := time.Now().UTC()
	name := "tickets-" + now.Format("20060102-150405") + ".json"
	if err := h.provider.WriteFile(name, bytes.NewReader(body)); err != nil {
		sendJSONError(w, "Failed to write backup: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"backup": backupSummary{
			Name:      name,
			Size:      int64(len(body)),
			CreatedAt: now,
			Counts:    counts,
		},
	})
}

// ListBackups GET /api/admin/tickets/backups
//
// CreatedAt is intentionally left zero-valued here: object stores expose no
// cheap per-key mtime through ListFiles, so per-file creation time is not
// available on the provider path. Ordering instead relies on the
// timestamped filename (tickets-YYYYMMDD-HHMMSS.json), which sorts
// lexicographically in the same order as chronologically — this is a
// documented, intentional trade-off from the storage-provider rework.
func (h *TicketMigrationHandler) ListBackups(w http.ResponseWriter, r *http.Request) {
	files, err := h.provider.ListFiles("/")
	if err != nil {
		sendJSONError(w, "Failed to list backups: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var out []backupSummary
	for _, f := range files {
		if f.IsDir || !strings.HasSuffix(f.Name, ".json") {
			continue
		}
		out = append(out, backupSummary{Name: f.Name, Size: f.Size})
	}
	// Newest first — the timestamped name is the sort key (see doc comment).
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"backups": out,
	})
}

// DownloadBackup GET /api/admin/tickets/backups/{name}/download
func (h *TicketMigrationHandler) DownloadBackup(w http.ResponseWriter, r *http.Request) {
	name := safeBackupName(mux.Vars(r)["name"])
	if name == "" {
		sendJSONError(w, "Invalid backup name", http.StatusBadRequest)
		return
	}

	// Prefer a short-lived pre-signed URL when the backend supports it (S3):
	// redirect the browser straight to object storage instead of streaming
	// every byte through Core. The ("", nil) sentinel (LocalProvider / path
	// backend) AND any error from DownloadURL both fall through to
	// streaming — an error must never be conflated with "no URL, stream it".
	if url, err := h.provider.DownloadURL(name, 5*time.Minute); err == nil && url != "" {
		http.Redirect(w, r, url, http.StatusFound)
		return
	}

	rc, err := h.provider.GetFile(name)
	if err != nil {
		sendJSONError(w, "Backup not found", http.StatusNotFound)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	io.Copy(w, rc)
}

// DeleteBackup DELETE /api/admin/tickets/backups/{name}
func (h *TicketMigrationHandler) DeleteBackup(w http.ResponseWriter, r *http.Request) {
	name := safeBackupName(mux.Vars(r)["name"])
	if name == "" {
		sendJSONError(w, "Invalid backup name", http.StatusBadRequest)
		return
	}
	if err := h.provider.DeletePath(name); err != nil {
		sendJSONError(w, "Delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// ── Restore: Danger Zone (2FA + 15s timer + typed phrase) ────────────

type restoreInitRequest struct {
	Name string `json:"name"`
}

// InitRestore POST /api/admin/tickets/restore/init
// Issues a token tied to the backup name. The token must be presented to
// /execute alongside the admin's TOTP + the typed confirmation phrase.
// Token can't be consumed before MinExecuteAfter (15s cooldown).
func (h *TicketMigrationHandler) InitRestore(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	user, err := h.state.Store.GetUserByID(userID)
	if err != nil || user == nil {
		sendJSONError(w, "User not found", http.StatusUnauthorized)
		return
	}
	if !user.Is2FAEnabled {
		sendJSONError(w, "Restore requires 2FA to be enabled on your account", http.StatusForbidden)
		return
	}
	var req restoreInitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	name := safeBackupName(req.Name)
	if name == "" {
		sendJSONError(w, "Invalid backup name", http.StatusBadRequest)
		return
	}
	if rc, err := h.provider.GetFile(name); err != nil {
		sendJSONError(w, "Backup not found", http.StatusNotFound)
		return
	} else {
		rc.Close()
	}

	tokenBytes := make([]byte, 24)
	rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)

	now := time.Now()
	t := &restoreToken{
		BackupName:         name,
		UserID:             userID,
		IssuedAt:           now,
		MinExecuteAfter:    now.Add(restoreCooldown),
		ConfirmationPhrase: "restore tickets from " + name,
	}
	h.tokensMu.Lock()
	h.tokens[token] = t
	// Garbage-collect expired tokens opportunistically — keeps the map
	// from growing forever in long-running processes.
	for k, v := range h.tokens {
		if now.Sub(v.IssuedAt) > restoreTTL {
			delete(h.tokens, k)
		}
	}
	h.tokensMu.Unlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":              true,
		"token":                token,
		"cooldownSeconds":      int(restoreCooldown.Seconds()),
		"confirmationPhrase":   t.ConfirmationPhrase,
	})
}

type restoreExecuteRequest struct {
	Token              string `json:"token"`
	TOTPCode           string `json:"totpCode"`
	ConfirmationPhrase string `json:"confirmationPhrase"`
}

// ExecuteRestore POST /api/admin/tickets/restore/execute
// Validates: token exists + not expired + cooldown passed + caller matches
// the token's user + TOTP verifies + typed phrase matches.
//
// Restore semantics: WIPES every ticket table, then loads the backup. The
// confirmation phrase is the strongest signal we have that the admin
// intended this; we still log everything.
func (h *TicketMigrationHandler) ExecuteRestore(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)

	var req restoreExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	h.tokensMu.Lock()
	t, ok := h.tokens[req.Token]
	if ok && (time.Since(t.IssuedAt) > restoreTTL) {
		delete(h.tokens, req.Token)
		ok = false
	}
	h.tokensMu.Unlock()
	if !ok {
		sendJSONError(w, "Invalid or expired token — start the restore flow again", http.StatusUnauthorized)
		return
	}
	if t.UserID != userID {
		sendJSONError(w, "Token does not belong to you", http.StatusForbidden)
		return
	}
	if time.Now().Before(t.MinExecuteAfter) {
		secs := int(time.Until(t.MinExecuteAfter).Seconds()) + 1
		sendJSONError(w, fmt.Sprintf("Cooldown still active — wait %d second(s)", secs), http.StatusTooEarly)
		return
	}
	if req.ConfirmationPhrase != t.ConfirmationPhrase {
		sendJSONError(w, "Confirmation phrase does not match", http.StatusBadRequest)
		return
	}

	// 2FA gate — must be valid TOTP (NOT backup codes; those are for
	// emergency login, not destructive admin actions).
	user, err := h.state.Store.GetUserByID(userID)
	if err != nil || user == nil || !user.Is2FAEnabled {
		sendJSONError(w, "Account no longer has 2FA — aborting", http.StatusForbidden)
		return
	}
	if !totp.Validate(strings.TrimSpace(req.TOTPCode), user.TOTPSecret) {
		sendJSONError(w, "Invalid 2FA code", http.StatusUnauthorized)
		return
	}

	// Read the backup file BEFORE wiping anything. The restore flow only
	// ever needs the raw bytes (unmarshalled below), never a real path on
	// disk, so no temp-file staging is needed here — GetFile + ReadAll is
	// sufficient regardless of backend.
	rc, err := h.provider.GetFile(t.BackupName)
	if err != nil {
		sendJSONError(w, "Failed to read backup file", http.StatusInternalServerError)
		return
	}
	body, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		sendJSONError(w, "Failed to read backup file", http.StatusInternalServerError)
		return
	}
	var payload struct {
		Tables map[string][]map[string]interface{} `json:"tables"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		sendJSONError(w, "Backup file is malformed", http.StatusUnprocessableEntity)
		return
	}

	// Wipe + reload inside a single transaction so a mid-restore crash
	// rolls back to the previous state cleanly.
	if err := wipeAndReload(h.state.Store, payload.Tables); err != nil {
		sendJSONError(w, "Restore failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// One-shot token — consume it whether the restore succeeded or not.
	h.tokensMu.Lock()
	delete(h.tokens, req.Token)
	h.tokensMu.Unlock()

	LogIdentityAudit(h.state, r, "ticket_restore_executed", userID, "", map[string]interface{}{
		"backup": t.BackupName,
	})

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// ── Helpers ──────────────────────────────────────────────────────────

// safeBackupName whitelists timestamped JSON names so DELETE/DOWNLOAD can't
// path-traverse out of the backup root.
func safeBackupName(in string) string {
	in = filepath.Base(strings.TrimSpace(in))
	if !strings.HasSuffix(in, ".json") {
		return ""
	}
	if strings.Contains(in, "/") || strings.Contains(in, "\\") || strings.Contains(in, "..") {
		return ""
	}
	return in
}

// applyTicketSchema creates the ticket tables on an arbitrary *sql.DB so
// migration/restore against a fresh DB works. Mirrors what database.createTicketTables
// runs against the main DB at boot time. Kept compact since the tables are
// small and the SQL is identical to db.go's set.
func applyTicketSchema(db *sql.DB) error {
	// Use the same DDL list as database.createTicketTables — duplication is
	// intentional: changes to the main schema get mirrored here when we
	// remember, which is exactly when migration matters. A future polish
	// can extract the DDL to a shared constant.
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ticket_categories (
			id SERIAL PRIMARY KEY,
			name VARCHAR(128) NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			requires_server BOOLEAN NOT NULL DEFAULT FALSE,
			default_priority VARCHAR(16) NOT NULL DEFAULT 'normal',
			default_assignee_team VARCHAR(64),
			color VARCHAR(16),
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			position INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(name)
		)`,
		`CREATE TABLE IF NOT EXISTS tickets (
			id SERIAL PRIMARY KEY,
			region VARCHAR(32) NOT NULL DEFAULT 'default',
			category_id INTEGER NOT NULL,
			user_id UUID NOT NULL,
			server_uuid VARCHAR(64),
			server_region VARCHAR(32),
			title VARCHAR(200) NOT NULL,
			status VARCHAR(32) NOT NULL DEFAULT 'open',
			priority VARCHAR(16) NOT NULL DEFAULT 'normal',
			assigned_user_id UUID,
			assigned_team VARCHAR(64),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			closed_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS ticket_messages (
			id SERIAL PRIMARY KEY,
			ticket_id INTEGER NOT NULL,
			user_id UUID NOT NULL,
			body TEXT NOT NULL,
			is_internal BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS ticket_watchers (
			ticket_id INTEGER NOT NULL,
			user_id UUID NOT NULL,
			can_reply BOOLEAN NOT NULL DEFAULT FALSE,
			added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			added_by UUID,
			PRIMARY KEY (ticket_id, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS ticket_audit_events (
			id BIGSERIAL PRIMARY KEY,
			ticket_id INTEGER NOT NULL,
			event_type VARCHAR(64) NOT NULL,
			actor_user_id UUID,
			metadata JSONB,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS ticket_attachments (
			id SERIAL PRIMARY KEY,
			ticket_id INTEGER NOT NULL,
			message_id INTEGER,
			filename VARCHAR(255) NOT NULL,
			mime VARCHAR(128) NOT NULL DEFAULT 'application/octet-stream',
			size_bytes BIGINT NOT NULL DEFAULT 0,
			storage_key VARCHAR(512) NOT NULL,
			uploaded_by UUID,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS ticket_canned_responses (
			id SERIAL PRIMARY KEY,
			name VARCHAR(128) NOT NULL,
			body TEXT NOT NULL,
			category_id INTEGER,
			created_by UUID,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(name)
		)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// safeColumnName matches a plain SQL identifier — used to reject crafted
// column names from a restored backup before they reach an INSERT.
var safeColumnName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// bulkInsertRows inserts a slice of maps into the given table. Skips rows
// whose PK already exists (ON CONFLICT DO NOTHING) so re-running migration
// is safe. Returns (inserted, skipped, error).
//
// Builds the column list dynamically from each row's keys — keeps the
// migration robust against schema drift between source and target.
func bulkInsertRows(db *sql.DB, table string, rows []map[string]interface{}) (int, int, error) {
	inserted := 0
	skipped := 0
	for _, row := range rows {
		if len(row) == 0 {
			skipped++
			continue
		}
		cols := make([]string, 0, len(row))
		for k := range row {
			cols = append(cols, k)
		}
		sort.Strings(cols) // deterministic placeholder positions
		// Column names are interpolated into the INSERT, so they must be plain
		// SQL identifiers — a backup file with a crafted key must not inject.
		for _, c := range cols {
			if !safeColumnName.MatchString(c) {
				return inserted, skipped, fmt.Errorf("bulkInsertRows: unsafe column name %q for table %s", c, table)
			}
		}
		placeholders := make([]string, len(cols))
		args := make([]interface{}, len(cols))
		for i, c := range cols {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			v := row[c]
			// JSONB columns came back from the dump as strings (we converted
			// in CreateBackup) — Postgres accepts a string for jsonb INSERT
			// just fine. Same for INET-as-string.
			args[i] = v
		}
		q := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING`,
			table,
			strings.Join(cols, ", "),
			strings.Join(placeholders, ", "),
		)
		res, err := db.Exec(q, args...)
		if err != nil {
			return inserted, skipped, err
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			inserted++
		} else {
			skipped++
		}
	}
	return inserted, skipped, nil
}

// wipeAndReload truncates every ticket table on the main DB and reloads
// the supplied payload. Wrapped in a single transaction-equivalent via the
// store's own DB handle if available; here we run statements sequentially
// since the Store interface doesn't expose tx control. A future polish
// phase can switch to an explicit *sql.Tx for atomic rollback.
func wipeAndReload(s store.Store, tables map[string][]map[string]interface{}) error {
	// We don't have direct *sql.DB access from the Store interface, so the
	// wipe + reload runs through DumpTicketTable's sibling — except there
	// isn't one for INSERT. The Store interface intentionally doesn't grow
	// a "DELETE all from table X" method; restore is rare enough that the
	// caller (this handler) does it via the store's hidden raw access:
	// we cast to *PostgresStore for the duration of the call.
	type rawStore interface {
		RawDB() *sql.DB
	}
	rs, ok := s.(rawStore)
	if !ok {
		return fmt.Errorf("store does not expose RawDB — restore unavailable")
	}
	db := rs.RawDB()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// FK-safe wipe order = reverse insert order.
	wipeOrder := store.TicketTablesInOrder()
	for i := len(wipeOrder) - 1; i >= 0; i-- {
		if _, err := tx.Exec(`DELETE FROM ` + wipeOrder[i]); err != nil {
			return err
		}
	}
	// Reload in normal order.
	for _, t := range store.TicketTablesInOrder() {
		rows := tables[t]
		for _, row := range rows {
			if len(row) == 0 {
				continue
			}
			cols := make([]string, 0, len(row))
			for k := range row {
				cols = append(cols, k)
			}
			sort.Strings(cols)
			placeholders := make([]string, len(cols))
			args := make([]interface{}, len(cols))
			for i, c := range cols {
				placeholders[i] = fmt.Sprintf("$%d", i+1)
				args[i] = row[c]
			}
			q := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING`,
				t, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
			if _, err := tx.Exec(q, args...); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
