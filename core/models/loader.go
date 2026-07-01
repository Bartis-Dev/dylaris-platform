package models

import "time"

// Loader status values for the loaders cache table.
const (
	LoaderStatusPending = "pending"
	LoaderStatusReady   = "ready"
	LoaderStatusFailed  = "failed"
)

// Loader is one cached launcher-side loader "magic-zip", keyed by the unique
// (Minecraft, Loader, LoaderVersion) triple and shared across all packs. For
// Fabric/Quilt the stored artifact is a zip containing a single bin/version.json.
type Loader struct {
	ID               int        `json:"id"`
	Minecraft        string     `json:"minecraft"`
	Loader           string     `json:"loader"`
	LoaderVersion    string     `json:"loaderVersion"`
	ClientStorageKey string     `json:"clientStorageKey"`
	MD5              string     `json:"md5"`
	Filesize         int64      `json:"filesize"`
	BuildStatus      string     `json:"buildStatus"`
	BuildError       string     `json:"buildError"`
	BuiltAt          *time.Time `json:"builtAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}
