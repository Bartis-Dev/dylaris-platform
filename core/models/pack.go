package models

import "time"

// Content types (what a mods row represents inside a Solder-style pack).
const (
	ContentTypeMod         = "mod"
	ContentTypeResourcepack = "resourcepack"
	ContentTypeShaderpack  = "shaderpack"
	ContentTypeConfig      = "config"
	ContentTypeOther       = "other"
)

// Per-build side (maps to Solder inclusion and mrpack env).
const (
	SideClient = "client"
	SideServer = "server"
	SideBoth   = "both"
)

// Build channels (mirrors the old modpack channels).
const (
	ChannelDraft   = "draft"
	ChannelBeta    = "beta"
	ChannelRelease = "release"
)

// Where a modversion's bytes originate.
const (
	SourceModrinth = "modrinth"
	SourceUpload   = "upload"
	SourceLoader   = "loader"
)

// ModrinthPAT mirrors a row of modrinth_pats. Plaintext PAT is never on the
// struct — the encrypted ciphertext stays in storage and Decrypt produces a
// short-lived plaintext for outgoing Modrinth API calls only. Kept alongside
// the unified pack model because Modrinth publishing (a later phase) reuses it.
type ModrinthPAT struct {
	UserID           string     `json:"userId"`
	Ciphertext       string     `json:"-"`
	ModrinthUsername string     `json:"modrinthUsername"`
	LastValidatedAt  *time.Time `json:"lastValidatedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

// Pack is a modpack. It carries separate Solder and Modrinth identities so the
// same pack can be published to either launcher with its own name/link.
type Pack struct {
	ID                  int       `json:"id"`
	OwnerID             string    `json:"ownerId"`
	InternalName        string    `json:"internalName"`
	InternalSlug        string    `json:"internalSlug"`
	Summary             string    `json:"summary"`
	SolderDisplayName   string    `json:"solderDisplayName"`
	SolderSlug          string    `json:"solderSlug"`
	Hidden              bool      `json:"hidden"`
	Private             bool      `json:"private"`
	RecommendedBuild    string    `json:"recommendedBuild"`
	LatestBuild         string    `json:"latestBuild"`
	IconURL             string    `json:"iconUrl"`
	LogoURL             string    `json:"logoUrl"`
	BackgroundURL       string    `json:"backgroundUrl"`
	IconMD5             string    `json:"iconMd5"`
	LogoMD5             string    `json:"logoMd5"`
	BackgroundMD5       string    `json:"backgroundMd5"`
	ModrinthProjectID   string    `json:"modrinthProjectId"`
	ModrinthProjectName string    `json:"modrinthProjectName"`
	ModrinthVisibility  string    `json:"modrinthVisibility"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

// PackBuild is one version of a pack. MC + loader live here (not on the pack)
// so different builds can target different Minecraft versions.
type PackBuild struct {
	ID                int        `json:"id"`
	PackID            int        `json:"packId"`
	VersionString     string     `json:"versionString"`
	Minecraft         string     `json:"minecraft"`
	Loader            string     `json:"loader"`
	LoaderVersion     string     `json:"loaderVersion"`
	MinJava           string     `json:"minJava"`
	MinMemory         int        `json:"minMemory"`
	Changelog         string     `json:"changelog"`
	Channel           string     `json:"channel"`
	Frozen            bool       `json:"frozen"`
	SolderPublished   bool       `json:"solderPublished"`
	SolderPrivate     bool       `json:"solderPrivate"`
	ModrinthPublished bool       `json:"modrinthPublished"`
	ModrinthVersionID string     `json:"modrinthVersionId"`
	MrpackStorageKey  string     `json:"mrpackStorageKey"`
	MrpackSHA256      string     `json:"mrpackSha256"`
	CreatedAt         time.Time  `json:"createdAt"`
	PublishedAt       *time.Time `json:"publishedAt,omitempty"`
}

// Mod is an owner-scoped content catalog entry (the Solder "mod" unit; also
// holds resourcepacks/shaders/config bundles via ContentType).
type Mod struct {
	ID          int    `json:"id"`
	OwnerID     string `json:"ownerId"`
	Slug        string `json:"slug"`
	PrettyName  string `json:"prettyName"`
	Author      string `json:"author"`
	Description string `json:"description"`
	Link        string `json:"link"`
	ContentType string `json:"contentType"`
}

// Modversion is one concrete artifact version of a Mod.
type Modversion struct {
	ID                      int        `json:"id"`
	ModID                   int        `json:"modId"`
	Version                 string     `json:"version"`
	Filesize                int64      `json:"filesize"`
	StorageKey              string     `json:"storageKey"`
	MD5                     string     `json:"md5"`
	SHA1                    string     `json:"sha1"`
	SHA512                  string     `json:"sha512"`
	URLOverride             string     `json:"urlOverride"`
	Source                  string     `json:"source"`
	TargetPath              string     `json:"targetPath"`
	ModrinthProjectID       string     `json:"modrinthProjectId"`
	ModrinthDownloadURL     string     `json:"modrinthDownloadUrl"`
	ModrinthVersionID       string     `json:"modrinthVersionId"`
	ModrinthVersionNumber   string     `json:"modrinthVersionNumber"`
	ModrinthGameVersions    string     `json:"modrinthGameVersions"`
	ModrinthLatestVersionID string     `json:"modrinthLatestVersionId"`
	ModrinthLastChecked     *time.Time `json:"modrinthLastChecked,omitempty"`
	CreatedAt               time.Time  `json:"createdAt"`
	UpdatedAt               time.Time  `json:"updatedAt"`
}

// BuildContentEntry is the flattened join row the builder UI renders: a
// modversion plus its owning mod's display fields plus the per-build side.
type BuildContentEntry struct {
	Modversion
	Side        string `json:"side"`
	ModSlug     string `json:"modSlug"`
	PrettyName  string `json:"prettyName"`
	ContentType string `json:"contentType"`
	// Linked is true when this entry resolves to a Modrinth project (clean
	// files[] reference + auto-update). false => manual upload (Modrinth warn).
	Linked bool `json:"linked"`
}
