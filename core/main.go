package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dylaris-core/config"
	"dylaris-core/database"
	nodegrpc "dylaris-core/grpc"
	"dylaris-core/handlers"
	"dylaris-core/pkg/leader"
	"dylaris-core/services"
	"dylaris-core/store"

	gorillaHandlers "github.com/gorilla/handlers"
	"github.com/gorilla/mux"
)

// detectBeamPlatform maps a browser User-Agent header to one of the platform
// slugs the gateway's beam-relay serves at /download/{slug}.
// Conservative: anything unrecognised falls back to linux-amd64.
func detectBeamPlatform(ua string) string {
	u := strings.ToLower(ua)
	switch {
	case strings.Contains(u, "windows"):
		return "windows-amd64"
	case strings.Contains(u, "mac os x"), strings.Contains(u, "macintosh"):
		if strings.Contains(u, "arm") {
			return "darwin-arm64"
		}
		return "darwin-amd64"
	case strings.Contains(u, "linux"):
		if strings.Contains(u, "aarch64") || strings.Contains(u, "arm64") {
			return "linux-arm64"
		}
		return "linux-amd64"
	default:
		return "linux-amd64"
	}
}

func main() {
	log.Println("Starting Dylaris Core...")

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("FATAL: Config Error: %v", err)
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		log.Fatalf("FATAL: Database Error: %v", err)
	}
	defer db.Close()

	pgStore := store.NewPostgresStore(db)

	// gRPC Registry for Node connections
	grpcRegistry := nodegrpc.NewRegistry()

	appState := &handlers.AppState{
		Store:               pgStore,
		GRPCRegistry:        grpcRegistry,
		FrontendURL:         cfg.FrontendURL,
		ExternalTicketDBURL: cfg.ExternalTicketDBURL,
	}

	redisClient, err := database.InitRedis(cfg)
	if err != nil {
		log.Fatalf("FATAL: Redis Error: %v", err)
	}

	appState.Redis = redisClient
	appState.Queue = services.NewQueueService(redisClient)

	// Phase 7 — system-events publisher. Mutating handlers (regions,
	// modules, features, maintenance, servers CRUD) drop events into a
	// single Redis Pub/Sub channel; panels subscribe via SSE so they refresh
	// without polling. Construction is cheap — wired before any handler so
	// every code path can call h.state.Events.Publish unconditionally.
	appState.Events = services.NewSystemEventsPublisher(redisClient)

	// Leader election (Phase 0b): a single Redis lease named for the
	// "core-leader" role, identified by this instance's CoreID. Every
	// scheduled background loop consults the leader's IsLeader() to
	// decide whether to perform its work or idle. Single-instance Core
	// always wins the election so behavior is unchanged for dev. Multi-
	// instance Core safely converges on exactly one active leader.
	coreLeader := leader.New(redisClient, "dylaris:core:leader", cfg.CoreID)
	coreLeader.Start(context.Background())

	discovery := services.NewDiscoveryService(pgStore, redisClient, cfg.ClusterSecret)
	discovery.SetLeader(coreLeader)
	discovery.Start()

	nodeCleanup := services.NewNodeCleanupService(pgStore, 24*time.Hour)
	nodeCleanup.SetLeader(coreLeader)
	nodeCleanup.Start()

	statusWatcher := services.NewStatusWatcherService(pgStore, redisClient)
	statusWatcher.SetLeader(coreLeader)
	statusWatcher.Start()

	statsConsumer := services.NewStatsConsumerService(pgStore, redisClient, cfg.CoreID)
	statsConsumer.Start()

	sftpSync := services.NewSFTPSyncService(pgStore, redisClient)
	sftpSync.Start()

	// Auto-delete service (Phase 0a.6) — daily ticker scans inactive users,
	// emails warnings, executes deletions per the auth.* settings. No-op
	// unless the operator turns it on. Leader-gated so only one Core runs
	// it under multi-instance.
	autoDelete := services.NewAutoDeleteService(pgStore, cfg.FrontendURL)
	autoDelete.SetLeader(coreLeader)
	autoDelete.Start(context.Background())

	// Ticket auto-close (Phase 3) — daily ticker, leader-gated. No-op until
	// the operator turns it on via Settings → Ticket Settings.
	ticketAutoClose := services.NewTicketAutoCloseService(pgStore)
	ticketAutoClose.SetLeader(coreLeader)
	ticketAutoClose.Start(context.Background())

	// Server-audit retention sweep (Phase 4) — daily, leader-gated. No-op
	// when audit.server_retention_days is 0 (keep forever).
	serverAuditRetention := services.NewServerAuditRetentionService(pgStore)
	serverAuditRetention.SetLeader(coreLeader)
	serverAuditRetention.Start(context.Background())

	// Fallback cleanup if TimescaleDB retention policy is not active.
	// Leader-gated (Phase 0b) so under multi-Core only one instance
	// fires the hourly DELETE — the followers idle on the tick.
	go func() {
		for range time.NewTicker(1 * time.Hour).C {
			if !coreLeader.IsLeader() {
				continue
			}
			db.Exec("DELETE FROM server_stats WHERE time < NOW() - INTERVAL '24 hours'")
		}
	}()

	// Gateway always active — uses same Redis as Core
	appState.Gateway = services.NewRedisGateway(redisClient, pgStore, cfg.ClusterSecret)

	// Routing migration service for batch redeployment when mode changes
	appState.RoutingMigration = services.NewRoutingMigrationService(pgStore, appState.Queue, redisClient)

	// Publish routing modes to Redis on startup so Nodes pick them up immediately.
	// Always write (even defaults) so stale Redis values from a previous install don't persist.
	{
		mode, _ := pgStore.GetSetting("routing_mode")
		if mode == "" {
			mode = "ip_port"
		}
		redisClient.Set(context.Background(), "dylaris:routing_mode", mode, 0)

		fileMode, _ := pgStore.GetSetting("file_access_mode")
		if fileMode == "" {
			fileMode = "sftp"
		}
		redisClient.Set(context.Background(), "dylaris:file_access_mode", fileMode, 0)
	}

	// Handler initialisieren
	authHandler := handlers.NewAuthHandler(appState, cfg.JWTSecret)
	serverHandler := handlers.NewServerHandler(appState)
	nodeHandler := handlers.NewNodeHandler(appState)
	userHandler := handlers.NewUserHandler(appState)
	moduleHandler := handlers.NewModuleHandler(appState)
	systemHandler := handlers.NewSystemHandler(cfg.Region, cfg.CoreID)
	fileHandler := handlers.NewFileHandler(appState)
	nodeGRPCHandler := handlers.NewNodeGRPCHandler(appState)
	libraryHandler := handlers.NewLibraryHandler(appState)
	settingsHandler := handlers.NewSettingsHandler(appState, libraryHandler)
	placementHandler := handlers.NewPlacementHandler(appState)
	consoleHandler := handlers.NewConsoleHandler(appState)
	statsHandler := handlers.NewStatsHandler(appState)
	memberHandler := handlers.NewMemberHandler(appState)
	versionHandler := handlers.NewVersionHandler(appState)
	beamHandler := handlers.NewBeamHandler(appState, cfg.JWTSecret)
	backupHandler := handlers.NewBackupHandler(appState)
	regionsHandler := handlers.NewRegionsHandler(appState)
	userRegionsHandler := handlers.NewUserRegionsHandler(appState)
	authSettingsHandler := handlers.NewAuthSettingsHandler(appState)
	registrationHandler := handlers.NewRegistrationHandler(appState)
	passwordResetHandler := handlers.NewPasswordResetHandler(appState)
	securityQuestionsHandler := handlers.NewSecurityQuestionsHandler(appState)
	maintenanceHandler := handlers.NewMaintenanceHandler(appState)
	ticketCategoriesHandler := handlers.NewTicketCategoriesHandler(appState)
	ticketsHandler := handlers.NewTicketsHandler(appState)
	ticketSettingsHandler := handlers.NewTicketSettingsHandler(appState)
	ticketAttachmentsHandler := handlers.NewTicketAttachmentsHandler(appState)
	cannedResponsesHandler := handlers.NewCannedResponsesHandler(appState)
	notificationsHandler := handlers.NewNotificationsHandler(appState)
	serverAuditHandler := handlers.NewServerAuditHandler(appState)
	auditSettingsHandler := handlers.NewAuditSettingsHandler(appState)
	ticketMigrationHandler := handlers.NewTicketMigrationHandler(appState)
	systemEventsHandler := handlers.NewSystemEventsHandler(appState)
	scheduledTasksHandler := handlers.NewScheduledTasksHandler(appState)
	rconHandler := handlers.NewRconHandler(appState)
	apiKeysHandler := handlers.NewAPIKeysHandler(appState)
	modrinthHandler := handlers.NewModrinthHandler(appState, "Dylaris/0.10 (+https://github.com/Bartis-Dev/dylaris-platform)")
	serverModsHandler := handlers.NewServerModsHandler(appState)
	sparkHandler := handlers.NewSparkHandler(appState)
	serverTabsHandler := handlers.NewServerTabsHandler(appState)
	modrinthPATHandler := handlers.NewModrinthPATHandler(appState, cfg.ClusterSecret)
	modpacksHandler := handlers.NewModpacksHandler(appState)
	modpacksPublishHandler := handlers.NewModpacksPublishHandler(appState, modrinthPATHandler, "Dylaris/0.14 (+https://github.com/Bartis-Dev/dylaris-platform)")
	collaboratorsHandler := handlers.NewCollaboratorsHandler(appState, modrinthPATHandler, "Dylaris/0.14 (+https://github.com/Bartis-Dev/dylaris-platform)")
	usernameHistoryHandler := handlers.NewUsernameHistoryHandler(appState)
	accountPolicyHandler := handlers.NewAccountPolicyHandler(appState)

	// gRPC Server for Node connections (NodeService)
	grpcLookup := &nodegrpc.StoreAdapter{
		GetByToken: func(token string) (int, error) {
			node, err := pgStore.GetNodeByToken(token)
			if err != nil {
				return 0, err
			}
			return node.ID, nil
		},
	}
	go func() {
		if err := nodegrpc.StartGRPCServer(cfg.GRPCPort, grpcRegistry, grpcLookup, cfg.CoreID); err != nil {
			log.Fatalf("gRPC server error: %v", err)
		}
	}()

	// Core Heartbeat in Redis (so Nodes can discover this Core)
	coreHeartbeat := services.NewCoreHeartbeatService(redisClient, cfg.CoreID, cfg.Region, cfg.GRPCPort)
	coreHeartbeat.Start()

	// Backup scheduler — ticks once a minute, dispatches due jobs to nodes.
	// Wire the gRPC mesh in so retention deletes can reach node-local stores.
	// Leader-gated (Phase 0b): tick + Pub/Sub result processing run only on
	// the elected Core to avoid double-dispatch and double-result-write.
	backupScheduler := services.NewBackupScheduler(pgStore, redisClient)
	backupScheduler.SetRegistry(grpcRegistry)
	backupScheduler.SetLeader(coreLeader)
	backupScheduler.Start(context.Background())

	// Scheduled-tasks executor (Phase 8) — per-server cron jobs (restart, say).
	// Leader-gated, 30s tick. Publishes scheduled_tasks.changed via the SSE
	// channel after each dispatch so the panel updates last-run/next-run.
	scheduledTasksService := services.NewScheduledTaskService(pgStore, redisClient, appState.Queue, appState.Events)
	scheduledTasksService.SetLeader(coreLeader)
	scheduledTasksService.Start(context.Background())

	// Router & API Endpunkte einrichten
	r := mux.NewRouter()

	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Core Error: Endpoint not found (" + req.URL.Path + ")",
		})
	})

	r.MethodNotAllowedHandler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Core Error: Method " + req.Method + " not allowed for " + req.URL.Path,
		})
	})

	api := r.PathPrefix("/api").Subrouter().StrictSlash(true)

	// --- PUBLIC ENDPOINTS ---
	api.HandleFunc("/auth/login", authHandler.LoginHandler).Methods("POST")
	api.HandleFunc("/status", authHandler.StatusHandler).Methods("GET")
	api.HandleFunc("/system/capabilities", systemHandler.GetCapabilities).Methods("GET")
	// Public — used by the topbar to display "Connected to <region> Core".
	api.HandleFunc("/system/core-info", systemHandler.GetCoreInfo).Methods("GET")
	// Phase 7 — SSE stream of platform-wide config-change events. Panel
	// subscribes once on boot and refreshes its caches reactively. Auth via
	// ?token= query param since EventSource can't set Authorization headers.
	api.HandleFunc("/system/events", authHandler.AuthMiddleware(systemEventsHandler.StreamEvents)).Methods("GET")

	// --- Scheduled Tasks (Phase 8) ---
	// Cron preview — pure transform, available to anyone authed.
	api.HandleFunc("/scheduled-tasks/validate", authHandler.AuthMiddleware(scheduledTasksHandler.ValidateCron)).Methods("POST")
	// Per-server CRUD. Access gated to power-class (owner/admin/permitted).
	api.HandleFunc("/servers/{id:[0-9]+}/scheduled-tasks", authHandler.AuthMiddleware(scheduledTasksHandler.List)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/scheduled-tasks", authHandler.AuthMiddleware(scheduledTasksHandler.Create)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/scheduled-tasks/{taskId:[0-9]+}", authHandler.AuthMiddleware(scheduledTasksHandler.Update)).Methods("PATCH")
	api.HandleFunc("/servers/{id:[0-9]+}/scheduled-tasks/{taskId:[0-9]+}", authHandler.AuthMiddleware(scheduledTasksHandler.Delete)).Methods("DELETE")

	// --- RCON + API keys (Phase 9) ---
	// Panel-internal RCON. Power-class permission enforced inside the handler.
	api.HandleFunc("/servers/{id:[0-9]+}/rcon", authHandler.AuthMiddleware(rconHandler.ExecForUser)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/rcon/config", authHandler.AuthMiddleware(rconHandler.GetConfig)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/rcon/config", authHandler.AuthMiddleware(rconHandler.SetConfig)).Methods("PUT")
	// Per-user API key CRUD (panel surface).
	api.HandleFunc("/me/api-keys", authHandler.AuthMiddleware(apiKeysHandler.List)).Methods("GET")
	api.HandleFunc("/me/api-keys", authHandler.AuthMiddleware(apiKeysHandler.Create)).Methods("POST")
	api.HandleFunc("/me/api-keys/{id:[0-9]+}", authHandler.AuthMiddleware(apiKeysHandler.Revoke)).Methods("DELETE")
	// External RCON: Authorization: Bearer dyl_<key>. Scope check on the
	// path-uuid happens in the middleware itself.
	api.HandleFunc("/external/rcon/{uuid}/exec", apiKeysHandler.APIKeyMiddleware("rcon.exec")(rconHandler.ExecExternal)).Methods("POST")

	// --- Modrinth proxy + per-server mod install (Phase 10) ---
	// Browse + project metadata are cached in Redis (5 min / 1 h). All authed.
	api.HandleFunc("/modrinth/search", authHandler.AuthMiddleware(modrinthHandler.Search)).Methods("GET")
	api.HandleFunc("/modrinth/project/{slug}", authHandler.AuthMiddleware(modrinthHandler.Project)).Methods("GET")
	api.HandleFunc("/modrinth/project/{slug}/versions", authHandler.AuthMiddleware(modrinthHandler.ProjectVersions)).Methods("GET")
	api.HandleFunc("/modrinth/version/{id}", authHandler.AuthMiddleware(modrinthHandler.Version)).Methods("GET")
	// Per-server installed mods + install/uninstall dispatch.
	api.HandleFunc("/servers/{id:[0-9]+}/mods", authHandler.AuthMiddleware(serverModsHandler.List)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/mods", authHandler.AuthMiddleware(serverModsHandler.Install)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/mods/{modId:[0-9]+}", authHandler.AuthMiddleware(serverModsHandler.Uninstall)).Methods("DELETE")

	// --- Spark profiles (Phase 11) ---
	api.HandleFunc("/servers/{id:[0-9]+}/spark/profiles", authHandler.AuthMiddleware(sparkHandler.List)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/spark/profiles", authHandler.AuthMiddleware(sparkHandler.Record)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/spark/profiles/{profileId:[0-9]+}", authHandler.AuthMiddleware(sparkHandler.Delete)).Methods("DELETE")

	// --- Custom Tabs (Phase 13) ---
	api.HandleFunc("/servers/{id:[0-9]+}/tabs", authHandler.AuthMiddleware(serverTabsHandler.List)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/tabs", authHandler.AuthMiddleware(serverTabsHandler.Create)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/tabs/{tabId:[0-9]+}", authHandler.AuthMiddleware(serverTabsHandler.Update)).Methods("PATCH")
	api.HandleFunc("/servers/{id:[0-9]+}/tabs/{tabId:[0-9]+}", authHandler.AuthMiddleware(serverTabsHandler.Delete)).Methods("DELETE")

	// --- Modrinth PAT (Phase 14) ---
	api.HandleFunc("/me/modrinth-pat", authHandler.AuthMiddleware(modrinthPATHandler.Status)).Methods("GET")
	api.HandleFunc("/me/modrinth-pat", authHandler.AuthMiddleware(modrinthPATHandler.Set)).Methods("PUT")
	api.HandleFunc("/me/modrinth-pat", authHandler.AuthMiddleware(modrinthPATHandler.Clear)).Methods("DELETE")
	// --- Modpacks CRUD (Phase 14.1) ---
	api.HandleFunc("/me/modpacks", authHandler.AuthMiddleware(modpacksHandler.List)).Methods("GET")
	api.HandleFunc("/me/modpacks", authHandler.AuthMiddleware(modpacksHandler.Create)).Methods("POST")
	api.HandleFunc("/modpacks/{id:[0-9]+}", authHandler.AuthMiddleware(modpacksHandler.Get)).Methods("GET")
	api.HandleFunc("/modpacks/{id:[0-9]+}", authHandler.AuthMiddleware(modpacksHandler.Update)).Methods("PATCH")
	api.HandleFunc("/modpacks/{id:[0-9]+}", authHandler.AuthMiddleware(modpacksHandler.Delete)).Methods("DELETE")
	api.HandleFunc("/modpacks/{id:[0-9]+}/versions", authHandler.AuthMiddleware(modpacksHandler.ListVersions)).Methods("GET")
	api.HandleFunc("/modpacks/{id:[0-9]+}/versions", authHandler.AuthMiddleware(modpacksHandler.CreateVersion)).Methods("POST")
	api.HandleFunc("/modpacks/{id:[0-9]+}/versions/{versionId:[0-9]+}", authHandler.AuthMiddleware(modpacksHandler.DeleteVersion)).Methods("DELETE")
	api.HandleFunc("/modpacks/{id:[0-9]+}/versions/{versionId:[0-9]+}/mods", authHandler.AuthMiddleware(modpacksHandler.ListMods)).Methods("GET")
	api.HandleFunc("/modpacks/{id:[0-9]+}/versions/{versionId:[0-9]+}/mods", authHandler.AuthMiddleware(modpacksHandler.AddMod)).Methods("POST")
	api.HandleFunc("/modpacks/{id:[0-9]+}/versions/{versionId:[0-9]+}/mods/{modId:[0-9]+}", authHandler.AuthMiddleware(modpacksHandler.RemoveMod)).Methods("DELETE")
	// .mrpack export — query-token auth so it can be opened via window.open
	// without setting custom headers.
	api.HandleFunc("/modpacks/{id:[0-9]+}/versions/{versionId:[0-9]+}/mrpack", authHandler.AuthMiddleware(modpacksHandler.ExportMrpack)).Methods("GET")
	// --- Modrinth publish + collaborators (Phase 14.3) ---
	api.HandleFunc("/modpacks/{id:[0-9]+}/versions/{versionId:[0-9]+}/publish", authHandler.AuthMiddleware(modpacksPublishHandler.Publish)).Methods("POST")
	api.HandleFunc("/modpacks/{id:[0-9]+}/collaborators", authHandler.AuthMiddleware(collaboratorsHandler.List)).Methods("GET")
	api.HandleFunc("/modpacks/{id:[0-9]+}/collaborators", authHandler.AuthMiddleware(collaboratorsHandler.Add)).Methods("POST")
	api.HandleFunc("/modpacks/{id:[0-9]+}/collaborators/{modrinthUserId}", authHandler.AuthMiddleware(collaboratorsHandler.Remove)).Methods("DELETE")
	// --- Phase 15 — Username history + account policy ---
	api.HandleFunc("/me/username-history", authHandler.AuthMiddleware(usernameHistoryHandler.Me)).Methods("GET")
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/username-history", authHandler.AuthMiddleware(usernameHistoryHandler.Admin)).Methods("GET")
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/username", authHandler.AuthMiddleware(usernameHistoryHandler.AdminRename)).Methods("PATCH")
	api.HandleFunc("/admin/settings/users", authHandler.AuthMiddleware(accountPolicyHandler.Get)).Methods("GET")
	api.HandleFunc("/admin/settings/users", authHandler.AuthMiddleware(accountPolicyHandler.Set)).Methods("PUT")
	api.HandleFunc("/node/connect", nodeGRPCHandler.NodeConnectHandler).Methods("GET", "POST")

	// --- PROTECTED ENDPOINTS ---
	api.HandleFunc("/auth/profile", authHandler.AuthMiddleware(authHandler.GetProfileHandler)).Methods("GET")
	api.HandleFunc("/auth/profile", authHandler.AuthMiddleware(authHandler.UpdateProfileHandler)).Methods("PUT")
	api.HandleFunc("/auth/2fa/setup", authHandler.AuthMiddleware(authHandler.SetupTOTPHandler)).Methods("POST")
	api.HandleFunc("/auth/2fa/verify", authHandler.AuthMiddleware(authHandler.VerifyTOTPHandler)).Methods("POST")
	api.HandleFunc("/auth/2fa/disable", authHandler.AuthMiddleware(authHandler.DisableTOTPHandler)).Methods("POST")
	api.HandleFunc("/auth/2fa/regenerate-backup-codes", authHandler.AuthMiddleware(authHandler.RegenerateBackupCodesHandler)).Methods("POST")
	api.HandleFunc("/auth/2fa/status", authHandler.AuthMiddleware(authHandler.Get2FAStatusHandler)).Methods("GET")
	api.HandleFunc("/users/{id:[0-9a-f-]{36}}/2fa", authHandler.AuthMiddleware(authHandler.AdminResetTOTPHandler)).Methods("DELETE")

	api.HandleFunc("/users", authHandler.AuthMiddleware(userHandler.GetAllUsers)).Methods("GET")
	api.HandleFunc("/users", authHandler.AuthMiddleware(userHandler.CreateUser)).Methods("POST")
	api.HandleFunc("/users/{id:[0-9a-f-]{36}}", authHandler.AuthMiddleware(userHandler.DeleteUser)).Methods("DELETE")
	api.HandleFunc("/users/{id:[0-9a-f-]{36}}/password", authHandler.AuthMiddleware(userHandler.ResetUserPassword)).Methods("PUT")
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/cancel-deletion", authHandler.AuthMiddleware(userHandler.CancelUserDeletion)).Methods("POST")

	// --- Roles + capability flags (Phase 1) ---
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/role", authHandler.AuthMiddleware(userHandler.SetUserRoleHandler)).Methods("PUT")
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/permissions", authHandler.AuthMiddleware(userHandler.SetUserPermissionsHandler)).Methods("PUT")

	// --- Maintenance mode (Phase 1) ---
	// Public state — drives the banner; never blocked by the maintenance middleware.
	api.HandleFunc("/maintenance", maintenanceHandler.GetState).Methods("GET")
	api.HandleFunc("/admin/maintenance", authHandler.AuthMiddleware(maintenanceHandler.SaveState)).Methods("PUT")

	// --- Tickets (Phase 2) ---
	// Categories: public list (enabled only) for create form, admin CRUD for management.
	api.HandleFunc("/ticket-categories", authHandler.AuthMiddleware(ticketCategoriesHandler.ListCategories)).Methods("GET")
	api.HandleFunc("/admin/ticket-categories", authHandler.AuthMiddleware(ticketCategoriesHandler.AdminListCategories)).Methods("GET")
	api.HandleFunc("/admin/ticket-categories", authHandler.AuthMiddleware(ticketCategoriesHandler.CreateCategory)).Methods("POST")
	api.HandleFunc("/admin/ticket-categories/{id:[0-9]+}", authHandler.AuthMiddleware(ticketCategoriesHandler.UpdateCategory)).Methods("PATCH")
	api.HandleFunc("/admin/ticket-categories/{id:[0-9]+}", authHandler.AuthMiddleware(ticketCategoriesHandler.DeleteCategory)).Methods("DELETE")

	// Tickets: user CRUD + support inbox.
	api.HandleFunc("/tickets", authHandler.AuthMiddleware(ticketsHandler.ListMyTickets)).Methods("GET")
	api.HandleFunc("/tickets", authHandler.AuthMiddleware(ticketsHandler.CreateTicket)).Methods("POST")
	api.HandleFunc("/tickets/inbox", authHandler.AuthMiddleware(ticketsHandler.ListInboxTickets)).Methods("GET")
	api.HandleFunc("/tickets/{id:[0-9]+}", authHandler.AuthMiddleware(ticketsHandler.GetTicket)).Methods("GET")
	api.HandleFunc("/tickets/{id:[0-9]+}/messages", authHandler.AuthMiddleware(ticketsHandler.AddReply)).Methods("POST")
	api.HandleFunc("/tickets/{id:[0-9]+}/status", authHandler.AuthMiddleware(ticketsHandler.UpdateStatus)).Methods("PATCH")
	api.HandleFunc("/tickets/{id:[0-9]+}/priority", authHandler.AuthMiddleware(ticketsHandler.UpdatePriority)).Methods("PATCH")
	api.HandleFunc("/tickets/{id:[0-9]+}/assignment", authHandler.AuthMiddleware(ticketsHandler.UpdateAssignment)).Methods("PATCH")
	api.HandleFunc("/tickets/{id:[0-9]+}/watchers", authHandler.AuthMiddleware(ticketsHandler.AddWatcher)).Methods("POST")
	api.HandleFunc("/tickets/{id:[0-9]+}/watchers/{userId:[0-9a-f-]{36}}", authHandler.AuthMiddleware(ticketsHandler.RemoveWatcher)).Methods("DELETE")

	// Sidebar source for support's "Via tickets" tab.
	api.HandleFunc("/me/servers/via-tickets", authHandler.AuthMiddleware(ticketsHandler.ListMyServersViaTickets)).Methods("GET")

	// Settings.
	api.HandleFunc("/admin/settings/tickets", authHandler.AuthMiddleware(ticketSettingsHandler.GetSettings)).Methods("GET")
	api.HandleFunc("/admin/settings/tickets", authHandler.AuthMiddleware(ticketSettingsHandler.SaveSettings)).Methods("PUT")

	// --- Tickets Phase 3: attachments, canned responses, notifications ---
	api.HandleFunc("/tickets/{id:[0-9]+}/attachments", authHandler.AuthMiddleware(ticketAttachmentsHandler.UploadAttachment)).Methods("POST")
	api.HandleFunc("/tickets/{id:[0-9]+}/attachments", authHandler.AuthMiddleware(ticketAttachmentsHandler.ListAttachments)).Methods("GET")
	api.HandleFunc("/tickets/{id:[0-9]+}/attachments/{aid:[0-9]+}/download", authHandler.AuthMiddleware(ticketAttachmentsHandler.DownloadAttachment)).Methods("GET")
	api.HandleFunc("/tickets/{id:[0-9]+}/attachments/{aid:[0-9]+}", authHandler.AuthMiddleware(ticketAttachmentsHandler.DeleteAttachment)).Methods("DELETE")

	// Canned responses: support sees the read list, admin manages.
	api.HandleFunc("/ticket-canned-responses", authHandler.AuthMiddleware(cannedResponsesHandler.ListForSupport)).Methods("GET")
	api.HandleFunc("/admin/ticket-canned-responses", authHandler.AuthMiddleware(cannedResponsesHandler.AdminList)).Methods("GET")
	api.HandleFunc("/admin/ticket-canned-responses", authHandler.AuthMiddleware(cannedResponsesHandler.Create)).Methods("POST")
	api.HandleFunc("/admin/ticket-canned-responses/{id:[0-9]+}", authHandler.AuthMiddleware(cannedResponsesHandler.Update)).Methods("PATCH")
	api.HandleFunc("/admin/ticket-canned-responses/{id:[0-9]+}", authHandler.AuthMiddleware(cannedResponsesHandler.Delete)).Methods("DELETE")

	// Notifications: in-app inbox.
	api.HandleFunc("/notifications", authHandler.AuthMiddleware(notificationsHandler.List)).Methods("GET")
	api.HandleFunc("/notifications/unread-count", authHandler.AuthMiddleware(notificationsHandler.UnreadCount)).Methods("GET")
	api.HandleFunc("/notifications/{id:[0-9]+}/read", authHandler.AuthMiddleware(notificationsHandler.MarkRead)).Methods("POST")
	api.HandleFunc("/notifications/read-all", authHandler.AuthMiddleware(notificationsHandler.MarkAllRead)).Methods("POST")

	// --- Server audit (Phase 4) ---
	// Owner + admin can view. Force-on flag is admin-only.
	api.HandleFunc("/servers/{id:[0-9]+}/audit", authHandler.AuthMiddleware(serverAuditHandler.ListAudit)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/audit/status", authHandler.AuthMiddleware(serverAuditHandler.GetStatus)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/audit/force", authHandler.AuthMiddleware(serverAuditHandler.SetForce)).Methods("PUT")
	// Platform-wide audit retention policy.
	api.HandleFunc("/admin/settings/audit", authHandler.AuthMiddleware(auditSettingsHandler.GetPolicy)).Methods("GET")
	api.HandleFunc("/admin/settings/audit", authHandler.AuthMiddleware(auditSettingsHandler.SavePolicy)).Methods("PUT")

	// --- Ticket DB migration + backups (Phase 5, admin-only) ---
	api.HandleFunc("/admin/tickets/migration/status", authHandler.AuthMiddleware(ticketMigrationHandler.GetStatus)).Methods("GET")
	api.HandleFunc("/admin/tickets/migration/test-connection", authHandler.AuthMiddleware(ticketMigrationHandler.TestExternalConnection)).Methods("POST")
	api.HandleFunc("/admin/tickets/migration/dry-run", authHandler.AuthMiddleware(ticketMigrationHandler.DryRunMigration)).Methods("POST")
	api.HandleFunc("/admin/tickets/migration/execute", authHandler.AuthMiddleware(ticketMigrationHandler.ExecuteMigration)).Methods("POST")
	// Backups: create, list, download, delete.
	api.HandleFunc("/admin/tickets/backup", authHandler.AuthMiddleware(ticketMigrationHandler.CreateBackup)).Methods("POST")
	api.HandleFunc("/admin/tickets/backups", authHandler.AuthMiddleware(ticketMigrationHandler.ListBackups)).Methods("GET")
	api.HandleFunc("/admin/tickets/backups/{name}/download", authHandler.AuthMiddleware(ticketMigrationHandler.DownloadBackup)).Methods("GET")
	api.HandleFunc("/admin/tickets/backups/{name}", authHandler.AuthMiddleware(ticketMigrationHandler.DeleteBackup)).Methods("DELETE")
	// Restore: two-step Danger Zone (init + execute) — 2FA + 15s timer + typed phrase.
	api.HandleFunc("/admin/tickets/restore/init", authHandler.AuthMiddleware(ticketMigrationHandler.InitRestore)).Methods("POST")
	api.HandleFunc("/admin/tickets/restore/execute", authHandler.AuthMiddleware(ticketMigrationHandler.ExecuteRestore)).Methods("POST")
	api.HandleFunc("/users/{id:[0-9a-f-]{36}}/route-limit", authHandler.AuthMiddleware(userHandler.GetUserRouteLimit)).Methods("GET")
	api.HandleFunc("/users/{id:[0-9a-f-]{36}}/route-limit", authHandler.AuthMiddleware(userHandler.SetUserRouteLimit)).Methods("PUT")

	api.HandleFunc("/modules", authHandler.AuthMiddleware(moduleHandler.GetModulesHandler)).Methods("GET")
	api.HandleFunc("/modules", authHandler.AuthMiddleware(moduleHandler.CreateModuleHandler)).Methods("POST")
	api.HandleFunc("/modules/{id:[0-9]+}", authHandler.AuthMiddleware(moduleHandler.DeleteModuleHandler)).Methods("DELETE")
	api.HandleFunc("/modules/{id:[0-9]+}/toggle", authHandler.AuthMiddleware(moduleHandler.ToggleModuleHandler)).Methods("PATCH")
	api.HandleFunc("/modules/{id:[0-9]+}/position", authHandler.AuthMiddleware(moduleHandler.UpdateModulePositionHandler)).Methods("PATCH")
	api.HandleFunc("/modules/{id:[0-9]+}/role", authHandler.AuthMiddleware(moduleHandler.SetModuleAccessRoleHandler)).Methods("PATCH")

	api.HandleFunc("/nodes", authHandler.AuthMiddleware(nodeHandler.GetNodes)).Methods("GET")
	api.HandleFunc("/nodes", authHandler.AuthMiddleware(nodeHandler.CreateNode)).Methods("POST")
	api.HandleFunc("/nodes/{id:[0-9]+}", authHandler.AuthMiddleware(nodeHandler.UpdateNode)).Methods("PUT")
	api.HandleFunc("/nodes/{id:[0-9]+}", authHandler.AuthMiddleware(nodeHandler.DeleteNode)).Methods("DELETE")
	api.HandleFunc("/nodes/{id:[0-9]+}/servers", authHandler.AuthMiddleware(nodeHandler.GetNodeServers)).Methods("GET")
	api.HandleFunc("/nodes/{id:[0-9]+}/force", authHandler.AuthMiddleware(nodeHandler.ForceDeleteNode)).Methods("DELETE")
	api.HandleFunc("/nodes/{id:[0-9]+}/storage", authHandler.AuthMiddleware(nodeHandler.GetNodeStorage)).Methods("GET")

	// Admin endpoints
	api.HandleFunc("/admin/servers", authHandler.AuthMiddleware(serverHandler.GetAdminServers)).Methods("GET")
	api.HandleFunc("/admin/servers/{id:[0-9]+}/owner", authHandler.AuthMiddleware(serverHandler.AdminUpdateServerOwner)).Methods("PATCH")
	api.HandleFunc("/admin/nodes/{id:[0-9]+}/disk-analysis", authHandler.AuthMiddleware(nodeHandler.GetDiskAnalysis)).Methods("GET")
	api.HandleFunc("/admin/nodes/{id:[0-9]+}/orphan", authHandler.AuthMiddleware(nodeHandler.DeleteOrphanedFolder)).Methods("DELETE")

	// Orphan file browser (admin-only, read-only — no DB servers row required)
	api.HandleFunc("/disk/orphans/{nodeId:[0-9]+}/{uuid}/files", authHandler.AuthMiddleware(nodeHandler.ListOrphanFiles)).Methods("GET")
	api.HandleFunc("/disk/orphans/{nodeId:[0-9]+}/{uuid}/content", authHandler.AuthMiddleware(nodeHandler.GetOrphanFileContent)).Methods("GET")
	api.HandleFunc("/disk/orphans/{nodeId:[0-9]+}/{uuid}/inspect", authHandler.AuthMiddleware(nodeHandler.InspectOrphan)).Methods("GET")
	api.HandleFunc("/disk/orphans/assign", authHandler.AuthMiddleware(nodeHandler.AssignOrphan)).Methods("POST")

	api.HandleFunc("/servers", authHandler.AuthMiddleware(serverHandler.GetServers)).Methods("GET")
	api.HandleFunc("/servers", authHandler.AuthMiddleware(serverHandler.CreateServer)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/power", authHandler.AuthMiddleware(serverHandler.ServerPowerHandler)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/install-cooldown", authHandler.AuthMiddleware(serverHandler.GetInstallCooldown)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/setup", authHandler.AuthMiddleware(serverHandler.SetupServer)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/reinstall", authHandler.AuthMiddleware(serverHandler.ReinstallServer)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/switch", authHandler.AuthMiddleware(serverHandler.SwitchSubServer)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/name", authHandler.AuthMiddleware(serverHandler.UpdateServerName)).Methods("PATCH")
	api.HandleFunc("/servers/{id:[0-9]+}/resources", authHandler.AuthMiddleware(serverHandler.UpdateServerResources)).Methods("PATCH")
	api.HandleFunc("/servers/{id:[0-9]+}/console/history", authHandler.AuthMiddleware(consoleHandler.GetHistory)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/console/stream", authHandler.AuthMiddleware(consoleHandler.StreamConsole)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/console/command", authHandler.AuthMiddleware(consoleHandler.SendCommand)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/stats/stream", authHandler.AuthMiddleware(statsHandler.StreamStats)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/stats/history", authHandler.AuthMiddleware(statsHandler.GetHistory)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/stats/disk", authHandler.AuthMiddleware(statsHandler.GetDisk)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/members", authHandler.AuthMiddleware(memberHandler.GetMembers)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/members", authHandler.AuthMiddleware(memberHandler.InviteMember)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/members/inherited", authHandler.AuthMiddleware(memberHandler.GetInheritedMembers)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/members/{userId:[0-9a-f-]{36}}", authHandler.AuthMiddleware(memberHandler.UpdateMemberPermissions)).Methods("PATCH")
	api.HandleFunc("/servers/{id:[0-9]+}/members/{userId:[0-9a-f-]{36}}", authHandler.AuthMiddleware(memberHandler.RemoveMember)).Methods("DELETE")
	api.HandleFunc("/servers/{id:[0-9]+}", authHandler.AuthMiddleware(serverHandler.DeleteServer)).Methods("DELETE")
	api.HandleFunc("/servers/{id:[0-9]+}/sub-servers/{subServerName}", authHandler.AuthMiddleware(serverHandler.DeleteSubServer)).Methods("DELETE")
	api.HandleFunc("/servers/{id:[0-9]+}/proxy", authHandler.AuthMiddleware(serverHandler.LinkServerToProxy)).Methods("PUT")
	api.HandleFunc("/servers/{id:[0-9]+}/proxy", authHandler.AuthMiddleware(serverHandler.UnlinkServerFromProxy)).Methods("DELETE")
	api.HandleFunc("/servers/{id:[0-9]+}/proxy-endpoint", authHandler.AuthMiddleware(serverHandler.GetProxyEndpoint)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/storage-path", authHandler.AuthMiddleware(serverHandler.GetServerStoragePath)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/migrate-storage", authHandler.AuthMiddleware(serverHandler.MigrateServerStorage)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/sftp-credentials", authHandler.AuthMiddleware(serverHandler.GetSftpCredentials)).Methods("GET")

	api.HandleFunc("/versions/software", versionHandler.GetSoftwareList).Methods("GET")
	api.HandleFunc("/versions", authHandler.AuthMiddleware(versionHandler.GetVersions)).Methods("GET")

	// --- Gateway Endpoints ---
	gatewayHandler := handlers.NewGatewayHandler(appState)
	infrastructureHandler := handlers.NewInfrastructureHandler(appState)

	// Admin endpoints
	api.HandleFunc("/gateway/links", authHandler.AuthMiddleware(gatewayHandler.GetLinks)).Methods("GET")
	api.HandleFunc("/gateway/edges", authHandler.AuthMiddleware(gatewayHandler.GetEdges)).Methods("GET")
	api.HandleFunc("/gateway/routes", authHandler.AuthMiddleware(gatewayHandler.GetAllRoutes)).Methods("GET")
	api.HandleFunc("/gateway/routes/suffixes", authHandler.AuthMiddleware(gatewayHandler.GetRouteSuffixes)).Methods("GET")
	api.HandleFunc("/gateway/routes/bulk-delete", authHandler.AuthMiddleware(gatewayHandler.BulkDeleteRoutesBySuffix)).Methods("POST")
	api.HandleFunc("/gateway/routes/{domain:.+}", authHandler.AuthMiddleware(gatewayHandler.AdminDeleteRoute)).Methods("DELETE")
	api.HandleFunc("/gateway/check-domain", authHandler.AuthMiddleware(gatewayHandler.CheckDomainAvailability)).Methods("GET")
	api.HandleFunc("/gateway/logs", authHandler.AuthMiddleware(gatewayHandler.GetLogs)).Methods("GET")
	api.HandleFunc("/gateway/stats", authHandler.AuthMiddleware(gatewayHandler.GetStats)).Methods("GET")
	api.HandleFunc("/gateway/sync", authHandler.AuthMiddleware(gatewayHandler.TriggerSync)).Methods("POST")
	api.HandleFunc("/gateway/errors", authHandler.AuthMiddleware(gatewayHandler.GetErrors)).Methods("GET")

	// User endpoints (per-server routes, identified by domain)
	api.HandleFunc("/servers/{id:[0-9]+}/routes", authHandler.AuthMiddleware(gatewayHandler.GetServerRoutes)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/routes", authHandler.AuthMiddleware(gatewayHandler.CreateServerRoute)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/routes/{domain:.+}", authHandler.AuthMiddleware(gatewayHandler.DeleteServerRoute)).Methods("DELETE")

	// Infrastructure overview + migration status
	api.HandleFunc("/infrastructure/overview", authHandler.AuthMiddleware(infrastructureHandler.GetOverview)).Methods("GET")
	api.HandleFunc("/infrastructure/routing-migration", authHandler.AuthMiddleware(infrastructureHandler.GetRoutingMigrationStatus)).Methods("GET")

	// XDP / eBPF DDoS Protection (deployment-wide config managed by Panel,
	// consumed by every Edge replica via Redis poll)
	xdpHandler := handlers.NewXDPHandler(appState)
	api.HandleFunc("/admin/xdp/config", authHandler.AuthMiddleware(xdpHandler.GetConfig)).Methods("GET")
	api.HandleFunc("/admin/xdp/config", authHandler.AuthMiddleware(xdpHandler.UpdateConfig)).Methods("PUT")

	api.HandleFunc("/files", authHandler.AuthMiddleware(fileHandler.GetFilesHandler)).Methods("GET")
	api.HandleFunc("/files/content", authHandler.AuthMiddleware(fileHandler.GetFileContentHandler)).Methods("GET")
	api.HandleFunc("/files/save", authHandler.AuthMiddleware(fileHandler.SaveFileHandler)).Methods("POST")
	api.HandleFunc("/files/create", authHandler.AuthMiddleware(fileHandler.CreateFileHandler)).Methods("POST")
	api.HandleFunc("/files/rename", authHandler.AuthMiddleware(fileHandler.RenameFileHandler)).Methods("POST")
	api.HandleFunc("/files/copy", authHandler.AuthMiddleware(fileHandler.CopyFileHandler)).Methods("POST")
	api.HandleFunc("/files/delete", authHandler.AuthMiddleware(fileHandler.DeleteFileHandler)).Methods("POST")
	api.HandleFunc("/files/download", authHandler.AuthMiddleware(fileHandler.DownloadFileHandler)).Methods("GET")
	api.HandleFunc("/files/download/selective", authHandler.AuthMiddleware(fileHandler.SelectiveDownloadHandler)).Methods("GET")
	api.HandleFunc("/files/upload", authHandler.AuthMiddleware(fileHandler.UploadFileHandler)).Methods("POST")

	// Library Endpunkte
	api.HandleFunc("/library", authHandler.AuthMiddleware(libraryHandler.GetLibraryHandler)).Methods("GET")
	api.HandleFunc("/library/delete", authHandler.AuthMiddleware(libraryHandler.DeleteLibraryHandler)).Methods("POST")
	api.HandleFunc("/library/mkdir", authHandler.AuthMiddleware(libraryHandler.MkdirLibraryHandler)).Methods("POST")
	api.HandleFunc("/library/upload", authHandler.AuthMiddleware(libraryHandler.UploadLibraryHandler)).Methods("POST")
	api.HandleFunc("/library/download", authHandler.AuthMiddleware(libraryHandler.DownloadLibraryHandler)).Methods("GET")
	api.HandleFunc("/library/toggle", authHandler.AuthMiddleware(libraryHandler.ToggleLibraryPathHandler)).Methods("POST")

	// Settings Endpunkte
	api.HandleFunc("/settings/library", authHandler.AuthMiddleware(settingsHandler.GetLibrarySettings)).Methods("GET")
	api.HandleFunc("/settings/library", authHandler.AuthMiddleware(settingsHandler.SaveLibrarySettings)).Methods("POST")
	api.HandleFunc("/settings/library/test", authHandler.AuthMiddleware(settingsHandler.TestLibraryConnection)).Methods("GET")
	api.HandleFunc("/settings/filemanager", authHandler.AuthMiddleware(settingsHandler.GetFileManagerSettings)).Methods("GET")
	api.HandleFunc("/settings/filemanager", authHandler.AuthMiddleware(settingsHandler.SaveFileManagerSettings)).Methods("POST")
	api.HandleFunc("/settings/filemanager/limits", authHandler.AuthMiddleware(settingsHandler.GetUserLimits)).Methods("GET")
	api.HandleFunc("/settings/features", authHandler.AuthMiddleware(settingsHandler.GetFeatureSettings)).Methods("GET")
	api.HandleFunc("/settings/features", authHandler.AuthMiddleware(settingsHandler.SaveFeatureSettings)).Methods("POST")
	api.HandleFunc("/settings/gateway", authHandler.AuthMiddleware(settingsHandler.GetGatewaySettings)).Methods("GET")
	api.HandleFunc("/settings/gateway", authHandler.AuthMiddleware(settingsHandler.SaveGatewaySettings)).Methods("POST")

	// Placement / Scheduling
	api.HandleFunc("/settings/placement", authHandler.AuthMiddleware(settingsHandler.GetPlacementSettings)).Methods("GET")
	api.HandleFunc("/settings/placement", authHandler.AuthMiddleware(settingsHandler.SavePlacementSettings)).Methods("POST")
	api.HandleFunc("/placement/pick", authHandler.AuthMiddleware(placementHandler.PickNode)).Methods("POST")
	api.HandleFunc("/placement/tags", authHandler.AuthMiddleware(placementHandler.AvailableTagsHandler)).Methods("GET")
	api.HandleFunc("/placement/regions", authHandler.AuthMiddleware(placementHandler.AvailableRegionsHandler)).Methods("GET")
	api.HandleFunc("/nodes/{id:[0-9]+}/placement", authHandler.AuthMiddleware(placementHandler.SetNodePlacement)).Methods("PUT")

	// Server auto-move toggle
	api.HandleFunc("/servers/{id:[0-9]+}/automove", authHandler.AuthMiddleware(serverHandler.SetServerAutoMove)).Methods("PATCH")
	api.HandleFunc("/gateway/route-options", authHandler.AuthMiddleware(settingsHandler.GetGatewayRouteOptions)).Methods("GET")
	api.HandleFunc("/settings/servers", authHandler.AuthMiddleware(settingsHandler.GetServerSettings)).Methods("GET")
	api.HandleFunc("/settings/servers", authHandler.AuthMiddleware(settingsHandler.SaveServerSettings)).Methods("POST")
	api.HandleFunc("/settings/beam", authHandler.AuthMiddleware(settingsHandler.GetBeamSettings)).Methods("GET")
	api.HandleFunc("/settings/beam", authHandler.AuthMiddleware(settingsHandler.SaveBeamSettings)).Methods("POST")
	api.HandleFunc("/settings/routing-mode", authHandler.AuthMiddleware(settingsHandler.GetRoutingMode)).Methods("GET")
	api.HandleFunc("/settings/routing-mode", authHandler.AuthMiddleware(settingsHandler.SaveRoutingMode)).Methods("POST")
	api.HandleFunc("/settings/backup", authHandler.AuthMiddleware(settingsHandler.GetBackupConfig)).Methods("GET")
	api.HandleFunc("/settings/backup", authHandler.AuthMiddleware(settingsHandler.SaveBackupConfig)).Methods("POST")

	// --- Regions (Phase 0a.1) ---
	// User-facing: list of enabled regions (drives region pickers).
	api.HandleFunc("/regions", authHandler.AuthMiddleware(regionsHandler.ListRegions)).Methods("GET")
	api.HandleFunc("/me/regions", authHandler.AuthMiddleware(userRegionsHandler.GetMyRegions)).Methods("GET")
	// Admin: full CRUD incl. disabled regions.
	api.HandleFunc("/admin/regions", authHandler.AuthMiddleware(regionsHandler.AdminListRegions)).Methods("GET")
	api.HandleFunc("/admin/regions", authHandler.AuthMiddleware(regionsHandler.CreateRegion)).Methods("POST")
	api.HandleFunc("/admin/regions/{id}", authHandler.AuthMiddleware(regionsHandler.UpdateRegion)).Methods("PATCH")
	api.HandleFunc("/admin/regions/{id}", authHandler.AuthMiddleware(regionsHandler.DeleteRegion)).Methods("DELETE")
	// Admin: per-user region assignment.
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/regions", authHandler.AuthMiddleware(userRegionsHandler.GetUserRegions)).Methods("GET")
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/regions", authHandler.AuthMiddleware(userRegionsHandler.SetUserRegions)).Methods("PUT")

	// --- Registration + Email Verify (Phase 0a.2) ---
	// Public — login page polls registration-status to decide whether to show the register link.
	api.HandleFunc("/auth/registration-status", registrationHandler.RegistrationStatus).Methods("GET")
	api.HandleFunc("/auth/register", registrationHandler.Register).Methods("POST")
	api.HandleFunc("/auth/verify-email", registrationHandler.VerifyEmail).Methods("POST")
	api.HandleFunc("/auth/resend-verification", registrationHandler.ResendVerification).Methods("POST")

	// --- Password reset (Phase 0a.4) — all public, all enumeration-safe ---
	api.HandleFunc("/auth/forgot-password", passwordResetHandler.ForgotPassword).Methods("POST")
	api.HandleFunc("/auth/validate-reset-token", passwordResetHandler.ValidateResetToken).Methods("POST")
	api.HandleFunc("/auth/reset-password", passwordResetHandler.ResetPassword).Methods("POST")

	// --- Security questions (Phase 0a.5) ---
	// Public: pool query used by /register and /reset-password to render pickers.
	api.HandleFunc("/auth/security-questions/pool", securityQuestionsHandler.GetPool).Methods("GET")
	// Authenticated: user manages their own questions in profile.
	api.HandleFunc("/me/security-questions", authHandler.AuthMiddleware(securityQuestionsHandler.GetMyQuestions)).Methods("GET")
	api.HandleFunc("/me/security-questions", authHandler.AuthMiddleware(securityQuestionsHandler.SetMyQuestions)).Methods("PUT")
	// Admin: pool management.
	api.HandleFunc("/admin/settings/security-questions-pool", authHandler.AuthMiddleware(securityQuestionsHandler.GetAdminPool)).Methods("GET")
	api.HandleFunc("/admin/settings/security-questions-pool", authHandler.AuthMiddleware(securityQuestionsHandler.SetAdminPool)).Methods("PUT")

	// --- Auth policy + SMTP config (Phase 0a.2, admin) ---
	api.HandleFunc("/admin/settings/auth", authHandler.AuthMiddleware(authSettingsHandler.GetAuthPolicy)).Methods("GET")
	api.HandleFunc("/admin/settings/auth", authHandler.AuthMiddleware(authSettingsHandler.SaveAuthPolicy)).Methods("PUT")
	api.HandleFunc("/admin/settings/smtp", authHandler.AuthMiddleware(authSettingsHandler.GetSMTPConfig)).Methods("GET")
	api.HandleFunc("/admin/settings/smtp", authHandler.AuthMiddleware(authSettingsHandler.SaveSMTPConfig)).Methods("PUT")
	api.HandleFunc("/admin/settings/smtp/test", authHandler.AuthMiddleware(authSettingsHandler.TestSendSMTP)).Methods("POST")

	// --- Beam Endpoints ---
	api.HandleFunc("/beam/servers", authHandler.AuthMiddleware(beamHandler.GetBeamServers)).Methods("GET")
	api.HandleFunc("/beam/ticket", authHandler.AuthMiddleware(beamHandler.GetBeamTicket)).Methods("GET", "POST")
	api.HandleFunc("/beam/config", authHandler.AuthMiddleware(beamHandler.GetBeamConfig)).Methods("GET")
	api.HandleFunc("/beam/download", beamHandler.GetBeamDownload).Methods("GET")

	// --- Backup Endpoints ---
	api.HandleFunc("/backup-storages", authHandler.AuthMiddleware(backupHandler.ListStorages)).Methods("GET")
	api.HandleFunc("/backup-storages", authHandler.AuthMiddleware(backupHandler.CreateStorage)).Methods("POST")
	api.HandleFunc("/backup-storages/{id:[0-9]+}", authHandler.AuthMiddleware(backupHandler.UpdateStorage)).Methods("PATCH")
	api.HandleFunc("/backup-storages/{id:[0-9]+}", authHandler.AuthMiddleware(backupHandler.DeleteStorage)).Methods("DELETE")
	api.HandleFunc("/backup-storages/{id:[0-9]+}/test", authHandler.AuthMiddleware(backupHandler.TestStorage)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/backup-jobs", authHandler.AuthMiddleware(backupHandler.ListJobs)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/backup-jobs", authHandler.AuthMiddleware(backupHandler.CreateJob)).Methods("POST")
	api.HandleFunc("/backup-jobs/{jobId:[0-9]+}", authHandler.AuthMiddleware(backupHandler.UpdateJob)).Methods("PATCH")
	api.HandleFunc("/backup-jobs/{jobId:[0-9]+}", authHandler.AuthMiddleware(backupHandler.DeleteJob)).Methods("DELETE")
	api.HandleFunc("/backup-jobs/{jobId:[0-9]+}/trigger", authHandler.AuthMiddleware(backupHandler.TriggerJob)).Methods("POST")
	api.HandleFunc("/backup-jobs/{jobId:[0-9]+}/runs", authHandler.AuthMiddleware(backupHandler.ListRuns)).Methods("GET")
	api.HandleFunc("/backup-runs/{runId:[0-9]+}/download", authHandler.AuthMiddleware(backupHandler.DownloadRun)).Methods("GET")
	api.HandleFunc("/backup-runs/{runId:[0-9]+}/restore", authHandler.AuthMiddleware(backupHandler.RestoreRun)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/backup-restores", authHandler.AuthMiddleware(backupHandler.ListRestores)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/backup-usage", authHandler.AuthMiddleware(backupHandler.BackupUsage)).Methods("GET")
	api.HandleFunc("/backup-runs/{runId:[0-9]+}", authHandler.AuthMiddleware(backupHandler.DeleteRun)).Methods("DELETE")
	api.HandleFunc("/tools/beam", func(w http.ResponseWriter, r *http.Request) {
		// The Beam desktop app is now served by gateway/beam-relay's
		// /download/{os}-{arch} endpoint — see plan. Core redirects to it
		// using either the admin-configured beam.download_url setting or,
		// as a convenience, derives it from beam.relay_address by swapping
		// in the relay's HTTPS download port (default 25552).
		downloadBase, _ := appState.Store.GetSetting("beam.download_url")
		if downloadBase == "" {
			relayAddr, _ := appState.Store.GetSetting("beam.relay_address")
			if relayAddr != "" {
				// strip any existing port; relay download is :25552 by convention
				host := relayAddr
				if i := strings.LastIndex(host, ":"); i > 0 {
					host = host[:i]
				}
				downloadBase = "https://" + host + ":25552"
			}
		}
		if downloadBase == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Beam download URL not configured. Set beam.download_url or beam.relay_address in Settings → Gateway → Beam.",
			})
			return
		}

		platform := r.URL.Query().Get("platform")
		if platform == "" {
			platform = detectBeamPlatform(r.UserAgent())
		}
		target := strings.TrimSuffix(downloadBase, "/") + "/download/" + platform
		http.Redirect(w, r, target, http.StatusFound)
	}).Methods("GET")

	// allowedOrigin gates CORS. Beyond the configured Panel and the
	// local dev origin, the Beam Desktop App is allowed through: it
	// runs the Panel inside a Wails webview whose origin is
	// http://wails.localhost (Windows) or wails://wails.localhost
	// (macOS/Linux). Same Core API, just a native shell — and auth is
	// Bearer-token (no cookies), so a wider CORS surface grants no
	// ambient privilege.
	allowedOrigin := func(origin string) bool {
		if origin == cfg.FrontendURL || origin == "http://localhost:25510" {
			return true
		}
		if strings.HasPrefix(origin, "wails://") {
			return true
		}
		if u, err := url.Parse(origin); err == nil && u.Hostname() == "wails.localhost" {
			return true
		}
		return false
	}

	corsObj := gorillaHandlers.CORS(
		gorillaHandlers.AllowedOriginValidator(allowedOrigin),
		gorillaHandlers.AllowedMethods([]string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}),
		gorillaHandlers.AllowedHeaders([]string{"Content-Type", "Authorization"}),
	)

	port := cfg.APIPort
	if port == "" {
		port = "25500"
	}

	log.Printf("Dylaris Core API running on port %s", port)
	if err := http.ListenAndServe(":"+port, corsObj(r)); err != nil {
		log.Fatalf("Core API crashed: %v", err)
	}
}
