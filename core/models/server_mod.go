package models

import "time"

// ServerMod is one installed mod/plugin on a server. We track Modrinth
// project + version IDs so the panel can compare against the latest version
// for "Update available" detection without re-hitting Modrinth on every
// page load (the cache layer on /api/modrinth/* covers re-checks).
type ServerMod struct {
	ID                  int       `json:"id"`
	ServerID            int       `json:"serverId"`
	SubServerName       string    `json:"subServerName"`
	ModrinthProjectID   string    `json:"modrinthProjectId"`
	ModrinthProjectSlug string    `json:"modrinthProjectSlug"`
	ModrinthVersionID   string    `json:"modrinthVersionId"`
	Title               string    `json:"title"`
	FileName            string    `json:"fileName"`
	// TargetDir is the directory the jar was installed into ("mods"/"plugins").
	// Empty on rows written before the column existed; the uninstall path then
	// falls back to deriving it from the loader, which is how they were placed.
	TargetDir           string    `json:"targetDir"`
	SHA512              string    `json:"sha512"`
	InstalledAt         time.Time `json:"installedAt"`
	InstalledBy         *string   `json:"installedBy,omitempty"`
}
