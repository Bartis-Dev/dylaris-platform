package store

import (
	"dylaris-core/models"
	"time"
)

// SFTPAccess is a flat row returned by GetSFTPAccessByNode.
type SFTPAccess struct {
	ServerUUID string
	ServerName string
	Username   string
}

// Store defines all database operations of the Core
type Store interface {
	// --- Users ---
	GetUserByUsername(username string) (*models.User, error)
	GetUserByID(id int) (*models.User, error)
	CreateUser(user *models.User) error
	UpdateUser(user *models.User) error
	UpdateUserPassword(id int, hashedPassword string) error
	DeleteUser(id int) error
	ListUsers() ([]models.User, error)
	CountUsers() (int, error)

	// --- 2FA (TOTP + Backup Codes) ---
	SetUserTOTP(id int, secret string, backupCodesJSON string, enabled bool) error
	DisableUserTOTP(id int) error

	// --- Nodes ---
	GetNodeByID(id int) (*models.Node, error)
	GetNodeByToken(token string) (*models.Node, error)
	GetNodeByName(name string) (*models.Node, error)
	ListNodes() ([]models.Node, error)
	CreateNode(node *models.Node) error
	DeleteNode(id int) error
	SetNodeStatus(id int, status string) error
	SetNodeTags(id int, tags string) error
	SetNodeName(id int, name string) error
	SetNodeAddress(id int, address string) error
	SetNodeIPs(id int, publicIP string, privateIPs []string) error
	UpdateNodeCpusetCpus(id int, cpusetCpus string) error
	SetNodeLastSeen(id int) error
	CountServersByNode(nodeID int) (int, error)
	ListServersByNode(nodeID int) ([]models.Server, error)
	DeleteServersByNode(nodeID int) error
	DeleteStaleOfflineNodes(offlineSince time.Time) (int, error)

	// --- Servers ---
	CreateServer(srv *models.Server) (int64, error)
	ListServers(filterByUser string) ([]models.Server, error)
	GetServerByID(id int) (*models.Server, error)
	GetServerByUUID(uuid string) (*models.Server, error)
	DeleteServer(id int) error
	UpdateServerStatus(id int, status string) error
	UpdateServerDesiredState(id int, desiredState string) error
	UpdateServerSetup(id int, image, command, activeSubServer, extraJvmFlags, installerType, minecraftVersion, buildNumber string) error
	UpdateServerActiveSubServer(id int, subServer string) error
	UpdateServerName(id int, name string) error
	UpdateServerResources(id int, ram int, cpuLimit float64, diskLimit int64) error
	UpdateServerPorts(id int, hostPort, containerPort int) error
	GetUsedHostPortsOnNode(nodeID int) ([]int, error)
	GetAllActiveServers() ([]models.Server, error)
	CountServersByOwner(ownerID int) (int, error)
	UpdateServerProxyID(id int, proxyID *int) error
	UpdateServerOwner(id int, ownerID *int) error

	// --- Server Invites ---
	CreateInvite(serverID, userID, invitedBy int, permissions map[string]bool) error
	DeleteInvite(serverID, userID int) error
	UpdateInvitePermissions(serverID, userID int, permissions map[string]bool) error
	GetInvite(serverID, userID int) (*models.ServerInvite, error)
	ListInvitesByServer(serverID int) ([]models.ServerInvite, error)
	ListServersForUser(userID int, isAdmin bool) ([]models.Server, error)

	// --- Modules ---
	ListModules() ([]models.Module, error)
	GetModuleByID(id int) (*models.Module, error)
	CreateModule(mod *models.Module) (int, error)
	DeleteModule(id int) error
	UpdateModuleStatus(id int, isEnabled bool) error
	UpdateModulePosition(id int, position int) error

	// --- Settings ---
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error

	// --- SFTP ---
	GetSFTPAccessByNode(nodeID int) ([]SFTPAccess, error)

	// --- Stats ---
	InsertStatsBatch(stats []models.ServerStatRow) error
	GetStatsHistory(serverUUID string, since time.Time) ([]models.ServerStatRow, error)

	// --- Library Disabled Paths ---
	ListDisabledLibraryPaths() ([]string, error)
	SetLibraryPathDisabled(path string, disabled bool) error

	// --- Gateway Route Limits (still managed by Core, not Hub) ---
	GetGatewayRouteLimit(scope string) (*models.GatewayRouteLimit, error)
	SetGatewayRouteLimit(scope string, max int) error
	ListGatewayRouteLimits() ([]models.GatewayRouteLimit, error)
	DeleteGatewayRouteLimit(scope string) error
}
