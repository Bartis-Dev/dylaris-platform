package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"dylaris-core/services"
)

// DBMigrationHandler exposes the in-panel cross-database migration: connect a
// new target database, copy everything into it under maintenance mode, and
// verify. All endpoints are admin-only. The job is shared (Redis-backed) so
// every admin sees the same live status. The live Core keeps using the OLD
// database; the operator restarts with the new DB_* env vars after success.
type DBMigrationHandler struct {
	state *AppState
}

func NewDBMigrationHandler(state *AppState) *DBMigrationHandler {
	return &DBMigrationHandler{state: state}
}

// dbTargetRequest is the connection form submitted by the panel. dbType is the
// multiple-choice target backend (postgresql | timescaledb).
type dbTargetRequest struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"dbName"`
	SSLMode  string `json:"sslMode"`
	DBType   string `json:"dbType"`
}

func (req dbTargetRequest) toParams() services.DBConnParams {
	return services.DBConnParams{
		Host:     strings.TrimSpace(req.Host),
		Port:     strings.TrimSpace(req.Port),
		User:     strings.TrimSpace(req.User),
		Password: req.Password,
		DBName:   strings.TrimSpace(req.DBName),
		SSLMode:  strings.TrimSpace(req.SSLMode),
		DBType:   strings.TrimSpace(req.DBType),
	}
}

// GetMigration GET /api/admin/db/migration — shared job status (all admins).
func (h *DBMigrationHandler) GetMigration(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	if h.state.DBMigration == nil {
		sendJSONError(w, "DB migration not available", http.StatusServiceUnavailable)
		return
	}
	job, ok, err := h.state.DBMigration.GetJob(r.Context())
	if err != nil {
		sendJSONError(w, "Failed to read job: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp := map[string]interface{}{"success": true, "hasJob": ok}
	if ok {
		resp["job"] = job
	}
	json.NewEncoder(w).Encode(resp)
}

// StartMigration POST /api/admin/db/migration — start a migration to the target.
func (h *DBMigrationHandler) StartMigration(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	if h.state.DBMigration == nil {
		sendJSONError(w, "DB migration not available", http.StatusServiceUnavailable)
		return
	}
	var req dbTargetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	actorID, _ := r.Context().Value("userID").(string)
	actorName := h.resolveUsername(actorID)

	job, err := h.state.DBMigration.Start(r.Context(), req.toParams(), actorID, actorName)
	if err != nil {
		switch {
		case err == services.ErrMigrationRunning:
			sendJSONError(w, err.Error(), http.StatusConflict)
		default:
			sendJSONError(w, err.Error(), http.StatusBadRequest)
		}
		return
	}

	LogIdentityAudit(h.state, r, AuditEventMaintenanceToggled, actorID, "", map[string]interface{}{
		"action": "db_migration_start",
		"target": job.Target,
		"jobId":  job.ID,
	})
	if h.state.Events != nil {
		h.state.Events.Publish(r.Context(), "dbmigration.changed", nil)
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "job": job})
}

// TestConnection POST /api/admin/db/migration/test-connection — open the target
// and return its server version, so admins confirm they hit the right database.
func (h *DBMigrationHandler) TestConnection(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var req dbTargetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	params := req.toParams()

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	db, err := params.Open(ctx, 6*time.Second)
	if err != nil {
		sendJSONError(w, "Failed to connect: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer db.Close()

	var version string
	if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&version); err != nil {
		sendJSONError(w, "Connected but query failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	// Report whether the target already has the TimescaleDB extension, so the UI
	// can warn if the chosen target DB_TYPE does not match the server's reality.
	var tsInstalled bool
	_ = db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')").Scan(&tsInstalled)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":            true,
		"version":            version,
		"timescaleInstalled": tsInstalled,
	})
}

// resolveUsername best-effort maps a user UUID to a display name.
func (h *DBMigrationHandler) resolveUsername(id string) string {
	if id == "" {
		return ""
	}
	if u, err := h.state.Store.GetUserByID(id); err == nil && u != nil {
		return u.Username
	}
	return ""
}
