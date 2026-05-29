package models

import "time"

// Modpack channel constants for the 3-stage publishing model:
//   draft   — local only, never on Modrinth
//   beta    — Modrinth as version_type="beta", project visibility usually
//             "unlisted" with explicit collaborators for testing
//   release — Modrinth as version_type="release", visibility per the
//             pack's modrinth_visibility column
const (
	ModpackChannelDraft   = "draft"
	ModpackChannelBeta    = "beta"
	ModpackChannelRelease = "release"
)

type Modpack struct {
	ID                 int       `json:"id"`
	OwnerID            int       `json:"ownerId"`
	Name               string    `json:"name"`
	Slug               string    `json:"slug"`
	Summary            string    `json:"summary"`
	McVersion          string    `json:"mcVersion"`
	Loader             string    `json:"loader"`
	ModrinthProjectID  string    `json:"modrinthProjectId"`
	ModrinthVisibility string    `json:"modrinthVisibility"` // unlisted|listed
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type ModpackVersion struct {
	ID                int        `json:"id"`
	ModpackID         int        `json:"modpackId"`
	VersionString     string     `json:"versionString"`
	Channel           string     `json:"channel"`
	Changelog         string     `json:"changelog"`
	MrpackStoragePath string     `json:"-"`
	FileSize          int64      `json:"fileSize"`
	ModrinthVersionID string     `json:"modrinthVersionId"`
	CreatedAt         time.Time  `json:"createdAt"`
	PublishedAt       *time.Time `json:"publishedAt,omitempty"`
}

type ModpackMod struct {
	ID                  int    `json:"id"`
	ModpackVersionID    int    `json:"modpackVersionId"`
	ModrinthProjectID   string `json:"modrinthProjectId"`
	ModrinthProjectSlug string `json:"modrinthProjectSlug"`
	ModrinthVersionID   string `json:"modrinthVersionId"`
	Title               string `json:"title"`
	FileName            string `json:"fileName"`
	DownloadURL         string `json:"downloadUrl"`
	SHA512              string `json:"sha512"`
	Side                string `json:"side"` // client|server|both
	Required            bool   `json:"required"`
}

// ModrinthPAT mirrors a row of modrinth_pats. Plaintext PAT is never on the
// struct — the encrypted ciphertext stays in storage and Decrypt produces a
// short-lived plaintext for outgoing Modrinth API calls only.
type ModrinthPAT struct {
	UserID           int        `json:"userId"`
	Ciphertext       string     `json:"-"`
	ModrinthUsername string     `json:"modrinthUsername"`
	LastValidatedAt  *time.Time `json:"lastValidatedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}
