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
	SetNodePlacement(id int, cpuRatio, ramRatio float64) error
	UpdateNodeCapacity(id int, totalCPU float64, totalRAMMB int64) error
	SetNodeRegion(id int, region string) error
	SumAllocatedByNode(nodeID int) (totalRAMMB int64, totalCPU float64, err error)
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
	SetServerAutoMove(id int, enabled bool) error

	// --- Server Invites ---
	CreateInvite(serverID, userID, invitedBy int, permissions map[string]bool) error
	DeleteInvite(serverID, userID int) error
	UpdateInvitePermissions(serverID, userID int, permissions map[string]bool) error
	GetInvite(serverID, userID int) (*models.ServerInvite, error)
	ListInvitesByServer(serverID int) ([]models.ServerInvite, error)
	CountInvitesPerServer() (map[int]int, error)
	ListServersForUser(userID int, isAdmin bool) ([]models.Server, error)

	// --- Backups ---
	ListBackupStorages() ([]models.BackupStorage, error)
	GetBackupStorage(id int) (*models.BackupStorage, error)
	GetDefaultBackupStorage() (*models.BackupStorage, error)
	CreateBackupStorage(s *models.BackupStorage) (int, error)
	UpdateBackupStorage(s *models.BackupStorage) error
	DeleteBackupStorage(id int) error

	ListBackupJobs(serverID int) ([]models.BackupJob, error)
	GetBackupJob(id int) (*models.BackupJob, error)
	CreateBackupJob(j *models.BackupJob) (int, error)
	UpdateBackupJob(j *models.BackupJob) error
	DeleteBackupJob(id int) error
	ListDueBackupJobs(now time.Time) ([]models.BackupJob, error)
	SetBackupJobScheduled(jobID int, lastRun, nextRun time.Time) error

	ListBackupRuns(jobID int, limit int) ([]models.BackupRun, error)
	GetBackupRun(id int) (*models.BackupRun, error)
	CreateBackupRun(r *models.BackupRun) (int, error)
	UpdateBackupRunStatus(id int, status, errorMsg string, sizeBytes int64, storageKey string, completed time.Time) error
	DeleteBackupRun(id int) error
	PruneOldBackupRuns(jobID, keep int) ([]models.BackupRun, error)

	CreateBackupRestore(r *models.BackupRestore) (int, error)
	GetBackupRestore(id int) (*models.BackupRestore, error)
	ListBackupRestores(serverID, limit int) ([]models.BackupRestore, error)
	UpdateBackupRestoreStatus(id int, status, errorMsg string, completed time.Time) error

	// --- Modules ---
	ListModules() ([]models.Module, error)
	GetModuleByID(id int) (*models.Module, error)
	CreateModule(mod *models.Module) (int, error)
	DeleteModule(id int) error
	UpdateModuleStatus(id int, isEnabled bool) error
	UpdateModulePosition(id int, position int) error
	SetModuleAccessRole(id int, role string) error

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

	// --- Regions (Phase 0a.1) ---
	ListRegions(includeDisabled bool) ([]models.Region, error)
	GetRegion(id string) (*models.Region, error)
	CreateRegion(r *models.Region) error
	UpdateRegion(r *models.Region) error
	DeleteRegion(id string) error
	CountServersInRegion(regionID string) (int, error)
	CountNodesInRegion(regionID string) (int, error)

	// --- User <-> Region M:N (Phase 0a.1) ---
	GetUserRegionIDs(userID int) ([]string, error)
	SetUserRegions(userID int, allAccess bool, regionIDs []string) error
	SetUserAllRegionsAccess(userID int, allAccess bool) error
	GetUserAllRegionsAccess(userID int) (bool, error)

	// --- Identity Audit Log (Phase 0a.1, append-only) ---
	InsertAuditIdentity(ev *models.AuditEventIdentity) error
	ListAuditIdentity(targetUserID *int, eventType string, limit int) ([]models.AuditEventIdentity, error)

	// --- Settings audit trail (Phase 0a.1) ---
	// SetSettingBy stores a setting value plus the user who changed it. Existing
	// callers can keep using SetSetting (updated_by = NULL) — only new flows
	// that care about audit need this variant.
	SetSettingBy(key, value string, updatedBy int) error

	// --- Email verification + login tracking (Phase 0a.2) ---
	GetUserByEmail(email string) (*models.User, error)
	GetUserByEmailVerificationToken(token string) (*models.User, error)
	SetEmailVerificationToken(userID int, token string) error
	MarkEmailVerified(userID int) error
	UpdateLastLoginAt(userID int) error

	// --- Password reset (Phase 0a.4) ---
	GetUserByPasswordResetToken(token string) (*models.User, error)
	SetPasswordResetToken(userID int, token string, expiresAt time.Time) error
	ClearPasswordResetToken(userID int) error

	// --- Security questions (Phase 0a.5) ---
	// GetUserSecurityQuestions returns the question texts only (no hashes),
	// in the same order they were stored — the verify path matches answers
	// positionally so order is part of the contract.
	GetUserSecurityQuestions(userID int) ([]string, error)
	// SetUserSecurityQuestions replaces the user's whole list. Pass an empty
	// slice to clear (allowed when the policy lets users opt out).
	SetUserSecurityQuestions(userID int, qaJSON string) error
	// GetUserSecurityQuestionsRaw returns the stored JSON for verification
	// — caller bcrypt-compares answer-by-answer.
	GetUserSecurityQuestionsRaw(userID int) (string, error)

	// --- Roles + granular permissions (Phase 1) ---
	// SetUserRole writes both role and the legacy is_admin flag so handlers
	// that still read is_admin stay in sync. Valid roles: 'user', 'support', 'admin'.
	SetUserRole(userID int, role string) error
	// SetUserPermissionFlags sets the can_* booleans. SupportTeam is optional;
	// pass "" to clear.
	SetUserPermissionFlags(userID int, canDeleteServers, canChangeResources bool, supportTeam string) error

	// --- Tickets (Phase 2) ---
	// Categories
	ListTicketCategories(includeDisabled bool) ([]models.TicketCategory, error)
	GetTicketCategory(id int) (*models.TicketCategory, error)
	CreateTicketCategory(c *models.TicketCategory) (int, error)
	UpdateTicketCategory(c *models.TicketCategory) error
	DeleteTicketCategory(id int) error

	// Tickets
	CreateTicket(t *models.Ticket) (int, error)
	GetTicket(id int) (*models.Ticket, error)
	// ListTickets supports the user list (filter user_id), the support inbox
	// (status, assignedUserID, assignedTeam filters), and admin global view.
	// Pass empty/nil for filters you don't want applied. limit caps at 200.
	ListTickets(filter TicketFilter) ([]models.Ticket, error)
	UpdateTicketStatus(id int, status string) error
	UpdateTicketPriority(id int, priority string) error
	UpdateTicketAssignment(id int, assignedUserID *int, assignedTeam string) error
	TouchTicketUpdated(id int) error // bump updated_at — call after any mutation

	// Messages
	AddTicketMessage(m *models.TicketMessage) (int, error)
	ListTicketMessages(ticketID int, includeInternal bool) ([]models.TicketMessage, error)

	// Watchers
	ListTicketWatchers(ticketID int) ([]models.TicketWatcher, error)
	AddTicketWatcher(w *models.TicketWatcher) error
	RemoveTicketWatcher(ticketID, userID int) error
	IsTicketWatcher(ticketID, userID int) (bool, error)

	// Audit
	InsertTicketAudit(ev *models.TicketAuditEvent) error
	ListTicketAudit(ticketID int) ([]models.TicketAuditEvent, error)

	// Sidebar: servers attached to active tickets assigned to a support user.
	ListServersViaActiveTickets(supportUserID int) ([]models.Server, error)

	// --- Tickets Phase 3 ---
	// Attachments
	AddTicketAttachment(a *models.TicketAttachment) (int, error)
	GetTicketAttachment(id int) (*models.TicketAttachment, error)
	ListTicketAttachments(ticketID int) ([]models.TicketAttachment, error)
	DeleteTicketAttachment(id int) error
	SumAttachmentBytesByTicket(ticketID int) (int64, error)
	SumAttachmentBytesByUser(userID int) (int64, error)

	// Canned responses
	ListCannedResponses(categoryID *int) ([]models.CannedResponse, error)
	GetCannedResponse(id int) (*models.CannedResponse, error)
	CreateCannedResponse(c *models.CannedResponse) (int, error)
	UpdateCannedResponse(c *models.CannedResponse) error
	DeleteCannedResponse(id int) error

	// Notifications
	InsertNotification(n *models.Notification) (int64, error)
	ListNotifications(userID int, includeRead bool, limit int) ([]models.Notification, error)
	CountUnreadNotifications(userID int) (int, error)
	MarkNotificationRead(id int64, userID int) error
	MarkAllNotificationsRead(userID int) error

	// Auto-close support
	ListResolvedTicketsOlderThan(cutoff time.Time) ([]int, error)
	// Watchers + assignee lookup for notification fan-out
	ListTicketParticipantsForNotify(ticketID int, excludeUserID int) ([]int, error)

	// --- Phase 5 — migration + backup raw access ---
	// CountTicketRows returns the row count for a single ticket-related
	// table from the main DB. Used by the dry-run + status endpoints
	// without dragging full per-table SELECTs into the migration handler.
	CountTicketRows(table string) (int, error)
	// DumpTicketTable streams all rows of the named table as
	// []map[string]interface{}. Used by both backup and migration.
	DumpTicketTable(table string) ([]map[string]interface{}, error)

	// --- Server audit (Phase 4) ---
	InsertServerAudit(ev *models.ServerAuditEvent) error
	ListServerAudit(serverID int, eventType string, limit, offset int) ([]models.ServerAuditEvent, int, error)
	SetServerAuditEnabled(serverID int, enabled bool) error
	SetServerAuditForceOn(serverID int, force bool) error
	// GetServerAuditState returns the (enabled, forceOn, count) tuple in one
	// query so the status endpoint stays cheap.
	GetServerAuditState(serverID int) (enabled, forceOn bool, count int, err error)
	// PurgeServerAuditOlderThan supports the retention sweep service.
	PurgeServerAuditOlderThan(cutoff time.Time) (int, error)

	// --- Auto-delete inactive users (Phase 0a.6) ---
	// ListInactiveCandidates returns active non-admin users whose last login
	// (or creation when never logged in) is older than `idleSince`. The
	// HasHistory flag lets the calling job apply an extra grace window
	// without a second query.
	ListInactiveCandidates(idleSince time.Time) ([]InactiveCandidate, error)
	// MarkUserPendingDeletion stages the user; the warning mail is sent
	// separately by the job so a mail failure doesn't roll back the stamp.
	MarkUserPendingDeletion(userID int, scheduledAt time.Time) error
	// ListUsersDueForDeletion returns user IDs whose scheduled_at is <= now
	// AND who are still in pending_deletion state.
	ListUsersDueForDeletion(now time.Time) ([]int, error)
	// CancelUserDeletion clears warning/scheduled stamps and resets status.
	// Idempotent: safe to call on already-active users.
	CancelUserDeletion(userID int) error
	// AnonymizeUser wipes PII (username/email/password/2FA/security questions)
	// but keeps the row + id so audit references stay valid.
	AnonymizeUser(userID int) error

	// --- Scheduled Tasks (Phase 8) ---
	// Per-server cron jobs. NextRun is computed on insert/update from the cron
	// string + now() and re-computed by the leader-gated executor after each
	// run. ListDueScheduledTasks is the executor's hot path; the partial
	// index in applyPhase8Schema keeps it cheap.
	ListScheduledTasksByServer(serverID int) ([]models.ScheduledTask, error)
	GetScheduledTask(id int) (*models.ScheduledTask, error)
	CreateScheduledTask(t *models.ScheduledTask) (int, error)
	UpdateScheduledTask(t *models.ScheduledTask) error
	DeleteScheduledTask(id int) error
	SetScheduledTaskEnabled(id int, enabled bool, nextRun *time.Time) error
	ListDueScheduledTasks(now time.Time, limit int) ([]models.ScheduledTask, error)
	RecordScheduledTaskRun(id int, ranAt time.Time, status, errMsg string, nextRun *time.Time) error
}

// InactiveCandidate is the minimal slice of user data the auto-delete job
// needs to make a decision without fetching the whole row.
type InactiveCandidate struct {
	ID         int
	Username   string
	Email      string
	LastLogin  *time.Time
	HasHistory bool
}

// TicketFilter is the optional filter struct for ListTickets. Zero-value
// means "no filter applied" — every pointer/slice that's nil/empty is
// ignored. Limit is clamped to [1, 200] by the store; 0 falls back to 50.
type TicketFilter struct {
	// Visibility scope — exactly one of these is typically set per request.
	UserID         *int     // user's own tickets
	AssignedUserID *int     // tickets assigned to a supporter
	AssignedTeam   string   // tickets owned by a team (cross-team visibility scope)
	WatcherUserID  *int     // tickets the user is CC'd on

	// Refinements layered on top of the scope.
	Status      []string // include only these statuses
	Priority    []string // include only these priorities
	CategoryID  *int
	ServerUUID  string
	Region      string

	// Pagination.
	Limit  int
	Offset int
}
