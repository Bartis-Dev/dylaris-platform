package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"dylaris-core/authz"
	"dylaris-core/models"
	"dylaris-core/services"
	"dylaris-core/storage"
	backupstorage "dylaris-core/storage/backup"

	pbNode "dylaris-proto/node"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type BackupHandler struct {
	state *AppState
}

func NewBackupHandler(state *AppState) *BackupHandler {
	return &BackupHandler{state: state}
}

// ───────────── Storages (PANEL settings.read/write) ─────────────
// Platform-shared storage-provider configs; gated at the route via RequireCap
// (routes.go), not in-handler. Admin still passes via the resolver's admin
// short-circuit; a panel-role holder of settings.* also passes.

// ListStorages GET /api/backup-storages
func (h *BackupHandler) ListStorages(w http.ResponseWriter, r *http.Request) {
	storages, err := h.state.Store.ListBackupStorages()
	if err != nil {
		sendJSONError(w, "Database error", 500)
		return
	}
	if storages == nil {
		storages = []models.BackupStorage{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "storages": storages})
}

// CreateStorage POST /api/backup-storages
func (h *BackupHandler) CreateStorage(w http.ResponseWriter, r *http.Request) {
	var req models.BackupStorage
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}
	if req.Name == "" || req.Provider == "" {
		sendJSONError(w, "name and provider are required", 400)
		return
	}
	id, err := h.state.Store.CreateBackupStorage(&req)
	if err != nil {
		sendJSONError(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "id": id})
}

// UpdateStorage PATCH /api/backup-storages/{id}
func (h *BackupHandler) UpdateStorage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		sendJSONError(w, "Invalid ID", 400)
		return
	}
	var req models.BackupStorage
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}
	req.ID = id
	if err := h.state.Store.UpdateBackupStorage(&req); err != nil {
		sendJSONError(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// DeleteStorage DELETE /api/backup-storages/{id}
func (h *BackupHandler) DeleteStorage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		sendJSONError(w, "Invalid ID", 400)
		return
	}
	if err := h.state.Store.DeleteBackupStorage(id); err != nil {
		sendJSONError(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// TestStorage POST /api/backup-storages/{id}/test — round-trip put/get/delete
// a tiny object to confirm credentials and bucket access.
func (h *BackupHandler) TestStorage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		sendJSONError(w, "Invalid ID", 400)
		return
	}
	storage, err := h.state.Store.GetBackupStorage(id)
	if err != nil {
		sendJSONError(w, "Storage not found", 404)
		return
	}
	provider, err := backupstorage.Open(r.Context(), storage, h.backupDeps())
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	probeKey := fmt.Sprintf("__dylaris_probe_%d.txt", time.Now().UnixNano())
	if err := provider.Put(r.Context(), probeKey, strReader("ok"), 2); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "put failed: " + err.Error()})
		return
	}
	provider.Delete(r.Context(), probeKey)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// ───────────── Jobs ─────────────

// ListJobs GET /api/servers/{id}/backup-jobs
func (h *BackupHandler) ListJobs(w http.ResponseWriter, r *http.Request) {
	serverID, srv, ok := h.resolveServer(w, r)
	if !ok {
		return
	}
	_ = srv
	jobs, err := h.state.Store.ListBackupJobs(serverID)
	if err != nil {
		sendJSONError(w, "Database error", 500)
		return
	}
	if jobs == nil {
		jobs = []models.BackupJob{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "jobs": jobs})
}

// CreateJob POST /api/servers/{id}/backup-jobs
func (h *BackupHandler) CreateJob(w http.ResponseWriter, r *http.Request) {
	serverID, _, ok := h.resolveServer(w, r)
	if !ok {
		return
	}
	var req models.BackupJob
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}
	req.ServerID = serverID
	if req.RetentionCount <= 0 {
		req.RetentionCount = 3
	}
	if req.Schedule == "" {
		req.Schedule = "manual"
	}
	req.NextRunAt = computeNextRun(req.Schedule, time.Now())
	id, err := h.state.Store.CreateBackupJob(&req)
	if err != nil {
		sendJSONError(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "id": id})
}

// UpdateJob PATCH /api/backup-jobs/{jobId}
func (h *BackupHandler) UpdateJob(w http.ResponseWriter, r *http.Request) {
	jobID, job, ok := h.resolveJobWithAccess(w, r, "backups.create")
	if !ok {
		return
	}
	var req models.BackupJob
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}
	req.ID = jobID
	req.ServerID = job.ServerID
	if req.RetentionCount <= 0 {
		req.RetentionCount = job.RetentionCount
	}
	if req.Schedule == "" {
		req.Schedule = job.Schedule
	}
	req.NextRunAt = computeNextRun(req.Schedule, time.Now())
	if err := h.state.Store.UpdateBackupJob(&req); err != nil {
		sendJSONError(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// DeleteJob DELETE /api/backup-jobs/{jobId}
func (h *BackupHandler) DeleteJob(w http.ResponseWriter, r *http.Request) {
	jobID, _, ok := h.resolveJobWithAccess(w, r, "backups.delete")
	if !ok {
		return
	}
	if err := h.state.Store.DeleteBackupJob(jobID); err != nil {
		sendJSONError(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// TriggerJob POST /api/backup-jobs/{jobId}/trigger
func (h *BackupHandler) TriggerJob(w http.ResponseWriter, r *http.Request) {
	_, job, ok := h.resolveJobWithAccess(w, r, "backups.create")
	if !ok {
		return
	}
	runID, err := h.startBackupRun(r.Context(), job)
	if err != nil {
		sendJSONError(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "runId": runID})
}

// ───────────── Runs ─────────────

// ListRuns GET /api/backup-jobs/{jobId}/runs
func (h *BackupHandler) ListRuns(w http.ResponseWriter, r *http.Request) {
	jobID, _, ok := h.resolveJobWithAccess(w, r, "backups.read")
	if !ok {
		return
	}
	runs, err := h.state.Store.ListBackupRuns(jobID, 50)
	if err != nil {
		sendJSONError(w, "Database error", 500)
		return
	}
	if runs == nil {
		runs = []models.BackupRun{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "runs": runs})
}

// DownloadRun GET /api/backup-runs/{runId}/download
// For S3-backed storage returns 302 to a pre-signed URL. For local storage
// streams the file directly through Core.
func (h *BackupHandler) DownloadRun(w http.ResponseWriter, r *http.Request) {
	runID, err := strconv.Atoi(mux.Vars(r)["runId"])
	if err != nil {
		sendJSONError(w, "Invalid run ID", 400)
		return
	}
	run, err := h.state.Store.GetBackupRun(runID)
	if err != nil {
		sendJSONError(w, "Run not found", 404)
		return
	}
	job, err := h.state.Store.GetBackupJob(run.JobID)
	if err != nil {
		sendJSONError(w, "Job not found", 404)
		return
	}
	if !h.hasServerAccess(r, job.ServerID, "backups.read") {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	if job.StorageID == nil {
		sendJSONError(w, "Job has no storage configured", 400)
		return
	}
	bs, err := h.state.Store.GetBackupStorage(*job.StorageID)
	if err != nil {
		sendJSONError(w, "Storage not found", 404)
		return
	}
	provider, err := backupstorage.Open(r.Context(), bs, h.backupDeps())
	if err != nil {
		sendJSONError(w, err.Error(), 500)
		return
	}

	// Prefer pre-signed URL when supported (S3) — keeps Core out of the data path.
	if url, _ := provider.DownloadURL(r.Context(), run.StorageKey, time.Hour); url != "" {
		http.Redirect(w, r, url, http.StatusFound)
		return
	}

	// Local fallback: stream the file through Core with the right headers.
	reader, err := provider.Get(r.Context(), run.StorageKey)
	if err != nil {
		sendJSONError(w, "Object not found", 404)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="backup-%d.tar.gz"`, run.ID))
	io.Copy(w, reader)
}

// RestoreRun POST /api/backup-runs/{runId}/restore
// Dispatches a restore command to the node. The node stops the affected
// sub-server, streams the archive from storage, extracts in place and
// restarts the container. We don't block the HTTP request on the actual
// restore — it can take minutes for large worlds — the panel polls run
// status instead.
func (h *BackupHandler) RestoreRun(w http.ResponseWriter, r *http.Request) {
	runID, err := strconv.Atoi(mux.Vars(r)["runId"])
	if err != nil {
		sendJSONError(w, "Invalid run ID", 400)
		return
	}
	run, err := h.state.Store.GetBackupRun(runID)
	if err != nil {
		sendJSONError(w, "Run not found", 404)
		return
	}
	if run.Status != "success" {
		sendJSONError(w, "Cannot restore a run that did not complete successfully", 400)
		return
	}
	job, err := h.state.Store.GetBackupJob(run.JobID)
	if err != nil {
		sendJSONError(w, "Job not found", 404)
		return
	}
	if !h.hasServerAccess(r, job.ServerID, "backups.restore") {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	srv, err := h.state.Store.GetServerByID(job.ServerID)
	if err != nil {
		sendJSONError(w, "Server not found", 404)
		return
	}
	node, err := h.state.Store.GetNodeByID(srv.NodeID)
	if err != nil {
		sendJSONError(w, "Node not found", 404)
		return
	}
	if job.StorageID == nil {
		sendJSONError(w, "Job has no storage configured", 400)
		return
	}
	storage, err := h.state.Store.GetBackupStorage(*job.StorageID)
	if err != nil {
		sendJSONError(w, "Storage not found", 404)
		return
	}

	// Track the restore attempt in the DB so the panel can show history
	// even after the node finishes (or fails).
	username := r.Context().Value("username").(string)
	var requestedBy *string
	if user, _ := h.state.Store.GetUserByUsername(username); user != nil {
		v := user.ID
		requestedBy = &v
	}
	restoreID, err := h.state.Store.CreateBackupRestore(&models.BackupRestore{
		RunID:       run.ID,
		ServerID:    job.ServerID,
		RequestedBy: requestedBy,
		Status:      "queued",
	})
	if err != nil {
		sendJSONError(w, "Failed to record restore: "+err.Error(), 500)
		return
	}

	if h.state.Queue == nil {
		h.state.Store.UpdateBackupRestoreStatus(restoreID, "failed", "queue unavailable", time.Now())
		sendJSONError(w, "Queue unavailable", 500)
		return
	}

	storageCfgJSON, presignedGet := services.PrepareNodeStorage(r.Context(), h.state.Store, storage, node, run.StorageKey, "get")
	subServer := ""
	if job.SubServer != nil {
		subServer = *job.SubServer
	}
	payload := map[string]interface{}{
		"action":          "backup_restore",
		"runId":           run.ID,
		"restoreId":       restoreID,
		"jobId":           job.ID,
		"serverUuid":      srv.UUID,
		"subServer":       subServer,
		"storageKey":      run.StorageKey,
		"storage":         json.RawMessage(storageCfgJSON),
		"presignedGetUrl": presignedGet,
	}
	// Publish to the node's durable :cmds stream (BC1) instead of RPush to the
	// retired dylaris:node:<token>:queue list, which nothing reads anymore.
	if err := h.state.Queue.SendRawCommand(r.Context(), node.Token, payload); err != nil {
		h.state.Store.UpdateBackupRestoreStatus(restoreID, "failed", "queue push failed: "+err.Error(), time.Now())
		sendJSONError(w, "Failed to queue restore: "+err.Error(), 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "restoreId": restoreID})
}

// ListRestores GET /api/servers/{id}/backup-restores
// Recent restore history for a server, newest first.
func (h *BackupHandler) ListRestores(w http.ResponseWriter, r *http.Request) {
	serverID, _, ok := h.resolveServer(w, r)
	if !ok {
		return
	}
	restores, err := h.state.Store.ListBackupRestores(serverID, 25)
	if err != nil {
		sendJSONError(w, "Database error", 500)
		return
	}
	if restores == nil {
		restores = []models.BackupRestore{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "restores": restores})
}

// DeleteRun DELETE /api/backup-runs/{runId}
func (h *BackupHandler) DeleteRun(w http.ResponseWriter, r *http.Request) {
	runID, err := strconv.Atoi(mux.Vars(r)["runId"])
	if err != nil {
		sendJSONError(w, "Invalid run ID", 400)
		return
	}
	run, err := h.state.Store.GetBackupRun(runID)
	if err != nil {
		sendJSONError(w, "Run not found", 404)
		return
	}
	job, err := h.state.Store.GetBackupJob(run.JobID)
	if err != nil {
		sendJSONError(w, "Job not found", 404)
		return
	}
	if !h.hasServerAccess(r, job.ServerID, "backups.delete") {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	// Best-effort object delete first; even if the storage is unreachable we
	// still remove the DB row so the UI doesn't keep a phantom entry.
	if job.StorageID != nil {
		if bs, sErr := h.state.Store.GetBackupStorage(*job.StorageID); sErr == nil {
			if provider, pErr := backupstorage.Open(r.Context(), bs, h.backupDeps()); pErr == nil {
				provider.Delete(r.Context(), run.StorageKey)
			}
		}
	}
	if err := h.state.Store.DeleteBackupRun(runID); err != nil {
		sendJSONError(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// BackupUsage GET /api/servers/{id}/backup-usage
//
// Returns the on-disk bytes used by node-local backups for the given
// server, plus archive count. Available regardless of the active backup
// mode — for s3/shared the numbers come back zero, which the Overview
// tab uses to decide whether to render the split storage display.
func (h *BackupHandler) BackupUsage(w http.ResponseWriter, r *http.Request) {
	serverID, srv, ok := h.resolveServer(w, r)
	if !ok {
		return
	}
	_ = serverID

	if h.state.GRPCRegistry == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"usedBytes": 0,
			"count":     0,
		})
		return
	}

	reqID := uuid.NewString()
	msg := &pbNode.NodeMessage{
		RequestId:  reqID,
		ServerUuid: srv.UUID,
		Payload: &pbNode.NodeMessage_BackupUsageReq{
			BackupUsageReq: &pbNode.BackupUsageReq{},
		},
	}
	resp, err := h.state.GRPCRegistry.SendRequest(srv.NodeID, msg, 5*time.Second)
	if err != nil {
		// Node offline or RPC failure — return zeros so the Overview tab
		// degrades gracefully (no quota row instead of an error toast).
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"usedBytes": 0,
			"count":     0,
			"degraded":  true,
		})
		return
	}
	usage := resp.GetBackupUsageResp()
	if usage == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"usedBytes": 0,
			"count":     0,
		})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"usedBytes": usage.UsedBytes,
		"count":     usage.Count,
	})
}

// ───────────── helpers ─────────────

// backupDeps assembles the runtime handles the storage factory passes to
// the node-local and core-storage providers. s3 / shared ignore all of it, so
// the same Deps can be reused everywhere a Storage is opened in this
// handler — keeps the call-sites uniform.
func (h *BackupHandler) backupDeps() backupstorage.Deps {
	return backupstorage.Deps{
		Registry:  h.state.GRPCRegistry,
		NodeStore: h.state.Store,
		// Resolved PER CALL, never cached: the shared Core file storage
		// config can change under a running Core, and every other provider
		// in this codebase resolves per request too.
		CoreStorage: func(subPrefix string) (backupstorage.Storage, error) {
			prov, err := h.state.buildCoreStorageProvider(subPrefix)
			if err != nil {
				return nil, err
			}
			return storage.NewCoreStorageBackupAdapter(prov), nil
		},
	}
}

// resolveServer parses the server ID from the URL and loads the row. Access
// is enforced at the route via RequireCap (routes.go) for every caller of
// this helper, so it only extracts data the handler needs, not authz.
func (h *BackupHandler) resolveServer(w http.ResponseWriter, r *http.Request) (int, *models.Server, bool) {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return 0, nil, false
	}
	srv, err := h.state.Store.GetServerByID(id)
	if err != nil {
		sendJSONError(w, "Server not found", 404)
		return 0, nil, false
	}
	return id, srv, true
}

// resolveJobWithAccess loads the job and checks capID against the job's
// server. The /backup-jobs/{jobId} family resolves its server from the job
// row rather than a path {id}/{uuid}, so RequireCap cannot gate it at the
// route (Rule R5) - this stays in-handler, routed through the same resolver.
func (h *BackupHandler) resolveJobWithAccess(w http.ResponseWriter, r *http.Request, capID string) (int, *models.BackupJob, bool) {
	jobID, err := strconv.Atoi(mux.Vars(r)["jobId"])
	if err != nil {
		sendJSONError(w, "Invalid job ID", 400)
		return 0, nil, false
	}
	job, err := h.state.Store.GetBackupJob(jobID)
	if err != nil {
		sendJSONError(w, "Job not found", 404)
		return 0, nil, false
	}
	if !h.hasServerAccess(r, job.ServerID, capID) {
		sendJSONError(w, "Forbidden", 403)
		return 0, nil, false
	}
	return jobID, job, true
}

// hasServerAccess routes through the same capability resolver every
// route-gated handler uses: owner short-circuit, or a direct/proxy/account
// grant holding capID.
func (h *BackupHandler) hasServerAccess(r *http.Request, serverID int, capID string) bool {
	username, _ := r.Context().Value("username").(string)
	isAdmin, _ := r.Context().Value("isAdmin").(bool)
	userID, _ := r.Context().Value("userID").(string)
	res, err := h.state.Authz.Resolve(authz.Identity{UserID: userID, Username: username, IsAdmin: isAdmin}, serverID)
	return err == nil && res.HasCap(capID)
}

// startBackupRun creates a backup_run row, dispatches a node command, and
// returns the new run-id. The actual heavy lifting happens on the node; this
// function returns immediately so the HTTP request stays snappy.
func (h *BackupHandler) startBackupRun(ctx context.Context, job *models.BackupJob) (int, error) {
	if job.StorageID == nil {
		def, _ := h.state.Store.GetDefaultBackupStorage()
		if def == nil {
			return 0, fmt.Errorf("no storage configured — set a default in Settings → Backups first")
		}
		job.StorageID = &def.ID
	}
	storage, err := h.state.Store.GetBackupStorage(*job.StorageID)
	if err != nil {
		return 0, fmt.Errorf("storage not found: %w", err)
	}
	srv, err := h.state.Store.GetServerByID(job.ServerID)
	if err != nil {
		return 0, fmt.Errorf("server not found: %w", err)
	}
	// R2 backup quota: refuse a new backup once the tenant is at/over quota
	// (0/unset = unlimited, so solo/hoster is unaffected).
	if exceeded, used, quota := services.R2QuotaExceeded(h.state.Store, srv.OwnerID); exceeded {
		return 0, fmt.Errorf("backup quota reached (%d / %d GB used) — delete old backups or raise the limit",
			used/(1024*1024*1024), quota/(1024*1024*1024))
	}
	node, err := h.state.Store.GetNodeByID(srv.NodeID)
	if err != nil {
		return 0, fmt.Errorf("node not found: %w", err)
	}

	storageKey := fmt.Sprintf("backups/%s/job-%d/%s.tar.gz", srv.UUID, job.ID, time.Now().UTC().Format("20060102-150405"))
	runID, err := h.state.Store.CreateBackupRun(&models.BackupRun{
		JobID:      job.ID,
		Status:     "running",
		StorageKey: storageKey,
	})
	if err != nil {
		return 0, err
	}

	// Mark scheduled bookkeeping. Manual triggers don't change the next-run.
	now := time.Now()
	next := computeNextRun(job.Schedule, now)
	if next != nil {
		h.state.Store.SetBackupJobScheduled(job.ID, now, *next)
	}

	if h.state.Queue == nil {
		return runID, fmt.Errorf("queue unavailable")
	}
	// BYON nodes get a presigned PUT URL + creds-stripped storage so the tenant's
	// machine never receives the bucket credentials. Operator nodes are unchanged.
	storageCfgJSON, presignedPut := services.PrepareNodeStorage(ctx, h.state.Store, storage, node, storageKey, "put")
	payload := map[string]interface{}{
		"action":          "backup_run",
		"runId":           runID,
		"jobId":           job.ID,
		"serverUuid":      srv.UUID,
		"subServer":       deref(job.SubServer),
		"includePatterns": job.IncludePatterns,
		"excludePatterns": job.ExcludePatterns,
		"storageKey":      storageKey,
		"storage":         json.RawMessage(storageCfgJSON),
		"presignedPutUrl": presignedPut,
	}
	// Publish to the node's durable :cmds stream (BC1) instead of RPush to the
	// retired dylaris:node:<token>:queue list, which nothing reads anymore.
	if err := h.state.Queue.SendRawCommand(ctx, node.Token, payload); err != nil {
		h.state.Store.UpdateBackupRunStatus(runID, "failed", "queue push failed: "+err.Error(), 0, "", time.Now())
		return runID, err
	}
	return runID, nil
}

// computeNextRun parses simple schedule expressions: "manual", "every Nh",
// "every Nd". Returns nil for manual. Anything malformed → nil (caller falls
// back to manual semantics).
func computeNextRun(schedule string, from time.Time) *time.Time {
	if schedule == "" || schedule == "manual" {
		return nil
	}
	var n int
	var unit string
	if _, err := fmt.Sscanf(schedule, "every %d%s", &n, &unit); err != nil || n <= 0 {
		return nil
	}
	var d time.Duration
	switch unit {
	case "h":
		d = time.Duration(n) * time.Hour
	case "d":
		d = time.Duration(n) * 24 * time.Hour
	default:
		return nil
	}
	next := from.Add(d)
	return &next
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// strReader is a tiny helper used by TestStorage probe.
func strReader(s string) io.Reader {
	return &stringReader{s: s}
}

type stringReader struct {
	s   string
	off int
}

func (r *stringReader) Read(p []byte) (int, error) {
	if r.off >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.off:])
	r.off += n
	return n, nil
}
