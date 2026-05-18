package store

import (
	"database/sql"
	"dylaris-core/models"
	"encoding/json"
	"time"

	"github.com/lib/pq"
)

// ───────────── Storages ─────────────

func (s *PostgresStore) ListBackupStorages() ([]models.BackupStorage, error) {
	rows, err := s.db.Query(`SELECT id, name, provider, config, is_default, created_at FROM backup_storages ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.BackupStorage
	for rows.Next() {
		var bs models.BackupStorage
		var cfg []byte
		if err := rows.Scan(&bs.ID, &bs.Name, &bs.Provider, &cfg, &bs.IsDefault, &bs.CreatedAt); err != nil {
			continue
		}
		bs.Config = json.RawMessage(cfg)
		out = append(out, bs)
	}
	return out, nil
}

func (s *PostgresStore) GetBackupStorage(id int) (*models.BackupStorage, error) {
	var bs models.BackupStorage
	var cfg []byte
	err := s.db.QueryRow(`SELECT id, name, provider, config, is_default, created_at FROM backup_storages WHERE id = $1`, id).
		Scan(&bs.ID, &bs.Name, &bs.Provider, &cfg, &bs.IsDefault, &bs.CreatedAt)
	if err != nil {
		return nil, err
	}
	bs.Config = json.RawMessage(cfg)
	return &bs, nil
}

func (s *PostgresStore) GetDefaultBackupStorage() (*models.BackupStorage, error) {
	var bs models.BackupStorage
	var cfg []byte
	err := s.db.QueryRow(`SELECT id, name, provider, config, is_default, created_at FROM backup_storages WHERE is_default = TRUE LIMIT 1`).
		Scan(&bs.ID, &bs.Name, &bs.Provider, &cfg, &bs.IsDefault, &bs.CreatedAt)
	if err == sql.ErrNoRows {
		// Fall back to the first configured storage when no default is set.
		err = s.db.QueryRow(`SELECT id, name, provider, config, is_default, created_at FROM backup_storages ORDER BY id LIMIT 1`).
			Scan(&bs.ID, &bs.Name, &bs.Provider, &cfg, &bs.IsDefault, &bs.CreatedAt)
		if err == sql.ErrNoRows {
			return nil, nil
		}
	}
	if err != nil {
		return nil, err
	}
	bs.Config = json.RawMessage(cfg)
	return &bs, nil
}

func (s *PostgresStore) CreateBackupStorage(bs *models.BackupStorage) (int, error) {
	cfg := []byte(bs.Config)
	if len(cfg) == 0 {
		cfg = []byte("{}")
	}
	if bs.IsDefault {
		s.db.Exec(`UPDATE backup_storages SET is_default = FALSE WHERE is_default = TRUE`)
	}
	var id int
	err := s.db.QueryRow(
		`INSERT INTO backup_storages (name, provider, config, is_default) VALUES ($1, $2, $3::jsonb, $4) RETURNING id`,
		bs.Name, bs.Provider, cfg, bs.IsDefault,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) UpdateBackupStorage(bs *models.BackupStorage) error {
	cfg := []byte(bs.Config)
	if len(cfg) == 0 {
		cfg = []byte("{}")
	}
	if bs.IsDefault {
		s.db.Exec(`UPDATE backup_storages SET is_default = FALSE WHERE is_default = TRUE AND id != $1`, bs.ID)
	}
	_, err := s.db.Exec(
		`UPDATE backup_storages SET name = $1, provider = $2, config = $3::jsonb, is_default = $4 WHERE id = $5`,
		bs.Name, bs.Provider, cfg, bs.IsDefault, bs.ID,
	)
	return err
}

func (s *PostgresStore) DeleteBackupStorage(id int) error {
	_, err := s.db.Exec(`DELETE FROM backup_storages WHERE id = $1`, id)
	return err
}

// ───────────── Jobs ─────────────

func (s *PostgresStore) scanJob(row interface{ Scan(...interface{}) error }) (*models.BackupJob, error) {
	var j models.BackupJob
	var subServer sql.NullString
	var storageID sql.NullInt64
	var lastRun, nextRun sql.NullTime
	var includes, excludes pq.StringArray
	err := row.Scan(
		&j.ID, &j.ServerID, &subServer, &j.Name, &j.Schedule,
		&includes, &excludes, &j.RetentionCount,
		&storageID, &j.Enabled, &lastRun, &nextRun, &j.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if subServer.Valid {
		s := subServer.String
		j.SubServer = &s
	}
	if storageID.Valid {
		id := int(storageID.Int64)
		j.StorageID = &id
	}
	if lastRun.Valid {
		t := lastRun.Time
		j.LastRunAt = &t
	}
	if nextRun.Valid {
		t := nextRun.Time
		j.NextRunAt = &t
	}
	j.IncludePatterns = []string(includes)
	j.ExcludePatterns = []string(excludes)
	return &j, nil
}

const backupJobCols = `id, server_id, sub_server, name, schedule, include_patterns, exclude_patterns, retention_count, storage_id, enabled, last_run_at, next_run_at, created_at`

func (s *PostgresStore) ListBackupJobs(serverID int) ([]models.BackupJob, error) {
	rows, err := s.db.Query(`SELECT `+backupJobCols+` FROM backup_jobs WHERE server_id = $1 ORDER BY id`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.BackupJob
	for rows.Next() {
		j, err := s.scanJob(rows)
		if err != nil {
			continue
		}
		out = append(out, *j)
	}
	return out, nil
}

func (s *PostgresStore) GetBackupJob(id int) (*models.BackupJob, error) {
	row := s.db.QueryRow(`SELECT `+backupJobCols+` FROM backup_jobs WHERE id = $1`, id)
	return s.scanJob(row)
}

func (s *PostgresStore) CreateBackupJob(j *models.BackupJob) (int, error) {
	var id int
	err := s.db.QueryRow(
		`INSERT INTO backup_jobs (server_id, sub_server, name, schedule, include_patterns, exclude_patterns, retention_count, storage_id, enabled, next_run_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		j.ServerID, nullableString(j.SubServer), j.Name, j.Schedule,
		pq.Array(j.IncludePatterns), pq.Array(j.ExcludePatterns),
		j.RetentionCount, nullableInt(j.StorageID), j.Enabled, nullableTime(j.NextRunAt),
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) UpdateBackupJob(j *models.BackupJob) error {
	_, err := s.db.Exec(
		`UPDATE backup_jobs SET sub_server = $1, name = $2, schedule = $3, include_patterns = $4,
		 exclude_patterns = $5, retention_count = $6, storage_id = $7, enabled = $8, next_run_at = $9 WHERE id = $10`,
		nullableString(j.SubServer), j.Name, j.Schedule,
		pq.Array(j.IncludePatterns), pq.Array(j.ExcludePatterns),
		j.RetentionCount, nullableInt(j.StorageID), j.Enabled, nullableTime(j.NextRunAt), j.ID,
	)
	return err
}

func (s *PostgresStore) DeleteBackupJob(id int) error {
	_, err := s.db.Exec(`DELETE FROM backup_jobs WHERE id = $1`, id)
	return err
}

func (s *PostgresStore) ListDueBackupJobs(now time.Time) ([]models.BackupJob, error) {
	rows, err := s.db.Query(
		`SELECT `+backupJobCols+` FROM backup_jobs WHERE enabled = TRUE AND next_run_at IS NOT NULL AND next_run_at <= $1`,
		now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.BackupJob
	for rows.Next() {
		j, err := s.scanJob(rows)
		if err != nil {
			continue
		}
		out = append(out, *j)
	}
	return out, nil
}

func (s *PostgresStore) SetBackupJobScheduled(jobID int, lastRun, nextRun time.Time) error {
	_, err := s.db.Exec(`UPDATE backup_jobs SET last_run_at = $1, next_run_at = $2 WHERE id = $3`, lastRun, nextRun, jobID)
	return err
}

// ───────────── Runs ─────────────

func (s *PostgresStore) scanRun(row interface{ Scan(...interface{}) error }) (*models.BackupRun, error) {
	var r models.BackupRun
	var completed sql.NullTime
	err := row.Scan(&r.ID, &r.JobID, &r.StartedAt, &completed, &r.Status, &r.SizeBytes, &r.StorageKey, &r.ErrorMessage)
	if err != nil {
		return nil, err
	}
	if completed.Valid {
		t := completed.Time
		r.CompletedAt = &t
	}
	return &r, nil
}

const backupRunCols = `id, job_id, started_at, completed_at, status, size_bytes, storage_key, error_message`

func (s *PostgresStore) ListBackupRuns(jobID, limit int) ([]models.BackupRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT `+backupRunCols+` FROM backup_runs WHERE job_id = $1 ORDER BY started_at DESC LIMIT $2`, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.BackupRun
	for rows.Next() {
		r, err := s.scanRun(rows)
		if err != nil {
			continue
		}
		out = append(out, *r)
	}
	return out, nil
}

func (s *PostgresStore) GetBackupRun(id int) (*models.BackupRun, error) {
	row := s.db.QueryRow(`SELECT `+backupRunCols+` FROM backup_runs WHERE id = $1`, id)
	return s.scanRun(row)
}

func (s *PostgresStore) CreateBackupRun(r *models.BackupRun) (int, error) {
	var id int
	err := s.db.QueryRow(
		`INSERT INTO backup_runs (job_id, status, storage_key) VALUES ($1, $2, $3) RETURNING id`,
		r.JobID, r.Status, r.StorageKey,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) UpdateBackupRunStatus(id int, status, errorMsg string, sizeBytes int64, storageKey string, completed time.Time) error {
	var completedArg interface{} = completed
	if completed.IsZero() {
		completedArg = nil
	}
	_, err := s.db.Exec(
		`UPDATE backup_runs SET status = $1, error_message = $2, size_bytes = $3, storage_key = $4, completed_at = $5 WHERE id = $6`,
		status, errorMsg, sizeBytes, storageKey, completedArg, id,
	)
	return err
}

func (s *PostgresStore) DeleteBackupRun(id int) error {
	_, err := s.db.Exec(`DELETE FROM backup_runs WHERE id = $1`, id)
	return err
}

func (s *PostgresStore) PruneOldBackupRuns(jobID, keep int) ([]models.BackupRun, error) {
	if keep < 0 {
		keep = 0
	}
	rows, err := s.db.Query(
		`SELECT `+backupRunCols+` FROM backup_runs
		 WHERE job_id = $1 AND status = 'success'
		 ORDER BY started_at DESC
		 OFFSET $2`,
		jobID, keep,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var toPrune []models.BackupRun
	for rows.Next() {
		r, err := s.scanRun(rows)
		if err != nil {
			continue
		}
		toPrune = append(toPrune, *r)
	}
	for _, r := range toPrune {
		s.db.Exec(`DELETE FROM backup_runs WHERE id = $1`, r.ID)
	}
	return toPrune, nil
}

// ───────────── Restores ─────────────

func (s *PostgresStore) scanRestore(row interface{ Scan(...interface{}) error }) (*models.BackupRestore, error) {
	var r models.BackupRestore
	var requestedBy sql.NullInt64
	var completed sql.NullTime
	err := row.Scan(&r.ID, &r.RunID, &r.ServerID, &requestedBy, &r.RequestedAt, &completed, &r.Status, &r.ErrorMessage)
	if err != nil {
		return nil, err
	}
	if requestedBy.Valid {
		v := int(requestedBy.Int64)
		r.RequestedBy = &v
	}
	if completed.Valid {
		t := completed.Time
		r.CompletedAt = &t
	}
	return &r, nil
}

const backupRestoreCols = `id, run_id, server_id, requested_by, requested_at, completed_at, status, error_message`

func (s *PostgresStore) CreateBackupRestore(r *models.BackupRestore) (int, error) {
	var id int
	var requestedBy interface{} = nil
	if r.RequestedBy != nil {
		requestedBy = *r.RequestedBy
	}
	err := s.db.QueryRow(
		`INSERT INTO backup_restores (run_id, server_id, requested_by, status) VALUES ($1, $2, $3, $4) RETURNING id`,
		r.RunID, r.ServerID, requestedBy, r.Status,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) GetBackupRestore(id int) (*models.BackupRestore, error) {
	row := s.db.QueryRow(`SELECT `+backupRestoreCols+` FROM backup_restores WHERE id = $1`, id)
	return s.scanRestore(row)
}

func (s *PostgresStore) ListBackupRestores(serverID, limit int) ([]models.BackupRestore, error) {
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.db.Query(
		`SELECT `+backupRestoreCols+` FROM backup_restores WHERE server_id = $1 ORDER BY requested_at DESC LIMIT $2`,
		serverID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.BackupRestore
	for rows.Next() {
		r, err := s.scanRestore(rows)
		if err != nil {
			continue
		}
		out = append(out, *r)
	}
	return out, nil
}

func (s *PostgresStore) UpdateBackupRestoreStatus(id int, status, errorMsg string, completed time.Time) error {
	var completedArg interface{} = completed
	if completed.IsZero() {
		completedArg = nil
	}
	_, err := s.db.Exec(
		`UPDATE backup_restores SET status = $1, error_message = $2, completed_at = $3 WHERE id = $4`,
		status, errorMsg, completedArg, id,
	)
	return err
}

// ───────────── helpers ─────────────

func nullableString(s *string) interface{} {
	if s == nil {
		return nil
	}
	return *s
}
func nullableInt(i *int) interface{} {
	if i == nil {
		return nil
	}
	return *i
}
func nullableTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return *t
}
