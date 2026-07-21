package models

import (
	"encoding/json"
	"time"
)

// BackupStorage is an admin-configured target for backup archives.
type BackupStorage struct {
	ID        int             `json:"id"`
	Name      string          `json:"name"`
	Provider  string          `json:"provider"` // "local"|"shared" | "s3" | "node-local" | "core-storage"
	Config    json.RawMessage `json:"config"`
	IsDefault bool            `json:"isDefault"`
	CreatedAt time.Time       `json:"createdAt"`

	// SecretSet reports, on a read response, that an s3 secret is stored
	// WITHOUT returning it. It is transient: set by the store on read from the
	// encrypted secret_enc column (or a legacy plaintext secret still in
	// config), never persisted. The secret itself lives encrypted in secret_enc
	// and is stripped from Config on the list path, so a settings.read holder
	// can no longer harvest every backup credential over the API.
	SecretSet bool `json:"secretSet,omitempty"`
}

// BackupJob describes a recurring or manual backup configuration.
// Schedule is a free-form string; "manual" disables auto-runs.
// Otherwise expected formats: "every Nh", "every Nd" (parsed by scheduler).
type BackupJob struct {
	ID              int        `json:"id"`
	ServerID        int        `json:"serverId"`
	SubServer       *string    `json:"subServer,omitempty"` // NULL = full container
	Name            string     `json:"name"`
	Schedule        string     `json:"schedule"`
	IncludePatterns []string   `json:"includePatterns"`
	ExcludePatterns []string   `json:"excludePatterns"`
	RetentionCount  int        `json:"retentionCount"`
	StorageID       *int       `json:"storageId,omitempty"`
	Enabled         bool       `json:"enabled"`
	LastRunAt       *time.Time `json:"lastRunAt,omitempty"`
	NextRunAt       *time.Time `json:"nextRunAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// BackupRun records a single execution (in-progress or completed).
type BackupRun struct {
	ID           int        `json:"id"`
	JobID        int        `json:"jobId"`
	StartedAt    time.Time  `json:"startedAt"`
	CompletedAt  *time.Time `json:"completedAt,omitempty"`
	Status       string     `json:"status"` // running | success | failed
	SizeBytes    int64      `json:"sizeBytes"`
	StorageKey   string     `json:"storageKey"`
	ErrorMessage string     `json:"errorMessage"`
}

// BackupRestore records a restore attempt against an archived BackupRun.
type BackupRestore struct {
	ID            int        `json:"id"`
	RunID         int        `json:"runId"`
	ServerID      int        `json:"serverId"`
	RequestedBy   *string    `json:"requestedBy,omitempty"`
	RequestedAt   time.Time  `json:"requestedAt"`
	CompletedAt   *time.Time `json:"completedAt,omitempty"`
	Status        string     `json:"status"` // queued | running | success | failed
	ErrorMessage  string     `json:"errorMessage"`
}
