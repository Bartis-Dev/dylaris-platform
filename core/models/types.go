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
	Permissions       string    `json:"permissions"`
	PublicID          string    `json:"publicId"`
	CreatedAt         time.Time `json:"createdAt"`
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
	IsLocal    bool     `json:"isLocal"`
	PublicIP   string   `json:"publicIp"`
	PrivateIPs []string `json:"privateIps"`

	ServerCount int `json:"serverCount,omitempty"`

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
	ServerType      string          `json:"serverType"`
	ProxyID         *int            `json:"proxyId"`
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
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Icon      string `json:"icon"`
	URL       string `json:"url"`
	IsEnabled bool   `json:"isEnabled"`
	IsSystem  bool   `json:"isSystem"`
	Position  int    `json:"position"`
}

// --- Gateway Route Limits (managed by Core, not Hub) ---

type GatewayRouteLimit struct {
	ID        int    `json:"id"`
	Scope     string `json:"scope"`
	MaxRoutes int    `json:"maxRoutes"`
}
