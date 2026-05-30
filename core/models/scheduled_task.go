package models

import "time"

// ScheduledTask is a per-server cron job. Phase 8 ships two task types:
//   - "restart" — payload unused, dispatches the same pipeline as the
//     Restart power button
//   - "say"     — payload is the chat message, executed as `say <payload>`
//     via container stdin
//
// schedule_cron is standard 5-field cron (min hour dom mon dow) parsed by
// github.com/robfig/cron/v3 with default settings (no seconds, UTC).
//
// LastStatus values: "" (never run) | "ok" | "error". LastError is human-
// readable explanation when LastStatus == "error".
type ScheduledTask struct {
	ID           int        `json:"id"`
	ServerID     int        `json:"serverId"`
	Name         string     `json:"name"`
	TaskType     string     `json:"taskType"`
	ScheduleCron string     `json:"scheduleCron"`
	Payload      string     `json:"payload"`
	Enabled      bool       `json:"enabled"`
	NextRun      *time.Time `json:"nextRun,omitempty"`
	LastRun      *time.Time `json:"lastRun,omitempty"`
	LastStatus   string     `json:"lastStatus"`
	LastError    string     `json:"lastError,omitempty"`
	CreatedBy    *string    `json:"createdBy,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}
