package store

import (
	"context"
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
	GetUserByID(id string) (*models.User, error)
	CreateUser(user *models.User) error
	UpdateUser(user *models.User) error
	UpdateUserPassword(id string, hashedPassword string) error
	DeleteUser(id string) error
	ListUsers() ([]models.User, error)
	CountUsers() (int, error)

	// --- 2FA (TOTP + Backup Codes) ---
	SetUserTOTP(id string, secret string, backupCodesJSON string, enabled bool) error
	DisableUserTOTP(id string) error

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
	SetNodeOwner(id int, ownerID *string) error
	// --- BYON node enrollment ---
	CreateNodeEnrollToken(userID, plaintext, label string, expiresAt *time.Time) error
	ResolveNodeEnrollToken(plaintext string) (userID string, ok bool, err error)
	ListNodeEnrollTokens(userID string) ([]NodeEnrollToken, error)
	DeleteNodeEnrollToken(id, userID string) error
	// --- BYON traffic metering ---
	TenantServerOwners() (map[string]string, error)
	TenantBackupBytes() (map[string]int64, error)
	AddTrafficUsage(userID string, period time.Time, edgeBytes, relayBytes int64) error
	SetTrafficBackupBytes(userID string, period time.Time, backupBytes int64) error
	GetTrafficUsage(userID string, period time.Time) (*TrafficUsage, error)
	ListTrafficUsage(period time.Time) ([]TrafficUsage, error)
	// --- BYON billing lifecycle ---
	GetUserBilling(userID string) (*UserBilling, error)
	SetUserBillingStatus(userID, status string, graceUntil, suspendedAt *time.Time) error
	SetUserBillingOverrides(userID, gracePeriod, r2Retention, nodeRetention string, r2QuotaGB *int64) error
	ListUserBillingByStatus(status string) ([]UserBilling, error)
	ListServersByOwner(ownerID string) ([]models.Server, error)
	ListBackupRunsByOwner(ownerID string) ([]BackupRunRef, error)
	BackupBytesByOwner(ownerID string) (int64, error)
	// --- BYON plans + limits ---
	ListPlans() ([]Plan, error)
	GetPlan(id int) (*Plan, error)
	GetDefaultPlan() (*Plan, error)
	CreatePlan(p Plan) (int, error)
	UpdatePlan(p Plan) error
	DeletePlan(id int) error
	GetUserPlanID(userID string) (*int, error)
	SetUserPlan(userID string, planID *int) error
	SetUserLimitOverrides(userID string, maxNodes, trafficEdge, trafficRelay, trafficCombined *int64) error
	CountNodesByOwner(ownerID string) (int, error)
	SetNodeLastSeen(id int) error
	SetNodePlacement(id int, cpuRatio, ramRatio float64) error
	UpdateNodeCapacity(id int, totalCPU float64, totalRAMMB int64) error
	SetNodeRegion(id int, region string) error
	// SetNodeConfig persists an admin's panel-configured name/region/tags and
	// marks the node configured=true so the heartbeat env stops overwriting them.
	SetNodeConfig(id int, name, region, tags string) error
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
	UpdateServerCPUPinning(id int, mode, cpuset string) error
	ListServerCpusetsByNode(nodeID int) (map[int]string, error)
	ResetServerCPUPinningByNode(nodeID int) (int64, error)
	UpdateServerPorts(id int, hostPort, containerPort int) error
	GetUsedHostPortsOnNode(nodeID int) ([]int, error)
	GetAllActiveServers() ([]models.Server, error)
	CountServersByOwner(ownerID string) (int, error)
	UpdateServerProxyID(id int, proxyID *int) error
	UpdateServerOwner(id int, ownerID *string) error
	SetServerAutoMove(id int, enabled bool) error
	// UpdateServerNode reassigns a server to a different node. Used by the
	// auto-move migration flow (later wave) to flip the FK after transport.
	UpdateServerNode(serverID int, newNodeID int) error
	// ResetAllAutoMove clears the auto_move opt-in on every server. Called when
	// gateway routing is switched off, since auto-move cannot run without it.
	ResetAllAutoMove() error

	// --- Server Invites ---
	CreateInvite(serverID int, userID, invitedBy string, permissions map[string]bool) error
	DeleteInvite(serverID int, userID string) error
	UpdateInvitePermissions(serverID int, userID string, permissions map[string]bool) error
	GetInvite(serverID int, userID string) (*models.ServerInvite, error)
	ListInvitesByServer(serverID int) ([]models.ServerInvite, error)
	CountInvitesPerServer() (map[int]int, error)
	ListServersForUser(userID string, isAdmin bool) ([]models.Server, error)

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

	// --- Health ---
	// Ping verifies the underlying database connection is alive. Backed by
	// sql.DB.PingContext so the status endpoint can report DB liveness without
	// running a real query.
	Ping(ctx context.Context) error
	// TimescaleEnabled reports whether the TimescaleDB extension is installed.
	// server_stats is created as a hypertable only when the extension is present;
	// without it the table still works as plain Postgres but loses automatic
	// retention and fast long-range history queries, which the status endpoint
	// surfaces as a degraded (not failed) component.
	TimescaleEnabled(ctx context.Context) (bool, error)
	// IsServerStatsHypertable reports whether server_stats is already a TimescaleDB
	// hypertable. Only meaningful when the timescaledb extension is installed.
	IsServerStatsHypertable(ctx context.Context) (bool, error)
	// ConvertServerStatsToHypertable promotes the existing plain server_stats table
	// to a hypertable IN PLACE (migrate_data) and (re)applies the retention policy.
	// Used after swapping a plain-Postgres image for a TimescaleDB one on the same DB.
	ConvertServerStatsToHypertable(ctx context.Context) error
	// EstimateServerStatsRows returns the planner's row-count estimate for
	// server_stats (pg_class.reltuples) - instant, unlike COUNT(*) on a huge table.
	EstimateServerStatsRows(ctx context.Context) (int64, error)

	// --- Warp ---
	CreateWarpAPIKey(k WarpAPIKey) (int, error)
	GetWarpAPIKeyByHash(hash string) (*WarpAPIKey, error)
	ListWarpAPIKeysByOwner(ownerID string) ([]WarpAPIKey, error)
	InsertWarpPeer(p WarpPeer) (int, error)
	GetWarpPeerByPubkey(pubkey string) (*WarpPeer, error)
	ListWarpPeersByKey(apiKeyID int) ([]WarpPeer, error)
	ListAllWarpPeers() ([]WarpPeer, error)
	ListWarpPeersByRegion(region string) ([]WarpPeer, error)
	CountWarpPeersByRegion() (map[string]int, error)
	DeleteWarpPeerByPubkey(pubkey string) error
	// Warp regions + leaders (multi-hub: region = identity, leaders = endpoints)
	ListWarpRegions() ([]WarpRegion, error)
	GetWarpRegion(region string) (*WarpRegion, error)
	UpsertWarpRegion(region, subnet string, enabled bool) error
	DeleteWarpRegion(region string) error
	ListWarpLeaders() ([]WarpLeader, error)
	ListWarpLeadersByRegion(region string) ([]WarpLeader, error)
	UpsertWarpLeader(leaderID, region, endpoint string, enabled bool) error
	DeleteWarpLeader(leaderID string) error
	SeedWarpRegionIfEmpty(region, subnet, leaderID, endpoint string) error

	// --- SFTP ---
	GetSFTPAccessByNode(nodeID int) ([]SFTPAccess, error)

	// --- Stats ---
	InsertStatsBatch(stats []models.ServerStatRow) error
	GetStatsHistory(serverUUID string, since time.Time) ([]models.ServerStatRow, error)

	// --- Telemetry ---
	SumLatestPlayerCounts() (int, error)

	// --- Library Disabled Paths ---
	ListDisabledLibraryPaths() ([]string, error)
	SetLibraryPathDisabled(path string, disabled bool) error

	// --- Gateway Route Limits (still managed by Core, not Hub) ---
	GetGatewayRouteLimit(scope string) (*models.GatewayRouteLimit, error)
	SetGatewayRouteLimit(scope string, max int) error
	ListGatewayRouteLimits() ([]models.GatewayRouteLimit, error)
	DeleteGatewayRouteLimit(scope string) error

	// --- Regions ---
	ListRegions(includeDisabled bool) ([]models.Region, error)
	GetRegion(id string) (*models.Region, error)
	CreateRegion(r *models.Region) error
	UpdateRegion(r *models.Region) error
	DeleteRegion(id string) error
	CountServersInRegion(regionID string) (int, error)
	CountNodesInRegion(regionID string) (int, error)

	// --- User <-> Region M:N ---
	GetUserRegionIDs(userID string) ([]string, error)
	SetUserRegions(userID string, allAccess bool, regionIDs []string) error
	SetUserAllRegionsAccess(userID string, allAccess bool) error
	GetUserAllRegionsAccess(userID string) (bool, error)

	// --- Identity Audit Log (append-only) ---
	InsertAuditIdentity(ev *models.AuditEventIdentity) error
	ListAuditIdentity(targetUserID *string, eventType string, limit int) ([]models.AuditEventIdentity, error)

	// --- Settings audit trail ---
	// SetSettingBy stores a setting value plus the user who changed it. Existing
	// callers can keep using SetSetting (updated_by = NULL) — only new flows
	// that care about audit need this variant.
	SetSettingBy(key, value string, updatedBy string) error

	// --- Email verification + login tracking ---
	GetUserByEmail(email string) (*models.User, error)
	GetUserByEmailVerificationToken(token string) (*models.User, error)
	SetEmailVerificationToken(userID string, token string) error
	MarkEmailVerified(userID string) error
	UpdateLastLoginAt(userID string) error

	// --- Password reset ---
	GetUserByPasswordResetToken(token string) (*models.User, error)
	SetPasswordResetToken(userID string, token string, expiresAt time.Time) error
	ClearPasswordResetToken(userID string) error

	// --- Security questions ---
	// GetUserSecurityQuestions returns the question texts only (no hashes),
	// in the same order they were stored — the verify path matches answers
	// positionally so order is part of the contract.
	GetUserSecurityQuestions(userID string) ([]string, error)
	// SetUserSecurityQuestions replaces the user's whole list. Pass an empty
	// slice to clear (allowed when the policy lets users opt out).
	SetUserSecurityQuestions(userID string, qaJSON string) error
	// GetUserSecurityQuestionsRaw returns the stored JSON for verification
	// — caller bcrypt-compares answer-by-answer.
	GetUserSecurityQuestionsRaw(userID string) (string, error)

	// --- Roles + granular permissions ---
	// SetUserRole writes both role and the legacy is_admin flag so handlers
	// that still read is_admin stay in sync. Valid roles: 'user', 'support', 'admin'.
	SetUserRole(userID string, role string) error
	// SetUserPermissionFlags sets the can_* booleans. SupportTeam is optional;
	// pass "" to clear.
	SetUserPermissionFlags(userID string, canDeleteServers, canChangeResources bool, supportTeam string) error

	// --- Tickets ---
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
	UpdateTicketAssignment(id int, assignedUserID *string, assignedTeam string) error
	TouchTicketUpdated(id int) error // bump updated_at — call after any mutation

	// Messages
	AddTicketMessage(m *models.TicketMessage) (int, error)
	ListTicketMessages(ticketID int, includeInternal bool) ([]models.TicketMessage, error)

	// Watchers
	ListTicketWatchers(ticketID int) ([]models.TicketWatcher, error)
	AddTicketWatcher(w *models.TicketWatcher) error
	RemoveTicketWatcher(ticketID int, userID string) error
	IsTicketWatcher(ticketID int, userID string) (bool, error)

	// Audit
	InsertTicketAudit(ev *models.TicketAuditEvent) error
	ListTicketAudit(ticketID int) ([]models.TicketAuditEvent, error)

	// Sidebar: servers attached to active tickets assigned to a support user.
	ListServersViaActiveTickets(supportUserID string) ([]models.Server, error)

	// --- Ticket deletion (admin-only, audited) ---
	// DeleteTicket removes the ticket + every attached row in one tx. Returns
	// sql.ErrNoRows when the ticket id doesn't exist.
	DeleteTicket(id int) error
	// InsertTicketDeletion stamps the audit row. The handler builds the
	// snapshot fields before calling DeleteTicket so the row is independent of
	// the now-gone source data.
	InsertTicketDeletion(rec *models.TicketDeletion) error
	// ListTicketDeletions returns (rows, total) for the admin Deletion-Log UI.
	// deletedBy "" means no filter; limit is clamped server-side.
	ListTicketDeletions(limit, offset int, deletedBy string) ([]models.TicketDeletion, int, error)
	// ListAttachmentStorageKeysByTicket returns the on-disk storage keys for a
	// ticket so the handler can delete the file blobs after the DB rows are
	// gone. Called BEFORE DeleteTicket so the keys are still readable.
	ListAttachmentStorageKeysByTicket(ticketID int) ([]string, error)

	// --- Tickets (attachments, canned responses, notifications) ---
	// Attachments
	AddTicketAttachment(a *models.TicketAttachment) (int, error)
	GetTicketAttachment(id int) (*models.TicketAttachment, error)
	ListTicketAttachments(ticketID int) ([]models.TicketAttachment, error)
	DeleteTicketAttachment(id int) error
	SumAttachmentBytesByTicket(ticketID int) (int64, error)
	SumAttachmentBytesByUser(userID string) (int64, error)

	// Canned responses
	ListCannedResponses(categoryID *int) ([]models.CannedResponse, error)
	GetCannedResponse(id int) (*models.CannedResponse, error)
	CreateCannedResponse(c *models.CannedResponse) (int, error)
	UpdateCannedResponse(c *models.CannedResponse) error
	DeleteCannedResponse(id int) error

	// Notifications
	InsertNotification(n *models.Notification) (int64, error)
	ListNotifications(userID string, includeRead bool, limit int) ([]models.Notification, error)
	CountUnreadNotifications(userID string) (int, error)
	MarkNotificationRead(id int64, userID string) error
	MarkAllNotificationsRead(userID string) error

	// Auto-close support
	ListResolvedTicketsOlderThan(cutoff time.Time) ([]int, error)
	// Watchers + assignee lookup for notification fan-out
	ListTicketParticipantsForNotify(ticketID int, excludeUserID string) ([]string, error)

	// --- Migration + backup raw access ---
	// CountTicketRows returns the row count for a single ticket-related
	// table from the main DB. Used by the dry-run + status endpoints
	// without dragging full per-table SELECTs into the migration handler.
	CountTicketRows(table string) (int, error)
	// DumpTicketTable streams all rows of the named table as
	// []map[string]interface{}. Used by both backup and migration.
	DumpTicketTable(table string) ([]map[string]interface{}, error)

	// --- Server audit ---
	InsertServerAudit(ev *models.ServerAuditEvent) error
	ListServerAudit(serverID int, eventType string, limit, offset int) ([]models.ServerAuditEvent, int, error)
	SetServerAuditEnabled(serverID int, enabled bool) error
	SetServerAuditForceOn(serverID int, force bool) error
	// GetServerAuditState returns the (enabled, forceOn, count) tuple in one
	// query so the status endpoint stays cheap.
	GetServerAuditState(serverID int) (enabled, forceOn bool, count int, err error)
	// PurgeServerAuditOlderThan supports the retention sweep service.
	PurgeServerAuditOlderThan(cutoff time.Time) (int, error)

	// --- Auto-delete inactive users ---
	// ListInactiveCandidates returns active non-admin users whose last login
	// (or creation when never logged in) is older than `idleSince`. The
	// HasHistory flag lets the calling job apply an extra grace window
	// without a second query.
	ListInactiveCandidates(idleSince time.Time) ([]InactiveCandidate, error)
	// MarkUserPendingDeletion stages the user; the warning mail is sent
	// separately by the job so a mail failure doesn't roll back the stamp.
	MarkUserPendingDeletion(userID string, scheduledAt time.Time) error
	// ListUsersDueForDeletion returns user IDs whose scheduled_at is <= now
	// AND who are still in pending_deletion state.
	ListUsersDueForDeletion(now time.Time) ([]string, error)
	// CancelUserDeletion clears warning/scheduled stamps and resets status.
	// Idempotent: safe to call on already-active users.
	CancelUserDeletion(userID string) error
	// AnonymizeUser wipes PII (username/email/password/2FA/security questions)
	// but keeps the row + id so audit references stay valid.
	AnonymizeUser(userID string) error

	// --- Scheduled Tasks ---
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

	// --- RCON config ---
	// Lazy accessors so RCON fields don't bloat every server-list scan.
	// Password is stored in plaintext for V1 — the column is excluded from
	// any list query and only fetched by the RCON-exec path.
	GetServerRconConfig(serverID int) (enabled bool, port int, password string, err error)
	SetServerRconConfig(serverID int, enabled bool, port int, password string) error

	// --- API Keys ---
	// Public RCON surface backing. Plaintext key is shown to the user once
	// on creation; the DB only ever sees sha256 hash. Scope JSON shape:
	//   { "servers": ["uuid-1", ...], "permissions": ["rcon.exec"] }
	CreateAPIKey(k *models.APIKey) (int, error)
	ListAPIKeysByUser(userID string) ([]models.APIKey, error)
	GetAPIKeyByHash(hash string) (*models.APIKey, error)
	RevokeAPIKey(id int, userID string) error
	TouchAPIKey(id int) error

	// --- Installed Mods ---
	// Per-server-per-sub-server inventory of Modrinth-sourced mods/plugins.
	// Drives the "Installed" view + the lazy update-detection scan.
	UpsertServerMod(m *models.ServerMod) (int, error)
	ListServerMods(serverID int, subServerName string) ([]models.ServerMod, error)
	DeleteServerMod(id, serverID int) error

	// --- Modpacks ---
	// User-authored modpacks. Slug uniqueness is per-owner so two users can
	// each have a "skyblock" pack without collision.
	CreateModpack(m *models.Modpack) (int, error)
	UpdateModpack(m *models.Modpack) error
	DeleteModpack(id int, ownerID string) error
	GetModpack(id int) (*models.Modpack, error)
	ListModpacksByOwner(ownerID string) ([]models.Modpack, error)

	// Versions + mods within a pack.
	CreateModpackVersion(v *models.ModpackVersion) (int, error)
	UpdateModpackVersion(v *models.ModpackVersion) error
	DeleteModpackVersion(id, modpackID int) error
	GetModpackVersion(id int) (*models.ModpackVersion, error)
	ListModpackVersions(modpackID int) ([]models.ModpackVersion, error)
	UpsertModpackMod(m *models.ModpackMod) (int, error)
	ListModpackMods(versionID int) ([]models.ModpackMod, error)
	DeleteModpackMod(id, versionID int) error

	// Modrinth PATs. One row per user; SetModrinthPAT upserts and
	// stamps last_validated_at on success. ClearModrinthPAT removes the
	// row entirely so a revoked PAT can't accidentally be re-used.
	SetModrinthPAT(userID string, ciphertext, modrinthUsername string) error
	GetModrinthPAT(userID string) (*models.ModrinthPAT, error)
	ClearModrinthPAT(userID string) error

	// --- Username history + admin rename ---
	RenameUser(userID string, newUsername string, changedBy string) error
	ListUsernameHistory(userID string) ([]models.UsernameHistory, error)
	GetUserAccountPolicy() (allowChange bool, cooldownDays int, err error)
	SetUserAccountPolicy(allowChange bool, cooldownDays int) error

	// --- Per-user feature flag ---
	SetUserCanCreateModpacks(userID string, can bool) error

	// --- Setup wizard ---
	// CountUsers is declared above in the Users block; only CountAdmins is new.
	CountAdmins() (int, error)
	// CreateFirstAdmin atomically inserts the first admin via a guarded CTE.
	// Returns ErrSetupAlreadyComplete / ErrSetupInvalidToken to let the
	// handler map outcomes to HTTP status codes without parsing strings.
	CreateFirstAdmin(username, passwordHash, totpSecret, recoveryToken string) (*models.User, error)
}

// InactiveCandidate is the minimal slice of user data the auto-delete job
// needs to make a decision without fetching the whole row.
type InactiveCandidate struct {
	ID         string
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
	UserID         *string // user's own tickets
	AssignedUserID *string // tickets assigned to a supporter
	AssignedTeam   string  // tickets owned by a team (cross-team visibility scope)
	WatcherUserID  *string // tickets the user is CC'd on

	// Refinements layered on top of the scope.
	Status     []string // include only these statuses
	Priority   []string // include only these priorities
	CategoryID *int
	ServerUUID string
	Region     string

	// Pagination.
	Limit  int
	Offset int
}
