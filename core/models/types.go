package models

import (
	"time"
)

type User struct {
	ID                int       `json:"id"`
	Username          string    `json:"username"`
	Password          string    `json:"password,omitempty"`
	Email             string    `json:"email"`
	MinecraftUsername string    `json:"minecraftUsername"`
	IsAdmin           bool      `json:"isAdmin"`
	Is2FAEnabled      bool      `json:"is2FAEnabled"`
	TOTPSecret        string    `json:"-"` // never sent to clients
	TOTPBackupCodes   string    `json:"-"` // JSON array of bcrypt-hashed codes
	Permissions       string    `json:"permissions"`
	PublicID          string    `json:"publicId"`
	CreatedAt         time.Time `json:"createdAt"`

	// Region access (Phase 0a.1)
	// AllRegionsAccess=true means "all regions, current and future" — overrides Regions.
	// Regions is populated on demand by handlers that need it (not by default scanUser).
	AllRegionsAccess bool     `json:"allRegionsAccess"`
	Regions          []string `json:"regions,omitempty"`

	// Phase 1 — role + granular capability flags. is_admin (above) is kept
	// in sync with role for backward-compat with handlers that read it.
	Role               string `json:"role"`
	CanDeleteServers   bool   `json:"canDeleteServers"`
	CanChangeResources bool   `json:"canChangeResources"`
	SupportTeam        string `json:"supportTeam,omitempty"`

	// Verification / lifecycle (Phase 0a.1, used in later sub-phases)
	EmailVerifiedAt          *time.Time `json:"emailVerifiedAt,omitempty"`
	EmailVerificationToken   string     `json:"-"`
	EmailVerificationSentAt  *time.Time `json:"-"`
	PasswordResetToken       string     `json:"-"`
	PasswordResetExpiresAt   *time.Time `json:"-"`
	LastLoginAt              *time.Time `json:"lastLoginAt,omitempty"`
	DeletionStatus           string     `json:"deletionStatus"`
	DeletionWarningSentAt    *time.Time `json:"deletionWarningSentAt,omitempty"`
	DeletionScheduledAt      *time.Time `json:"deletionScheduledAt,omitempty"`
}

// Region is a geographic deployment region. Single-region setups have one
// row with id='default'; multi-region adds 'eu', 'us-east' etc. The id is
// used as the value of nodes.region / servers.region.
type Region struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"displayName"`
	Enabled     bool      `json:"enabled"`
	Color       string    `json:"color,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// AuditEventIdentity is a single append-only audit row for identity-domain
// events (registration, login, role change, deletion, settings change, etc.).
type AuditEventIdentity struct {
	ID           int64                  `json:"id"`
	EventType    string                 `json:"eventType"`
	ActorUserID  *int                   `json:"actorUserId,omitempty"`
	TargetUserID *int                   `json:"targetUserId,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	IPAddress    string                 `json:"ipAddress,omitempty"`
	UserAgent    string                 `json:"userAgent,omitempty"`
	CreatedAt    time.Time              `json:"createdAt"`
}

// ── Phase 2 — Tickets ─────────────────────────────────────────────────

// TicketCategory is an admin-curated category. RequiresServer toggles the
// server-picker step in the create form. DefaultAssigneeTeam pre-populates
// the team string on new tickets — drives the cross-team visibility scope.
type TicketCategory struct {
	ID                  int       `json:"id"`
	Name                string    `json:"name"`
	Description         string    `json:"description"`
	RequiresServer      bool      `json:"requiresServer"`
	DefaultPriority     string    `json:"defaultPriority"`
	DefaultAssigneeTeam string    `json:"defaultAssigneeTeam,omitempty"`
	Color               string    `json:"color,omitempty"`
	Enabled             bool      `json:"enabled"`
	Position            int       `json:"position"`
	CreatedAt           time.Time `json:"createdAt"`
}

// Ticket is the canonical ticket row. ServerUUID/ServerRegion are nullable
// — only set when the category requires a server. AssignedUserID is the
// supporter currently responsible; AssignedTeam carries the Phase-1
// support_team string and drives the cross-team visibility scope.
type Ticket struct {
	ID             int        `json:"id"`
	Region         string     `json:"region"`
	CategoryID     int        `json:"categoryId"`
	CategoryName   string     `json:"categoryName,omitempty"`
	UserID         int        `json:"userId"`
	Username       string     `json:"username,omitempty"`
	ServerUUID     string     `json:"serverUuid,omitempty"`
	ServerRegion   string     `json:"serverRegion,omitempty"`
	ServerName     string     `json:"serverName,omitempty"`
	Title          string     `json:"title"`
	Status         string     `json:"status"`
	Priority       string     `json:"priority"`
	AssignedUserID *int       `json:"assignedUserId,omitempty"`
	AssignedName   string     `json:"assignedName,omitempty"`
	AssignedTeam   string     `json:"assignedTeam,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	ClosedAt       *time.Time `json:"closedAt,omitempty"`
	// MessageCount is populated by list queries for the badge in the inbox UI.
	MessageCount int `json:"messageCount,omitempty"`
	// UnseenInternal is the count of internal notes (only meaningful for
	// support viewers). 0 for the ticket creator.
	UnseenInternal int `json:"unseenInternal,omitempty"`
}

// TicketMessage is one reply on a ticket. IsInternal hides it from the
// creator + watchers — only support+admin see internal notes.
type TicketMessage struct {
	ID         int       `json:"id"`
	TicketID   int       `json:"ticketId"`
	UserID     int       `json:"userId"`
	Username   string    `json:"username,omitempty"`
	UserRole   string    `json:"userRole,omitempty"` // role at time of post (snapshot)
	Body       string    `json:"body"`
	IsInternal bool      `json:"isInternal"`
	CreatedAt  time.Time `json:"createdAt"`
}

// TicketWatcher is a CC participant. CanReply distinguishes read-only
// observers from co-authors. Read-only watchers never see internal notes.
type TicketWatcher struct {
	TicketID int       `json:"ticketId"`
	UserID   int       `json:"userId"`
	Username string    `json:"username,omitempty"`
	CanReply bool      `json:"canReply"`
	AddedAt  time.Time `json:"addedAt"`
	AddedBy  *int      `json:"addedBy,omitempty"`
}

// TicketAuditEvent is an append-only audit row scoped to one ticket.
// Surfaced in the support UI under a "History" tab.
type TicketAuditEvent struct {
	ID          int64                  `json:"id"`
	TicketID    int                    `json:"ticketId"`
	EventType   string                 `json:"eventType"`
	ActorUserID *int                   `json:"actorUserId,omitempty"`
	ActorName   string                 `json:"actorName,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt   time.Time              `json:"createdAt"`
}

// ── Phase 3 — Tickets Polish ─────────────────────────────────────────

// TicketAttachment is the metadata row for a file uploaded to a ticket.
// The actual bytes live in the configured StorageProvider under StorageKey.
// MessageID is nullable so attachments can land on the create form before
// any messages exist.
type TicketAttachment struct {
	ID         int       `json:"id"`
	TicketID   int       `json:"ticketId"`
	MessageID  *int      `json:"messageId,omitempty"`
	Filename   string    `json:"filename"`
	Mime       string    `json:"mime"`
	SizeBytes  int64     `json:"sizeBytes"`
	StorageKey string    `json:"-"` // never sent to clients
	UploadedBy *int      `json:"uploadedBy,omitempty"`
	Username   string    `json:"username,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// CannedResponse is an admin-curated snippet support staff insert into
// replies. CategoryID scopes the suggestion to a single ticket category
// (nullable = global). Body supports template variables expanded by the
// frontend at insert time.
type CannedResponse struct {
	ID         int       `json:"id"`
	Name       string    `json:"name"`
	Body       string    `json:"body"`
	CategoryID *int      `json:"categoryId,omitempty"`
	CreatedBy  *int      `json:"createdBy,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// ServerAuditEvent is one row in the per-server audit log (Phase 4).
// Append-only by convention — there's no UPDATE/DELETE in the store layer
// beyond the retention sweep. TargetUserID is set on member-related events
// (invite/remove/permission change) so admins can answer "who was kicked off
// my server" without parsing metadata.
type ServerAuditEvent struct {
	ID           int64                  `json:"id"`
	ServerID     int                    `json:"serverId"`
	Region       string                 `json:"region"`
	EventType    string                 `json:"eventType"`
	ActorUserID  *int                   `json:"actorUserId,omitempty"`
	ActorName    string                 `json:"actorName,omitempty"`
	TargetUserID *int                   `json:"targetUserId,omitempty"`
	TargetName   string                 `json:"targetName,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	IPAddress    string                 `json:"ipAddress,omitempty"`
	UserAgent    string                 `json:"userAgent,omitempty"`
	CreatedAt    time.Time              `json:"createdAt"`
}

// ServerAuditState is what GET /servers/{id}/audit/status returns — drives
// the UI toggle that admins use to flip audit_force_on.
type ServerAuditState struct {
	Enabled       bool `json:"enabled"`        // auto-flipped by InviteMember
	ForceOn       bool `json:"forceOn"`        // admin override
	EffectiveOn   bool `json:"effectiveOn"`    // enabled OR forceOn
	EventCount    int  `json:"eventCount"`     // total rows for this server
}

// Notification is one in-app notification row. Generic enough for any
// producer; tickets are just the first user. Link is the relative URL the
// bell-dropdown anchors the row to.
type Notification struct {
	ID        int64      `json:"id"`
	UserID    int        `json:"userId"`
	Type      string     `json:"type"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Link      string     `json:"link,omitempty"`
	ReadAt    *time.Time `json:"readAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

type Node struct {
	ID            int        `json:"id"`
	Name          string     `json:"name"`
	Token         string     `json:"token"`
	LinkEnabled   bool       `json:"linkEnabled"`
	LinkInstances int        `json:"linkInstances"`
	LinkSecret    string     `json:"linkSecret"`
	CpusetCpus    string     `json:"cpusetCpus"`
	CreatedAt     time.Time  `json:"createdAt"`
	LastSeenAt    *time.Time `json:"lastSeenAt"`

	Address    string   `json:"address"`
	Status     string   `json:"status"`
	Tags       string   `json:"tags"`
	Region     string   `json:"region"`
	IsLocal    bool     `json:"isLocal"`
	PublicIP   string   `json:"publicIp"`
	PrivateIPs []string `json:"privateIps"`

	ServerCount int `json:"serverCount,omitempty"`

	// Placement / overcommit (persisted)
	CPUOvercommitRatio float64 `json:"cpuOvercommitRatio"`
	RAMOvercommitRatio float64 `json:"ramOvercommitRatio"`
	TotalCPU           float64 `json:"totalCpu"`   // physical cores (cached from heartbeat)
	TotalRAMMB         int64   `json:"totalRamMb"` // physical RAM in MB (cached from heartbeat)

	// Live stats from heartbeat (not persisted, -1 = not available)
	CPUUsage  float64 `json:"cpuUsage"`
	RAMFree   int64   `json:"ramFree"`
	RAMTotal  uint64  `json:"ramTotal"`
	LinkCount int     `json:"linkCount"`
}

type Server struct {
	ID              int          `json:"id"`
	UUID            string       `json:"uuid"`
	Name            string       `json:"name"`
	NodeID          int          `json:"nodeId"`
	NodeName        string       `json:"node"`
	NodeAddress     string       `json:"nodeAddress"`
	OwnerID         int          `json:"ownerId"`
	OwnerName       string       `json:"owner"`
	GameImage       string       `json:"image"`
	Port            int          `json:"port"`
	Memory          int          `json:"memory"`
	CPULimit        float64      `json:"cpuLimit"`
	StartCommand    string       `json:"startCommand"`
	Status          string       `json:"status"`
	DesiredState    string       `json:"desiredState"`
	IsFixed         bool         `json:"isFixed"`
	ActiveSubServer string       `json:"activeSubServer"`
	ExtraJvmFlags    string       `json:"extraJvmFlags"`
	InstallerType    string       `json:"installerType"`
	MinecraftVersion string       `json:"minecraftVersion"`
	BuildNumber      string       `json:"buildNumber"`
	DiskLimit        int64        `json:"diskLimit"`
	HostPort        int             `json:"hostPort"`
	ContainerPort   int             `json:"containerPort"`
	ServerType      string          `json:"serverType"`
	ProxyID         *int            `json:"proxyId"`
	AutoMove        bool            `json:"autoMove"`
	Region          string          `json:"region"`
	CreatedAt       time.Time       `json:"createdAt"`
	Role            string          `json:"role,omitempty"`
	Permissions     *TabPermissions `json:"permissions,omitempty"`
}

// TabPermissions defines per-tab access rights for invited users
type TabPermissions struct {
	Console  bool `json:"console"`
	Files    bool `json:"files"`
	Config   bool `json:"config"`
	Setup    bool `json:"setup"`
	Overview bool `json:"overview"`
	Power    bool `json:"power"`
	Members  bool `json:"members"`
	Network  bool `json:"network"`
	Inherit  bool `json:"inherit"`
}

// ServerInvite represents an invitation for a user to access a server
type ServerInvite struct {
	ID          int            `json:"id"`
	ServerID    int            `json:"serverId"`
	UserID      int            `json:"userId"`
	Username    string         `json:"username"`
	Email       string         `json:"email"`
	Permissions TabPermissions `json:"permissions"`
	InvitedBy   int            `json:"invitedBy"`
	InviterName string         `json:"inviterName"`
	CreatedAt   time.Time      `json:"createdAt"`
}

// ServerStatRow represents a single stats data point stored in PostgreSQL
type ServerStatRow struct {
	Time       time.Time `json:"time"`
	ServerUUID string    `json:"serverUuid"`
	CPU        float64   `json:"cpu"`
	CPULimit   float64   `json:"cpuLimit"`
	MemUsed    int64     `json:"memUsed"`
	MemLimit   int64     `json:"memLimit"`
	Players    int       `json:"players"`
	MaxPlayers int       `json:"maxPlayers"`
}

type Module struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Icon       string `json:"icon"`
	URL        string `json:"url"`
	IsEnabled  bool   `json:"isEnabled"`
	IsSystem   bool   `json:"isSystem"`
	Position   int    `json:"position"`
	AccessRole string `json:"accessRole"` // "all" | "admin"
}

// --- Gateway Route Limits (managed by Core, not Hub) ---

type GatewayRouteLimit struct {
	ID        int    `json:"id"`
	Scope     string `json:"scope"`
	MaxRoutes int    `json:"maxRoutes"`
}
