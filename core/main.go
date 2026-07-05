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
	"dylaris-core/models"
	"dylaris-core/pkg/leader"
	"dylaris-core/services"
	"dylaris-core/services/redisacl"
	"dylaris-core/store"
	beamauth "dylaris-pkg/beam/auth"

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

// aclHandshakeStore adapts the Core store to redisacl.HandshakeStore. Primitive
// types only, keeping the redisacl package free of store/models imports.
type aclHandshakeStore struct {
	store *store.PostgresStore
	flags *services.FeatureFlags
}

func (a *aclHandshakeStore) GetNodeSecretEnc(id int) (string, error) {
	return a.store.GetNodeSecretEnc(id)
}
func (a *aclHandshakeStore) SetNodeSecretEnc(id int, enc string) error {
	return a.store.SetNodeSecretEnc(id, enc)
}

func (a *aclHandshakeStore) ServerUUIDsByNode(nodeID int) ([]string, error) {
	servers, err := a.store.ListServersByNode(nodeID)
	if err != nil {
		return nil, err
	}
	uuids := make([]string, 0, len(servers))
	for _, s := range servers {
		uuids = append(uuids, s.UUID)
	}
	return uuids, nil
}

func (a *aclHandshakeStore) ResolveEnrollToken(plaintext string) (string, bool, error) {
	return a.store.ResolveNodeEnrollToken(plaintext)
}

func (a *aclHandshakeStore) ConsumeEnrollToken(plaintext string) (string, bool, error) {
	return a.store.ConsumeNodeEnrollToken(plaintext)
}

func (a *aclHandshakeStore) NodeIDByToken(token string) (int, bool, error) {
	n, err := a.store.GetNodeByToken(token)
	if err != nil {
		return 0, false, nil // unknown token: not found, not an error for this path
	}
	return n.ID, true, nil
}

// NodeLimitReached mirrors discovery.nodeLimitReached: only when BYON is on; a
// 0 / missing cap means unlimited; fail-open on store errors.
func (a *aclHandshakeStore) NodeLimitReached(ownerID string) bool {
	if a.flags == nil || !a.flags.IsBYONEnabled(context.Background()) {
		return false
	}
	lim, err := services.EffectiveLimits(a.store, ownerID)
	if err != nil || lim.MaxNodes <= 0 {
		return false
	}
	cnt, err := a.store.CountNodesByOwner(ownerID)
	if err != nil {
		return false
	}
	return int64(cnt) >= lim.MaxNodes
}

func (a *aclHandshakeStore) CreateBYONNode(token, address, ownerID, displayName string) (int, error) {
	n := &models.Node{Name: token, Token: token, Address: address, Status: "offline"}
	if err := a.store.CreateNode(n); err != nil {
		return 0, err
	}
	if err := a.store.SetNodeOwner(n.ID, &ownerID); err != nil {
		return 0, err
	}
	if displayName != "" {
		if err := a.store.SetNodeDisplayName(n.ID, displayName); err != nil {
			return 0, err
		}
	}
	return n.ID, nil
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
		FeatureFlags:        services.NewFeatureFlags(pgStore),
		DBType:              cfg.DBType,
		StoreEnabled:        cfg.StoreEnabled,
		StoreURL:            cfg.StoreURL,
		StoreSharedKey:      cfg.StoreSharedKey,
	}

	// Precompute the cluster-wide gRPC-TLS fingerprint once so handlers can hand it
	// to BYON operators without re-deriving. Non-secret; safe to expose.
	if fp, ferr := beamauth.ClusterGRPCCertFingerprint(cfg.ClusterSecret); ferr == nil {
		appState.GRPCTLSFingerprint = fp
	}

	redisClient, err := database.InitRedis(cfg)
	if err != nil {
		log.Fatalf("FATAL: Redis Error: %v", err)
	}

	appState.Redis = redisClient
	appState.Queue = services.NewQueueService(redisClient)

	// In-panel cross-database migration. The source is THIS Core's live DB,
	// re-opened read-only as the copy source; the target is supplied per-request.
	appState.DBMigration = services.NewDBMigrationService(redisClient, pgStore, services.DBConnParams{
		Host:     cfg.DBHost,
		Port:     cfg.DBPort,
		User:     cfg.DBUser,
		Password: cfg.DBPassword,
		DBName:   cfg.DBName,
		SSLMode:  cfg.DBSSLMode,
		DBType:   cfg.DBType,
	})

	// Per-server CPU pinning: reads node topology from Redis + computes auto cpusets.
	appState.CPUPinning = services.NewCPUPinningService(redisClient, pgStore)

	// System-events publisher. Mutating handlers (regions,
	// modules, features, maintenance, servers CRUD) drop events into a
	// single Redis Pub/Sub channel; panels subscribe via SSE so they refresh
	// without polling. Construction is cheap — wired before any handler so
	// every code path can call h.state.Events.Publish unconditionally.
	appState.Events = services.NewSystemEventsPublisher(redisClient)

	// Leader election: a single Redis lease named for the
	// "core-leader" role, identified by this instance's CoreID. Every
	// scheduled background loop consults the leader's IsLeader() to
	// decide whether to perform its work or idle. Single-instance Core
	// always wins the election so behavior is unchanged for dev. Multi-
	// instance Core safely converges on exactly one active leader.
	coreLeader := leader.New(redisClient, "dylaris:core:leader", cfg.CoreID)
	coreLeader.Start(context.Background())

	discovery := services.NewDiscoveryService(pgStore, redisClient, cfg.ClusterSecret)
	discovery.SetLeader(coreLeader)
	discovery.SetFeatureFlags(appState.FeatureFlags)
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

	// Traffic aggregator — leader-gated + BYON-gated. Turns the per-server byte
	// counters edges + relays publish to Redis into per-tenant monthly rows in
	// traffic_usage. No-op in solo/hoster mode (feature_byon_enabled off).
	trafficAggregator := services.NewTrafficAggregator(pgStore, redisClient, appState.FeatureFlags)
	trafficAggregator.SetLeader(coreLeader)
	trafficAggregator.Start(context.Background())

	// Billing lifecycle — leader-gated. Progresses past_due tenants whose grace
	// window has elapsed into suspended (stops servers, keeps data). Payment-
	// provider-agnostic; handlers/webhooks call EnterPastDue/Reactivate/Suspend.
	appState.Billing = services.NewBillingLifecycleService(pgStore, appState.Queue, grpcRegistry, cfg.FrontendURL)
	appState.Billing.SetLeader(coreLeader)
	appState.Billing.Start(context.Background())

	// DNS reconciler — leader-gated. Points each region's edge wildcard A record
	// at the live edge IPs via the DNS provider. Off unless DNS_UPDATER_ENABLED
	// and provider credentials are set; credentials live only here, never on edges.
	if cfg.DNSUpdaterEnabled {
		if provider := services.NewCloudflareProvider(cfg.CFAPIToken, cfg.CFZoneID); provider != nil {
			dnsReconciler := services.NewDNSReconciler(redisClient, provider)
			dnsReconciler.SetLeader(coreLeader)
			dnsReconciler.Start(context.Background())
		} else {
			log.Println("DNS updater enabled but Cloudflare credentials missing (CF_API_TOKEN / CF_ZONE_ID) — skipping")
		}
	}

	// Auto-delete service — daily ticker scans inactive users,
	// emails warnings, executes deletions per the auth.* settings. No-op
	// unless the operator turns it on. Leader-gated so only one Core runs
	// it under multi-instance.
	autoDelete := services.NewAutoDeleteService(pgStore, cfg.FrontendURL)
	autoDelete.SetLeader(coreLeader)
	autoDelete.Start(context.Background())

	// Ticket auto-close — daily ticker, leader-gated. No-op until
	// the operator turns it on via Settings → Ticket Settings.
	ticketAutoClose := services.NewTicketAutoCloseService(pgStore)
	ticketAutoClose.SetLeader(coreLeader)
	ticketAutoClose.Start(context.Background())

	// Server-audit retention sweep — daily, leader-gated. No-op
	// when audit.server_retention_days is 0 (keep forever).
	serverAuditRetention := services.NewServerAuditRetentionService(pgStore)
	serverAuditRetention.SetLeader(coreLeader)
	serverAuditRetention.Start(context.Background())

	// Modpack auto-update checker — hourly, leader-gated. Pauses when the
	// modpacks feature is off; per-row staleness governed by the admin cadence
	// setting (modpack_update_check_interval_hours, default 24h).
	modpackUpdateChecker := services.NewModpackUpdateChecker(pgStore, appState.FeatureFlags)
	modpackUpdateChecker.SetLeader(coreLeader)
	modpackUpdateChecker.Start(context.Background())

	// Fallback cleanup if TimescaleDB retention policy is not active.
	// Leader-gated so under multi-Core only one instance
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

	// Migration orchestrator — leader-gated consumer of the node-to-node
	// migration (auto-move) queue. Manual + (Wave 4) rebalance moves enqueue
	// requests; only the elected Core runs the step machine. Exposed on
	// AppState so the manual-move endpoint can EnqueueMigration.
	migrationOrchestrator := services.NewMigrationOrchestrator(pgStore, redisClient, appState.Queue, appState.Gateway, cfg.ClusterSecret)
	migrationOrchestrator.SetLeader(coreLeader)
	migrationOrchestrator.SetFeatureFlags(appState.FeatureFlags)
	migrationOrchestrator.Start(context.Background())
	appState.Migration = migrationOrchestrator
	migrationOrchestrator.SetCPUPinning(appState.CPUPinning)

	// Rebalance worker — leader-gated ticker that migrates eligible (auto_move,
	// 0-player) servers off overloaded nodes by enqueuing onto the orchestrator.
	// No-op unless auto-move is enabled AND gateway routing is active.
	rebalanceWorker := services.NewRebalanceWorker(pgStore, redisClient, migrationOrchestrator, appState.FeatureFlags)
	rebalanceWorker.SetLeader(coreLeader)
	rebalanceWorker.Start(context.Background())

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

	// Initialize handlers
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

	// Warp: external/home node WireGuard bridge (multi-hub registry).
	warpService := services.NewWarpService(pgStore, redisClient, cfg.ClusterSecret)
	warpHandler := handlers.NewWarpHandler(appState, warpService)
	warpService.StartResyncWatcher(coreLeader.IsLeader)
	// Seed a default region from any pre-multi-hub settings so an existing
	// single-hub deployment keeps working unchanged. Region id "leader-01" keeps
	// the WG key derived from CLUSTER_SECRET+region byte-identical to the old
	// single-leader key, so already-enrolled peers stay valid.
	{
		seedSubnet, _ := pgStore.GetSetting("warp:client_subnet")
		if seedSubnet == "" {
			seedSubnet = "10.0.99.0/24"
		}
		seedEndpoint, _ := pgStore.GetSetting("warp:leader_endpoint")
		if err := pgStore.SeedWarpRegionIfEmpty("leader-01", seedSubnet, "leader-01", seedEndpoint); err != nil {
			log.Printf("warp: seed default region: %v", err)
		}
	}

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
	packsHandler := handlers.NewPacksHandler(appState)
	packsHandler.SetPATLoader(modrinthPATHandler)
	solderHandler := handlers.NewSolderHandler(appState)
	usernameHistoryHandler := handlers.NewUsernameHistoryHandler(appState)
	accountPolicyHandler := handlers.NewAccountPolicyHandler(appState)
	modpackSettingsHandler := handlers.NewModpackSettingsHandler(appState)
	telemetrySettingsHandler := handlers.NewTelemetrySettingsHandler(appState)
	systemFeaturesHandler := handlers.NewSystemFeaturesHandler(appState)
	featureSettingsHandler := handlers.NewFeatureSettingsHandler(appState)
	usageHandler := handlers.NewUsageHandler(appState)
	billingHandler := handlers.NewBillingHandler(appState)
	plansHandler := handlers.NewPlansHandler(appState)
	healthHandler := handlers.NewHealthHandler(appState)
	dbMigrationHandler := handlers.NewDBMigrationHandler(appState)
	cpuPinningHandler := handlers.NewCPUPinningHandler(appState)
	nodeEnrollHandler := handlers.NewNodeEnrollHandler(appState)
	ticketDeletionsHandler := handlers.NewTicketDeletionsHandler(appState)
	setupHandler := handlers.NewSetupHandler(appState, authHandler)

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
	// Per-node Redis-ACL handshake. nil-safe + gated: when feature_redis_acl is
	// off (default) the gRPC auth path is byte-identical to before.
	aclProvisioner := redisacl.NewProvisioner(redisClient)
	aclHandshake := redisacl.NewHandshake(
		&aclHandshakeStore{store: pgStore, flags: appState.FeatureFlags},
		aclProvisioner,
		cfg.ClusterSecret,
		func(ctx context.Context) bool { return appState.FeatureFlags.IsRedisACLEnabled(ctx) },
	)
	// Pre-placement ACL hook: re-apply a node's Redis ACL right before sending a
	// server-placement command, closing the window where a freshly created
	// server's keys are NOPERM for the node until its next reconnect. nil-safe.
	appState.Queue.SetACL(aclHandshake)
	go func() {
		if err := nodegrpc.StartGRPCServer(cfg.GRPCPort, grpcRegistry, grpcLookup, cfg.CoreID, aclHandshake, cfg.GRPCTLSEnabled, cfg.ClusterSecret); err != nil {
			log.Fatalf("gRPC server error: %v", err)
		}
	}()

	// Core Heartbeat in Redis (so Nodes can discover this Core)
	coreHeartbeat := services.NewCoreHeartbeatService(redisClient, cfg.CoreID, cfg.Region, cfg.GRPCPort)
	coreHeartbeat.Start()

	// Backup scheduler — ticks once a minute, dispatches due jobs to nodes.
	// Wire the gRPC mesh in so retention deletes can reach node-local stores.
	// Leader-gated: tick + Pub/Sub result processing run only on
	// the elected Core to avoid double-dispatch and double-result-write.
	backupScheduler := services.NewBackupScheduler(pgStore, redisClient)
	backupScheduler.SetRegistry(grpcRegistry)
	backupScheduler.SetLeader(coreLeader)
	backupScheduler.Start(context.Background())

	// Scheduled-tasks executor — per-server cron jobs (restart, say).
	// Leader-gated, 30s tick. Publishes scheduled_tasks.changed via the SSE
	// channel after each dispatch so the panel updates last-run/next-run.
	scheduledTasksService := services.NewScheduledTaskService(pgStore, redisClient, appState.Queue, appState.Events)
	scheduledTasksService.SetLeader(coreLeader)
	scheduledTasksService.Start(context.Background())

	// Telemetry heartbeat. Posts anonymous platform stats to
	// dylaris.dev every 10min for the live counter on the website.
	// Leader-gated so multi-Core deployments don't double-count.
	telemetryHeartbeat := services.NewTelemetryHeartbeat(pgStore, cfg.CoreID, cfg.Region)
	telemetryHeartbeat.SetLeader(coreLeader)
	telemetryHeartbeat.Start(context.Background())

	// Recovery-token printer. Background loop that logs either the
	// Fresh-Install hint or the Lost-Admin token + URL every 30s as long as
	// the platform has no admin. Stops at the ctx cancel triggered by SIGTERM.
	services.StartSetupRecoveryLoop(context.Background(), pgStore, cfg.FrontendURL)

	// Set up router and API endpoints
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

	// Setup-lock middleware wraps every /api/* route. /api/setup/*
	// is short-circuited by the middleware itself so the wizard endpoints stay
	// reachable in Fresh-Install mode (when every other API route returns 503
	// setup_required). In Lost-Admin + Complete modes this is a tiny COUNT()
	// per request that we deliberately don't cache so a wizard completion on
	// one Core unlocks others on their very next request.
	api.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(appState.RequireSetupComplete(next.ServeHTTP))
	})

	// Maintenance gate: blocks writes / all traffic per block_level while
	// maintenance is active. Runs before per-route AuthMiddleware, so it
	// resolves admin status from the token itself — admins always pass and
	// can toggle maintenance back off.
	api.Use(handlers.MaintenanceMuxMiddleware(appState, authHandler.IsAdminToken))

	// --- PUBLIC SOLDER API (Technic Launcher) ---
	// Registered on the ROOT router with NO .Use(...) — it deliberately bypasses
	// the setup-lock, maintenance, and auth middleware so the launcher can reach
	// published packs at all times. The modpacks feature is gated IN-HANDLER
	// (Solder-shaped {"error":...} JSON), not by the 503 feature middleware.
	solder := r.PathPrefix("/solder").Subrouter()
	solder.HandleFunc("/api/", solderHandler.Info).Methods("GET")
	solder.HandleFunc("/api/modpack", solderHandler.ListModpacks).Methods("GET")
	solder.HandleFunc("/api/modpack/{slug}", solderHandler.GetModpack).Methods("GET")
	solder.HandleFunc("/api/modpack/{slug}/{build}", solderHandler.GetBuild).Methods("GET")
	solder.HandleFunc("/api/verify/{key}", solderHandler.VerifyKey).Methods("GET")
	solder.HandleFunc("/mirror/{rest:.*}", solderHandler.SolderMirror).Methods("GET")

	// --- PUBLIC SHARE-LINK DOWNLOAD ---
	// Sibling of the /solder block on the ROOT router: bypasses the /api
	// subrouter's setup-lock + maintenance middleware AND auth, because the
	// token is the credential (like /solder/api/verify/{key}). Per-IP rate
	// limited; the modpacks feature is gated in-handler with a uniform 404.
	shareLimiter := handlers.NewIPRateLimiter()
	r.HandleFunc("/api/share/{token}", shareLimiter.Limit(30, packsHandler.ServeShare)).Methods("GET")

	// Per-IP rate limiter for public auth endpoints — blunts brute-force and
	// credential-stuffing on login/register/reset/setup.
	authLimiter := handlers.NewIPRateLimiter()

	// --- PUBLIC ENDPOINTS ---
	api.HandleFunc("/auth/login", authLimiter.Limit(10, authHandler.LoginHandler)).Methods("POST")
	api.HandleFunc("/status", authHandler.StatusHandler).Methods("GET")
	api.HandleFunc("/system/capabilities", systemHandler.GetCapabilities).Methods("GET")
	// Public — used by the topbar to display "Connected to <region> Core".
	api.HandleFunc("/system/core-info", systemHandler.GetCoreInfo).Methods("GET")
	// SSE stream of platform-wide config-change events. Panel
	// subscribes once on boot and refreshes its caches reactively. Auth via
	// ?token= query param since EventSource can't set Authorization headers.
	api.HandleFunc("/system/events", authHandler.AuthMiddleware(systemEventsHandler.StreamEvents)).Methods("GET")
	// Mint a short-lived SSE auth ticket so EventSource streams carry a
	// disposable ?ticket= in the URL instead of the long-lived session JWT.
	api.HandleFunc("/sse-ticket", authHandler.AuthMiddleware(authHandler.MintSSETicket)).Methods("POST")

	// Setup wizard. Open routes; /api/setup/* is also exempt from
	// the setup-lock middleware so they remain reachable in Fresh-Install
	// mode. Atomic CTE in Store.CreateFirstAdmin prevents racing inserts
	// across N Cores.
	api.HandleFunc("/setup/status", setupHandler.Status).Methods("GET")
	api.HandleFunc("/setup/admin", authLimiter.Limit(10, setupHandler.CreateAdmin)).Methods("POST")

	// --- Scheduled Tasks ---
	// Cron preview — pure transform, available to anyone authed.
	api.HandleFunc("/scheduled-tasks/validate", authHandler.AuthMiddleware(scheduledTasksHandler.ValidateCron)).Methods("POST")
	// Per-server CRUD. Access gated to power-class (owner/admin/permitted).
	api.HandleFunc("/servers/{id:[0-9]+}/scheduled-tasks", authHandler.AuthMiddleware(scheduledTasksHandler.List)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/scheduled-tasks", authHandler.AuthMiddleware(scheduledTasksHandler.Create)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/scheduled-tasks/{taskId:[0-9]+}", authHandler.AuthMiddleware(scheduledTasksHandler.Update)).Methods("PATCH")
	api.HandleFunc("/servers/{id:[0-9]+}/scheduled-tasks/{taskId:[0-9]+}", authHandler.AuthMiddleware(scheduledTasksHandler.Delete)).Methods("DELETE")

	// --- RCON + API keys ---
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

	// --- Modrinth proxy + per-server mod install ---
	// Browse + project metadata are cached in Redis (5 min / 1 h). All authed.
	// Per-IP rate limit on the Modrinth proxy: each distinct query is a cache
	// miss that both hits Modrinth and grows the Redis cache, so cap volume.
	modrinthLimiter := handlers.NewIPRateLimiter()
	api.HandleFunc("/modrinth/search", modrinthLimiter.Limit(120, authHandler.AuthMiddleware(modrinthHandler.Search))).Methods("GET")
	api.HandleFunc("/modrinth/project/{slug}", modrinthLimiter.Limit(120, authHandler.AuthMiddleware(modrinthHandler.Project))).Methods("GET")
	api.HandleFunc("/modrinth/project/{slug}/versions", modrinthLimiter.Limit(120, authHandler.AuthMiddleware(modrinthHandler.ProjectVersions))).Methods("GET")
	api.HandleFunc("/modrinth/version/{id}", modrinthLimiter.Limit(120, authHandler.AuthMiddleware(modrinthHandler.Version))).Methods("GET")
	// Per-server installed mods + install/uninstall dispatch.
	api.HandleFunc("/servers/{id:[0-9]+}/mods", authHandler.AuthMiddleware(serverModsHandler.List)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/mods", authHandler.AuthMiddleware(serverModsHandler.Install)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/mods/{modId:[0-9]+}", authHandler.AuthMiddleware(serverModsHandler.Uninstall)).Methods("DELETE")

	// --- Spark profiles ---
	api.HandleFunc("/servers/{id:[0-9]+}/spark/profiles", authHandler.AuthMiddleware(sparkHandler.List)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/spark/profiles", authHandler.AuthMiddleware(sparkHandler.Record)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/spark/profiles/{profileId:[0-9]+}", authHandler.AuthMiddleware(sparkHandler.Delete)).Methods("DELETE")

	// --- Custom Tabs ---
	api.HandleFunc("/servers/{id:[0-9]+}/tabs", authHandler.AuthMiddleware(serverTabsHandler.List)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/tabs", authHandler.AuthMiddleware(serverTabsHandler.Create)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/tabs/{tabId:[0-9]+}", authHandler.AuthMiddleware(serverTabsHandler.Update)).Methods("PATCH")
	api.HandleFunc("/servers/{id:[0-9]+}/tabs/{tabId:[0-9]+}", authHandler.AuthMiddleware(serverTabsHandler.Delete)).Methods("DELETE")

	// --- Modrinth PAT ---
	api.HandleFunc("/me/modrinth-pat", authHandler.AuthMiddleware(appState.AllowReadOnlyWhenDisabled(modrinthPATHandler.Status))).Methods("GET")
	api.HandleFunc("/me/modrinth-pat", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(modrinthPATHandler.Set)))).Methods("PUT")
	api.HandleFunc("/me/modrinth-pat", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(modrinthPATHandler.Clear)))).Methods("DELETE")
	// --- Unified pack builder (Solder + Modrinth). Reuses the modpacks feature gates. ---
	api.HandleFunc("/me/packs", authHandler.AuthMiddleware(appState.AllowReadOnlyWhenDisabled(packsHandler.List))).Methods("GET")
	api.HandleFunc("/me/packs", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.Create)))).Methods("POST")
	api.HandleFunc("/me/packs/import-solder/preview", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.ImportSolderPreview)))).Methods("POST")
	api.HandleFunc("/me/packs/import-solder", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.ImportSolder)))).Methods("POST")
	api.HandleFunc("/packs/{id:[0-9]+}", authHandler.AuthMiddleware(appState.AllowReadOnlyWhenDisabled(packsHandler.Get))).Methods("GET")
	api.HandleFunc("/packs/{id:[0-9]+}", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.Update)))).Methods("PATCH")
	api.HandleFunc("/packs/{id:[0-9]+}", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.Delete)))).Methods("DELETE")
	api.HandleFunc("/packs/{id:[0-9]+}/builds", authHandler.AuthMiddleware(appState.AllowReadOnlyWhenDisabled(packsHandler.ListBuilds))).Methods("GET")
	api.HandleFunc("/packs/{id:[0-9]+}/builds", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.CreateBuild)))).Methods("POST")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.UpdateBuild)))).Methods("PATCH")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.DeleteBuild)))).Methods("DELETE")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/content", authHandler.AuthMiddleware(appState.AllowReadOnlyWhenDisabled(packsHandler.ListContent))).Methods("GET")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/content/modrinth", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.AddModrinth)))).Methods("POST")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/content/upload", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.UploadContent)))).Methods("POST")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/content/{modversionId:[0-9]+}", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.RemoveContent)))).Methods("DELETE")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/content/{modversionId:[0-9]+}/side", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.SetSide)))).Methods("PATCH")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/content/{modversionId:[0-9]+}/text", authHandler.AuthMiddleware(appState.AllowReadOnlyWhenDisabled(packsHandler.GetContentText))).Methods("GET")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/content/{modversionId:[0-9]+}/text", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.SetContentText)))).Methods("PUT")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/publish", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.PublishModrinth)))).Methods("POST")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/export", authHandler.AuthMiddleware(appState.AllowReadOnlyWhenDisabled(packsHandler.ExportMrpack))).Methods("GET")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/loader", authHandler.AuthMiddleware(appState.AllowReadOnlyWhenDisabled(packsHandler.GetBuildLoader))).Methods("GET")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/content/{modversionId:[0-9]+}/replace-modrinth", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.ReplaceWithModrinth)))).Methods("POST")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/update-mods", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.UpdateMods)))).Methods("POST")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/publish-solder", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.PublishSolder)))).Methods("POST")
	api.HandleFunc("/packs/{id:[0-9]+}/solder-config", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.SetSolderConfig)))).Methods("PATCH")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/share-link", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireShareLinksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.CreateShareLink))))).Methods("POST")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/share-links", authHandler.AuthMiddleware(appState.AllowReadOnlyWhenDisabled(packsHandler.ListShareLinks))).Methods("GET")
	api.HandleFunc("/packs/{id:[0-9]+}/builds/{buildId:[0-9]+}/share-links/{linkId:[0-9]+}", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(packsHandler.RevokeShareLink)))).Methods("DELETE")

	// --- Solder client/key management (authed) ---
	api.HandleFunc("/solder/clients", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(solderHandler.ListClients)))).Methods("GET")
	api.HandleFunc("/solder/clients", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(solderHandler.CreateClient)))).Methods("POST")
	api.HandleFunc("/solder/clients/{id:[0-9]+}", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(solderHandler.DeleteClient)))).Methods("DELETE")

	api.HandleFunc("/packs/{id:[0-9]+}/clients", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(solderHandler.ListPackClientsHandler)))).Methods("GET")
	api.HandleFunc("/packs/{id:[0-9]+}/clients/{clientId:[0-9]+}", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(solderHandler.AddPackClient)))).Methods("POST")
	api.HandleFunc("/packs/{id:[0-9]+}/clients/{clientId:[0-9]+}", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(solderHandler.RemovePackClient)))).Methods("DELETE")

	api.HandleFunc("/solder/keys", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(solderHandler.ListKeys)))).Methods("GET")
	api.HandleFunc("/solder/keys", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(solderHandler.CreateKey)))).Methods("POST")
	api.HandleFunc("/solder/keys/{id:[0-9]+}", authHandler.AuthMiddleware(appState.RequireModpacksEnabled(appState.RequireUserCanCreateModpacks(solderHandler.DeleteKey)))).Methods("DELETE")

	// --- Username history + account policy ---
	api.HandleFunc("/me/usage", authHandler.AuthMiddleware(usageHandler.GetMyUsage)).Methods("GET")
	api.HandleFunc("/admin/usage", authHandler.AuthMiddleware(usageHandler.GetAllUsage)).Methods("GET")

	api.HandleFunc("/me/billing", authHandler.AuthMiddleware(billingHandler.GetMyBilling)).Methods("GET")
	api.HandleFunc("/admin/settings/billing", authHandler.AuthMiddleware(billingHandler.GetBillingSettings)).Methods("GET")
	api.HandleFunc("/admin/settings/billing", authHandler.AuthMiddleware(billingHandler.SetBillingSettings)).Methods("PUT")
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/billing", authHandler.AuthMiddleware(billingHandler.GetUserBilling)).Methods("GET")
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/billing", authHandler.AuthMiddleware(billingHandler.SetBillingStatus)).Methods("PATCH")
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/billing-overrides", authHandler.AuthMiddleware(billingHandler.SetBillingOverrides)).Methods("PATCH")

	// --- BYON plans + per-user plan/limit overrides (admin) ---
	api.HandleFunc("/admin/plans", authHandler.AuthMiddleware(plansHandler.List)).Methods("GET")
	api.HandleFunc("/admin/plans", authHandler.AuthMiddleware(plansHandler.Create)).Methods("POST")
	api.HandleFunc("/admin/plans/{id:[0-9]+}", authHandler.AuthMiddleware(plansHandler.Update)).Methods("PUT")
	api.HandleFunc("/admin/plans/{id:[0-9]+}", authHandler.AuthMiddleware(plansHandler.Delete)).Methods("DELETE")
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/plan", authHandler.AuthMiddleware(plansHandler.SetUserPlan)).Methods("PATCH")
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/limit-overrides", authHandler.AuthMiddleware(plansHandler.SetUserLimitOverrides)).Methods("PATCH")

	api.HandleFunc("/me/username-history", authHandler.AuthMiddleware(usernameHistoryHandler.Me)).Methods("GET")
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/username-history", authHandler.AuthMiddleware(usernameHistoryHandler.Admin)).Methods("GET")
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/username", authHandler.AuthMiddleware(usernameHistoryHandler.AdminRename)).Methods("PATCH")
	api.HandleFunc("/admin/settings/users", authHandler.AuthMiddleware(accountPolicyHandler.Get)).Methods("GET")
	api.HandleFunc("/admin/settings/users", authHandler.AuthMiddleware(accountPolicyHandler.Set)).Methods("PUT")
	// --- Modpack settings + system feature flags ---
	api.HandleFunc("/admin/settings/modpacks", authHandler.AuthMiddleware(modpackSettingsHandler.Get)).Methods("GET")
	api.HandleFunc("/admin/settings/modpacks", authHandler.AuthMiddleware(modpackSettingsHandler.Set)).Methods("PUT")
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/modpack-flag", authHandler.AuthMiddleware(modpackSettingsHandler.SetUserFlag)).Methods("PATCH")
	api.HandleFunc("/system/features", authHandler.AuthMiddleware(systemFeaturesHandler.Get)).Methods("GET")
	// Bundled admin GET/PUT for all platform-wide feature toggles. Replaces
	// the per-feature toggle that used to live inside /admin/settings/modpacks
	// (still works for back-compat; this is the new canonical surface).
	api.HandleFunc("/admin/settings/features", authHandler.AuthMiddleware(featureSettingsHandler.Get)).Methods("GET")
	api.HandleFunc("/admin/settings/features", authHandler.AuthMiddleware(featureSettingsHandler.Set)).Methods("PUT")
	// --- Platform status / health (admin Status page) ---
	api.HandleFunc("/admin/health", authHandler.AuthMiddleware(healthHandler.GetStatus)).Methods("GET")

	// In-panel cross-database migration (admin-only). Shared job is pollable by
	// every admin; the copy runs under maintenance mode on whichever Core started it.
	api.HandleFunc("/admin/db/migration", authHandler.AuthMiddleware(dbMigrationHandler.GetMigration)).Methods("GET")
	api.HandleFunc("/admin/db/migration", authHandler.AuthMiddleware(dbMigrationHandler.StartMigration)).Methods("POST")
	api.HandleFunc("/admin/db/migration/test-connection", authHandler.AuthMiddleware(dbMigrationHandler.TestConnection)).Methods("POST")
	api.HandleFunc("/admin/db/migration/verify", authHandler.AuthMiddleware(dbMigrationHandler.VerifyMigration)).Methods("POST")
	// In-place hypertable upgrade + TimescaleDB recommendation (same database).
	api.HandleFunc("/admin/db/hypertable", authHandler.AuthMiddleware(dbMigrationHandler.HypertableStatus)).Methods("GET")
	api.HandleFunc("/admin/db/hypertable/convert", authHandler.AuthMiddleware(dbMigrationHandler.ConvertHypertable)).Methods("POST")
	// --- Telemetry settings ---
	api.HandleFunc("/admin/settings/telemetry", authHandler.AuthMiddleware(telemetrySettingsHandler.Get)).Methods("GET")
	api.HandleFunc("/admin/settings/telemetry", authHandler.AuthMiddleware(telemetrySettingsHandler.Set)).Methods("PUT")

	// Warp enrollment (warp API-key auth, NOT user session)
	api.HandleFunc("/warp/enroll", warpHandler.WarpAPIKeyMiddleware(warpHandler.Enroll)).Methods("POST")
	// Warp admin registry: regions + leaders (user session; admin enforced inside handler)
	api.HandleFunc("/warp/regions", authHandler.AuthMiddleware(warpHandler.ListRegions)).Methods("GET")
	api.HandleFunc("/warp/regions", authHandler.AuthMiddleware(warpHandler.UpsertRegion)).Methods("POST")
	api.HandleFunc("/warp/regions/{region}", authHandler.AuthMiddleware(warpHandler.DeleteRegion)).Methods("DELETE")
	api.HandleFunc("/warp/leaders", authHandler.AuthMiddleware(warpHandler.UpsertLeader)).Methods("POST")
	api.HandleFunc("/warp/leaders/{leaderId}", authHandler.AuthMiddleware(warpHandler.DeleteLeader)).Methods("DELETE")
	api.HandleFunc("/admin/warp/keys", authHandler.AuthMiddleware(warpHandler.MintAPIKey)).Methods("POST")
	// Route-only link kits (tenant self-service; BYON-gated inside the handler)
	api.HandleFunc("/warp/link-kits", authHandler.AuthMiddleware(warpHandler.ListLinkKits)).Methods("GET")
	api.HandleFunc("/warp/link-kits", authHandler.AuthMiddleware(warpHandler.MintLinkKit)).Methods("POST")

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

	// --- Roles + capability flags ---
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/role", authHandler.AuthMiddleware(userHandler.SetUserRoleHandler)).Methods("PUT")
	api.HandleFunc("/admin/users/{id:[0-9a-f-]{36}}/permissions", authHandler.AuthMiddleware(userHandler.SetUserPermissionsHandler)).Methods("PUT")

	// --- Maintenance mode ---
	// Public state — drives the banner; never blocked by the maintenance middleware.
	api.HandleFunc("/maintenance", maintenanceHandler.GetState).Methods("GET")
	api.HandleFunc("/admin/maintenance", authHandler.AuthMiddleware(maintenanceHandler.SaveState)).Methods("PUT")

	// --- Tickets ---
	// Every ticket-related endpoint is wrapped in RequireTicketsEnabled so the
	// platform-wide tickets toggle flips the entire subsystem (UI + API +
	// notifications fan-out) without a per-handler check.
	// Categories: public list (enabled only) for create form, admin CRUD for management.
	api.HandleFunc("/ticket-categories", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketCategoriesHandler.ListCategories))).Methods("GET")
	api.HandleFunc("/admin/ticket-categories", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketCategoriesHandler.AdminListCategories))).Methods("GET")
	api.HandleFunc("/admin/ticket-categories", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketCategoriesHandler.CreateCategory))).Methods("POST")
	api.HandleFunc("/admin/ticket-categories/{id:[0-9]+}", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketCategoriesHandler.UpdateCategory))).Methods("PATCH")
	api.HandleFunc("/admin/ticket-categories/{id:[0-9]+}", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketCategoriesHandler.DeleteCategory))).Methods("DELETE")

	// Tickets: user CRUD + support inbox.
	api.HandleFunc("/tickets", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketsHandler.ListMyTickets))).Methods("GET")
	api.HandleFunc("/tickets", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketsHandler.CreateTicket))).Methods("POST")
	api.HandleFunc("/tickets/inbox", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketsHandler.ListInboxTickets))).Methods("GET")
	api.HandleFunc("/tickets/{id:[0-9]+}", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketsHandler.GetTicket))).Methods("GET")
	api.HandleFunc("/tickets/{id:[0-9]+}", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketDeletionsHandler.DeleteTicket))).Methods("DELETE")
	api.HandleFunc("/tickets/{id:[0-9]+}/messages", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketsHandler.AddReply))).Methods("POST")
	api.HandleFunc("/tickets/{id:[0-9]+}/status", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketsHandler.UpdateStatus))).Methods("PATCH")
	api.HandleFunc("/tickets/{id:[0-9]+}/priority", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketsHandler.UpdatePriority))).Methods("PATCH")
	api.HandleFunc("/tickets/{id:[0-9]+}/assignment", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketsHandler.UpdateAssignment))).Methods("PATCH")
	api.HandleFunc("/tickets/{id:[0-9]+}/watchers", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketsHandler.AddWatcher))).Methods("POST")
	api.HandleFunc("/tickets/{id:[0-9]+}/watchers/{userId:[0-9a-f-]{36}}", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketsHandler.RemoveWatcher))).Methods("DELETE")

	// Sidebar source for support's "Via tickets" tab.
	api.HandleFunc("/me/servers/via-tickets", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketsHandler.ListMyServersViaTickets))).Methods("GET")

	// Settings.
	api.HandleFunc("/admin/settings/tickets", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketSettingsHandler.GetSettings))).Methods("GET")
	api.HandleFunc("/admin/settings/tickets", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketSettingsHandler.SaveSettings))).Methods("PUT")

	// --- Tickets: attachments, canned responses, notifications ---
	api.HandleFunc("/tickets/{id:[0-9]+}/attachments", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketAttachmentsHandler.UploadAttachment))).Methods("POST")
	api.HandleFunc("/tickets/{id:[0-9]+}/attachments", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketAttachmentsHandler.ListAttachments))).Methods("GET")
	api.HandleFunc("/tickets/{id:[0-9]+}/attachments/{aid:[0-9]+}/download", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketAttachmentsHandler.DownloadAttachment))).Methods("GET")
	api.HandleFunc("/tickets/{id:[0-9]+}/attachments/{aid:[0-9]+}", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketAttachmentsHandler.DeleteAttachment))).Methods("DELETE")

	// Canned responses: support sees the read list, admin manages.
	api.HandleFunc("/ticket-canned-responses", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(cannedResponsesHandler.ListForSupport))).Methods("GET")
	api.HandleFunc("/admin/ticket-canned-responses", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(cannedResponsesHandler.AdminList))).Methods("GET")
	api.HandleFunc("/admin/ticket-canned-responses", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(cannedResponsesHandler.Create))).Methods("POST")
	api.HandleFunc("/admin/ticket-canned-responses/{id:[0-9]+}", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(cannedResponsesHandler.Update))).Methods("PATCH")
	api.HandleFunc("/admin/ticket-canned-responses/{id:[0-9]+}", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(cannedResponsesHandler.Delete))).Methods("DELETE")

	// Notifications: in-app inbox. Currently ticket-driven; gated with the
	// rest of the ticket subsystem.
	api.HandleFunc("/notifications", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(notificationsHandler.List))).Methods("GET")
	api.HandleFunc("/notifications/unread-count", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(notificationsHandler.UnreadCount))).Methods("GET")
	api.HandleFunc("/notifications/{id:[0-9]+}/read", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(notificationsHandler.MarkRead))).Methods("POST")
	api.HandleFunc("/notifications/read-all", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(notificationsHandler.MarkAllRead))).Methods("POST")

	// --- Server audit ---
	// Owner + admin can view. Force-on flag is admin-only.
	api.HandleFunc("/servers/{id:[0-9]+}/audit", authHandler.AuthMiddleware(serverAuditHandler.ListAudit)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/audit/status", authHandler.AuthMiddleware(serverAuditHandler.GetStatus)).Methods("GET")
	api.HandleFunc("/servers/{id:[0-9]+}/audit/force", authHandler.AuthMiddleware(serverAuditHandler.SetForce)).Methods("PUT")
	// Platform-wide audit retention policy.
	api.HandleFunc("/admin/settings/audit", authHandler.AuthMiddleware(auditSettingsHandler.GetPolicy)).Methods("GET")
	api.HandleFunc("/admin/settings/audit", authHandler.AuthMiddleware(auditSettingsHandler.SavePolicy)).Methods("PUT")

	// --- Ticket DB migration + backups (admin-only) ---
	api.HandleFunc("/admin/tickets/migration/status", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketMigrationHandler.GetStatus))).Methods("GET")
	api.HandleFunc("/admin/tickets/migration/test-connection", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketMigrationHandler.TestExternalConnection))).Methods("POST")
	api.HandleFunc("/admin/tickets/migration/dry-run", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketMigrationHandler.DryRunMigration))).Methods("POST")
	api.HandleFunc("/admin/tickets/migration/execute", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketMigrationHandler.ExecuteMigration))).Methods("POST")
	// Backups: create, list, download, delete.
	api.HandleFunc("/admin/tickets/backup", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketMigrationHandler.CreateBackup))).Methods("POST")
	api.HandleFunc("/admin/tickets/backups", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketMigrationHandler.ListBackups))).Methods("GET")
	api.HandleFunc("/admin/tickets/backups/{name}/download", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketMigrationHandler.DownloadBackup))).Methods("GET")
	api.HandleFunc("/admin/tickets/backups/{name}", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketMigrationHandler.DeleteBackup))).Methods("DELETE")
	// Restore: two-step Danger Zone (init + execute) — 2FA + 15s timer + typed phrase.
	api.HandleFunc("/admin/tickets/restore/init", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketMigrationHandler.InitRestore))).Methods("POST")
	api.HandleFunc("/admin/tickets/restore/execute", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketMigrationHandler.ExecuteRestore))).Methods("POST")
	// Deletion audit log (admin-only).
	api.HandleFunc("/admin/tickets/deletion-log", authHandler.AuthMiddleware(appState.RequireTicketsEnabled(ticketDeletionsHandler.ListDeletions))).Methods("GET")
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
	// Adopt an auto-discovered node: admin sets name/region/tags (DB precedence).
	api.HandleFunc("/nodes/{id:[0-9]+}/config", authHandler.AuthMiddleware(nodeHandler.ConfigureNode)).Methods("PATCH")
	api.HandleFunc("/nodes/{id:[0-9]+}", authHandler.AuthMiddleware(nodeHandler.DeleteNode)).Methods("DELETE")
	api.HandleFunc("/nodes/{id:[0-9]+}/servers", authHandler.AuthMiddleware(nodeHandler.GetNodeServers)).Methods("GET")
	api.HandleFunc("/nodes/{id:[0-9]+}/force", authHandler.AuthMiddleware(nodeHandler.ForceDeleteNode)).Methods("DELETE")
	api.HandleFunc("/nodes/{id:[0-9]+}/storage", authHandler.AuthMiddleware(nodeHandler.GetNodeStorage)).Methods("GET")
	api.HandleFunc("/nodes/{id:[0-9]+}/deploy-bundle", authHandler.AuthMiddleware(nodeHandler.GetDeployBundle)).Methods("GET")
	api.HandleFunc("/nodes/{id:[0-9]+}/cpu", authHandler.AuthMiddleware(cpuPinningHandler.GetNodeCPU)).Methods("GET")
	// BYON per-user node enrollment tokens (feature-gated inside the handlers).
	api.HandleFunc("/nodes/enroll-token", authHandler.AuthMiddleware(nodeEnrollHandler.MintToken)).Methods("POST")
	api.HandleFunc("/nodes/enroll-token", authHandler.AuthMiddleware(nodeEnrollHandler.ListTokens)).Methods("GET")
	api.HandleFunc("/nodes/enroll-token/{id}", authHandler.AuthMiddleware(nodeEnrollHandler.RevokeToken)).Methods("DELETE")

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
	dnsHandler := handlers.NewDNSHandler(appState)
	infrastructureHandler := handlers.NewInfrastructureHandler(appState)

	// Admin endpoints
	api.HandleFunc("/gateway/links", authHandler.AuthMiddleware(gatewayHandler.GetLinks)).Methods("GET")
	api.HandleFunc("/gateway/edges", authHandler.AuthMiddleware(gatewayHandler.GetEdges)).Methods("GET")
	api.HandleFunc("/gateway/routes", authHandler.AuthMiddleware(gatewayHandler.GetAllRoutes)).Methods("GET")
	api.HandleFunc("/gateway/dns-check", authHandler.AuthMiddleware(dnsHandler.CheckDNS)).Methods("GET")
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
	api.HandleFunc("/servers/{id:[0-9]+}/routes", authHandler.AuthMiddleware(appState.RequireGatewayEnabled(gatewayHandler.CreateServerRoute))).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/routes/{domain:.+}", authHandler.AuthMiddleware(gatewayHandler.DeleteServerRoute)).Methods("DELETE")

	// Route-only (external origin): a protected address pointed at a server the
	// user already runs, no managed node. Owner-scoped; create is gateway-gated.
	api.HandleFunc("/gateway/link-routes", authHandler.AuthMiddleware(gatewayHandler.ListLinkRoutes)).Methods("GET")
	api.HandleFunc("/gateway/link-routes", authHandler.AuthMiddleware(appState.RequireGatewayEnabled(gatewayHandler.CreateLinkRoute))).Methods("POST")
	api.HandleFunc("/gateway/link-routes/{domain:.+}", authHandler.AuthMiddleware(gatewayHandler.DeleteLinkRoute)).Methods("DELETE")

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

	// Library endpoints
	api.HandleFunc("/library", authHandler.AuthMiddleware(libraryHandler.GetLibraryHandler)).Methods("GET")
	api.HandleFunc("/library/delete", authHandler.AuthMiddleware(libraryHandler.DeleteLibraryHandler)).Methods("POST")
	api.HandleFunc("/library/mkdir", authHandler.AuthMiddleware(libraryHandler.MkdirLibraryHandler)).Methods("POST")
	api.HandleFunc("/library/upload", authHandler.AuthMiddleware(libraryHandler.UploadLibraryHandler)).Methods("POST")
	api.HandleFunc("/library/download", authHandler.AuthMiddleware(libraryHandler.DownloadLibraryHandler)).Methods("GET")
	api.HandleFunc("/library/toggle", authHandler.AuthMiddleware(libraryHandler.ToggleLibraryPathHandler)).Methods("POST")

	// Settings endpoints
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

	// Server auto-move toggle — gated on the feature flag AND active gateway.
	api.HandleFunc("/servers/{id:[0-9]+}/automove", authHandler.AuthMiddleware(appState.RequireAutoMoveEnabled(serverHandler.SetServerAutoMove))).Methods("PATCH")
	// Manual node-to-node move (admin) — gateway-only; enqueues onto the orchestrator.
	api.HandleFunc("/admin/servers/{id:[0-9]+}/move", authHandler.AuthMiddleware(appState.RequireGatewayEnabled(serverHandler.MoveServer))).Methods("POST")
	// Tenant-facing transfer (BYON) — gateway-only; owner-or-admin + placement authz inside.
	api.HandleFunc("/servers/{id:[0-9]+}/transfer", authHandler.AuthMiddleware(appState.RequireGatewayEnabled(serverHandler.TransferServer))).Methods("POST")
	// Demo flag (admin) — mark a normal server as a public read-only showcase.
	api.HandleFunc("/admin/servers/{id:[0-9]+}/demo", authHandler.AuthMiddleware(serverHandler.SetServerDemo)).Methods("PATCH")
	// Demo account designation (admin) — the single read-only account that sees the demo servers.
	api.HandleFunc("/admin/settings/demo-account", authHandler.AuthMiddleware(serverHandler.GetDemoAccount)).Methods("GET")
	api.HandleFunc("/admin/settings/demo-account", authHandler.AuthMiddleware(serverHandler.SetDemoAccount)).Methods("PUT")

	// Store-linking (dylaris.com). Gated by StoreEnabled inside each handler.
	// link/start + status are panel-user authed; link/verify + verify-user are
	// service-to-service (shared key in X-Store-Key, no panel session).
	storeHandler := handlers.NewStoreHandler(appState)
	api.HandleFunc("/store/link/start", authHandler.AuthMiddleware(storeHandler.LinkStart)).Methods("POST")
	api.HandleFunc("/store/status", authHandler.AuthMiddleware(storeHandler.Status)).Methods("GET")
	api.HandleFunc("/store/link/verify", authLimiter.Limit(20, storeHandler.LinkVerify)).Methods("POST")
	api.HandleFunc("/store/verify-user", authLimiter.Limit(60, storeHandler.VerifyUser)).Methods("GET")
	api.HandleFunc("/store/usage", authLimiter.Limit(60, storeHandler.GetUsage)).Methods("GET")
	api.HandleFunc("/store/provision", authLimiter.Limit(60, storeHandler.Provision)).Methods("POST")
	// Migration progress poll — owner-or-admin, ungated (reads are harmless).
	api.HandleFunc("/servers/{id:[0-9]+}/migration-status", authHandler.AuthMiddleware(serverHandler.GetMigrationStatus)).Methods("GET")
	api.HandleFunc("/gateway/route-options", authHandler.AuthMiddleware(settingsHandler.GetGatewayRouteOptions)).Methods("GET")
	api.HandleFunc("/settings/servers", authHandler.AuthMiddleware(settingsHandler.GetServerSettings)).Methods("GET")
	api.HandleFunc("/settings/servers", authHandler.AuthMiddleware(settingsHandler.SaveServerSettings)).Methods("POST")
	api.HandleFunc("/settings/beam", authHandler.AuthMiddleware(settingsHandler.GetBeamSettings)).Methods("GET")
	api.HandleFunc("/settings/beam", authHandler.AuthMiddleware(settingsHandler.SaveBeamSettings)).Methods("POST")
	api.HandleFunc("/settings/routing-mode", authHandler.AuthMiddleware(settingsHandler.GetRoutingMode)).Methods("GET")
	api.HandleFunc("/settings/routing-mode", authHandler.AuthMiddleware(settingsHandler.SaveRoutingMode)).Methods("POST")
	api.HandleFunc("/settings/backup", authHandler.AuthMiddleware(settingsHandler.GetBackupConfig)).Methods("GET")
	api.HandleFunc("/settings/backup", authHandler.AuthMiddleware(settingsHandler.SaveBackupConfig)).Methods("POST")

	// --- Regions ---
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

	// --- Registration + Email Verify ---
	// Public — login page polls registration-status to decide whether to show the register link.
	api.HandleFunc("/auth/registration-status", registrationHandler.RegistrationStatus).Methods("GET")
	api.HandleFunc("/auth/register", authLimiter.Limit(5, registrationHandler.Register)).Methods("POST")
	// Public one-click read-only demo session (rate-limited). 404 when no demo account is set.
	api.HandleFunc("/auth/demo-login", authLimiter.Limit(10, authHandler.DemoLogin)).Methods("POST")
	api.HandleFunc("/auth/verify-email", registrationHandler.VerifyEmail).Methods("POST")
	api.HandleFunc("/auth/resend-verification", registrationHandler.ResendVerification).Methods("POST")

	// --- Password reset — all public, all enumeration-safe ---
	api.HandleFunc("/auth/forgot-password", authLimiter.Limit(5, passwordResetHandler.ForgotPassword)).Methods("POST")
	api.HandleFunc("/auth/validate-reset-token", passwordResetHandler.ValidateResetToken).Methods("POST")
	api.HandleFunc("/auth/reset-password", authLimiter.Limit(10, passwordResetHandler.ResetPassword)).Methods("POST")

	// --- Security questions ---
	// Public: pool query used by /register and /reset-password to render pickers.
	api.HandleFunc("/auth/security-questions/pool", securityQuestionsHandler.GetPool).Methods("GET")
	// Authenticated: user manages their own questions in profile.
	api.HandleFunc("/me/security-questions", authHandler.AuthMiddleware(securityQuestionsHandler.GetMyQuestions)).Methods("GET")
	api.HandleFunc("/me/security-questions", authHandler.AuthMiddleware(securityQuestionsHandler.SetMyQuestions)).Methods("PUT")
	// Admin: pool management.
	api.HandleFunc("/admin/settings/security-questions-pool", authHandler.AuthMiddleware(securityQuestionsHandler.GetAdminPool)).Methods("GET")
	api.HandleFunc("/admin/settings/security-questions-pool", authHandler.AuthMiddleware(securityQuestionsHandler.SetAdminPool)).Methods("PUT")

	// --- Auth policy + SMTP config (admin) ---
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
	// ReadHeaderTimeout defends against Slowloris (slow header drip) without
	// capping body read/write time — Core serves SSE streams and multi-GB
	// uploads/downloads, so ReadTimeout/WriteTimeout are deliberately left
	// unset. IdleTimeout reaps idle keep-alive connections.
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           corsObj(r),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Core API crashed: %v", err)
	}
}
