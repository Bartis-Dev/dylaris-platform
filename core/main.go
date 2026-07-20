package main

// ci: trigger full pipeline run (no-op, safe to remove)

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"dylaris-core/authz"
	"dylaris-core/config"
	"dylaris-core/database"
	nodegrpc "dylaris-core/grpc"
	"dylaris-core/handlers"
	"dylaris-core/models"
	"dylaris-core/pkg/leader"
	"dylaris-core/services"
	"dylaris-core/services/redisacl"
	"dylaris-core/storage"
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
	ownerID, _, ok, err := a.store.ConsumeNodeEnrollToken(plaintext)
	return ownerID, ok, err
}

func (a *aclHandshakeStore) NodeIDByToken(token string) (int, bool, error) {
	n, err := a.store.GetNodeByToken(token)
	if err != nil {
		return 0, false, nil // unknown token: not found, not an error for this path
	}
	return n.ID, true, nil
}

// NodeLimitReached enforces the BYON node-adoption cap on the gRPC enroll
// path: only enforced when BYON is on; a 0 / missing cap means unlimited;
// fail-open on store errors.
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
		Authz:               authz.NewResolver(pgStore),
		DBType:              cfg.DBType,
		StoreEnabled:        cfg.StoreEnabled,
		StoreURL:            cfg.StoreURL,
		StoreSharedKey:      cfg.StoreSharedKey,
		TabProxyIsolationActive: cfg.TabProxyIsolationActive,
		UpdatesFeedURLPlatform:  cfg.UpdatesFeedURLPlatform,
		UpdatesFeedURLGateway:   cfg.UpdatesFeedURLGateway,
	}

	// Demo showcase read access flows through the resolver so the RequireCap
	// chokepoint covers console/stats/overview reads on demo servers.
	appState.Authz.SetDemoRead(appState.IsDemoServerID)

	// Precompute the cluster-wide gRPC-TLS fingerprint once so handlers can hand it
	// to BYON operators without re-deriving. Non-secret; safe to expose.
	if fp, ferr := beamauth.ClusterGRPCCertFingerprint(cfg.ClusterSecret); ferr == nil {
		appState.GRPCTLSFingerprint = fp
	}
	appState.GRPCTLSEnabled = cfg.GRPCTLSEnabled
	if !cfg.GRPCTLSEnabled {
		log.Println("WARNING: GRPC_TLS_ENABLED is false; node<->Core gRPC (control channel, carries per-node secrets) is UNENCRYPTED. Rely on an encrypted overlay (WireGuard/VPN) between node and Core, or set GRPC_TLS_ENABLED=true.")
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

	// In-panel blob storage migration (library, ticket-attachments,
	// ticket-backups, modpacks, server-backups rows). The resolver owns the
	// Core file storage config, the modpack settings and the backup_storages
	// rows, so it is wired from appState after appState.Store etc. are set.
	appState.StorageMigration = services.NewStorageMigrationService(
		redisClient, pgStore, handlers.NewStorageDataSetResolver(appState),
	)

	// Per-server CPU pinning: reads node topology from Redis + computes auto cpusets.
	appState.CPUPinning = services.NewCPUPinningService(redisClient, pgStore)

	// One cancellable context for every long-lived background loop below, so
	// shutdown can tell them to stop instead of relying on process exit. It is
	// cancelled after both HTTP listeners have drained, so a request that is
	// still in flight never loses a service it depends on mid-response.
	//
	// Deliberately NOT used for one-shot boot calls (a single Redis Set, the
	// host-path warning): those complete during boot, where this context is
	// never cancelled, so passing it would only suggest a lifecycle they do
	// not have.
	bgCtx, bgCancel := context.WithCancel(context.Background())

	// The two core-storage connection mechanisms. Exactly one of them is live
	// at a time, decided by which backend the config names.
	//
	// Both are constructed BEFORE SyncStorageGate, which points the watchdog at
	// the configured path and drops any stale s3 state. It touches both, so
	// creating either one after it would leave that half silently unsynced.
	appState.StorageGate = storage.NewGate()
	appState.StorageS3 = storage.NewS3Resilience()
	appState.SyncStorageGate()

	// The s3 recovery probe. It idles unless the backend is actually
	// reconnecting, and it is what lets a Core whose only storage traffic is
	// uploads notice that the object store came back instead of waiting out
	// the full retry budget.
	appState.StorageS3.StartProbe(bgCtx, appState.ProbeS3Connection)

	// System-events publisher. Mutating handlers (regions,
	// modules, features, maintenance, servers CRUD) drop events into a
	// single Redis Pub/Sub channel; panels subscribe via SSE so they refresh
	// without polling. Construction is cheap — wired before any handler so
	// every code path can call h.state.Events.Publish unconditionally.
	appState.Events = services.NewSystemEventsPublisher(redisClient)

	// Storage connection state onto the system-events channel. Wired AFTER the
	// publisher above, because it captures it. Start before Attach so the
	// forwarder is already draining when the first transition can fire.
	appState.StorageStatus = services.NewStorageStatus(appState.Events, appState.StorageGate, appState.StorageS3)
	appState.StorageStatus.Start(bgCtx)
	appState.StorageStatus.Attach()

	// Leader election: a single Redis lease named for the
	// "core-leader" role, identified by this instance's CoreID. Every
	// scheduled background loop consults the leader's IsLeader() to
	// decide whether to perform its work or idle. Single-instance Core
	// always wins the election so behavior is unchanged for dev. Multi-
	// instance Core safely converges on exactly one active leader.
	coreLeader := leader.New(redisClient, "dylaris:core:leader", cfg.CoreID)
	coreLeader.Start(bgCtx)

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

	// Traffic aggregator — leader-gated + BYON-gated. Turns the per-server byte
	// counters edges + relays publish to Redis into per-tenant monthly rows in
	// traffic_usage. No-op in solo/hoster mode (feature_byon_enabled off).
	trafficAggregator := services.NewTrafficAggregator(pgStore, redisClient, appState.FeatureFlags)
	trafficAggregator.SetLeader(coreLeader)
	trafficAggregator.Start(bgCtx)

	// Billing lifecycle — leader-gated. Progresses past_due tenants whose grace
	// window has elapsed into suspended (hard cutoff deferred to SuspendGrace
	// later, keeps data). Payment-provider-agnostic; handlers/webhooks call
	// EnterPastDue/Reactivate/Suspend.
	appState.Billing = services.NewBillingLifecycleService(pgStore, appState.Queue, grpcRegistry, cfg.FrontendURL, cfg.SuspendGrace)
	appState.Billing.SetLeader(coreLeader)
	// Start() is deferred until after SetLinkACL below, so the ticker can never run
	// a suspend before the link teardown dependencies are wired.

	// DNS reconciler — leader-gated. Points each region's edge wildcard A record
	// at the live edge IPs via the DNS provider. Off unless DNS_UPDATER_ENABLED
	// and provider credentials are set; credentials live only here, never on edges.
	if cfg.DNSUpdaterEnabled {
		if provider := services.NewCloudflareProvider(cfg.CFAPIToken, cfg.CFZoneID); provider != nil {
			dnsReconciler := services.NewDNSReconciler(redisClient, provider)
			dnsReconciler.SetLeader(coreLeader)
			dnsReconciler.Start(bgCtx)
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
	autoDelete.Start(bgCtx)

	// Ticket auto-close — daily ticker, leader-gated. No-op until
	// the operator turns it on via Settings → Ticket Settings.
	ticketAutoClose := services.NewTicketAutoCloseService(pgStore)
	ticketAutoClose.SetLeader(coreLeader)
	ticketAutoClose.Start(bgCtx)

	// Server-audit retention sweep — daily, leader-gated. No-op
	// when audit.server_retention_days is 0 (keep forever).
	serverAuditRetention := services.NewServerAuditRetentionService(pgStore)
	serverAuditRetention.SetLeader(coreLeader)
	serverAuditRetention.Start(bgCtx)

	// Modpack auto-update checker — hourly, leader-gated. Pauses when the
	// modpacks feature is off; per-row staleness governed by the admin cadence
	// setting (modpack_update_check_interval_hours, default 24h).
	modpackUpdateChecker := services.NewModpackUpdateChecker(pgStore, appState.FeatureFlags)
	modpackUpdateChecker.SetLeader(coreLeader)
	modpackUpdateChecker.Start(bgCtx)

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
	migrationOrchestrator.Start(bgCtx)
	appState.Migration = migrationOrchestrator
	migrationOrchestrator.SetCPUPinning(appState.CPUPinning)

	// Rebalance worker — leader-gated ticker that migrates eligible (auto_move,
	// 0-player) servers off overloaded nodes by enqueuing onto the orchestrator.
	// No-op unless auto-move is enabled AND gateway routing is active.
	rebalanceWorker := services.NewRebalanceWorker(pgStore, redisClient, migrationOrchestrator, appState.FeatureFlags)
	rebalanceWorker.SetLeader(coreLeader)
	rebalanceWorker.Start(bgCtx)

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

	// Warp: external/home node WireGuard bridge (multi-hub registry).
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

	root, extras := buildAPIRouter(appState, authHandler, routeCfg{
		JWTSecret:               cfg.JWTSecret,
		Region:                  cfg.Region,
		CoreID:                  cfg.CoreID,
		TabProxyOrigin:          cfg.TabProxyOrigin,
		ClusterSecret:           cfg.ClusterSecret,
		ModrinthUA:              "Dylaris/0.10 (+https://github.com/Bartis-Dev/dylaris-platform)",
		TabProxyIsolationActive: cfg.TabProxyIsolationActive,
	})
	// boot-time warp resync watcher + firewall-allowlist publish use the
	// handlers/service buildAPIRouter just constructed.
	extras.warpService.StartResyncWatcher(coreLeader.IsLeader)
	// Publish the spoke firewall allowlist to the central Redis key the warp
	// leaders read and poll, so a freshly (re)started leader gets the admin value
	// rather than only its compiled-in fail-closed default. Always write (even
	// the default) so a stale value from a previous install cannot linger.
	if err := redisClient.Set(context.Background(), handlers.WarpFirewallRedisKey, extras.settingsHandler.LoadWarpSpokeAllowedPorts(), 0).Err(); err != nil {
		log.Printf("WARNING: failed to publish warp-firewall allowlist to Redis at boot; leaders may still be running a stale/compiled-in default until the next successful save: %v", err)
	}

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
	// Per-node Redis-ACL handshake. Runs on every node connect (Redis ACL is
	// mandatory); provisions the node's scoped ACL users and mints/returns its
	// per-node secret.
	aclProvisioner := redisacl.NewProvisioner(redisClient)
	// Hand the warp handler Core's ACL provisioner + cluster secret so the
	// route-only link-boot endpoint can derive and provision per-link creds.
	appState.ACLProvisioner = aclProvisioner
	appState.ClusterSecret = cfg.ClusterSecret
	appState.AdminSecret = cfg.AdminSecret
	appState.SuspendGrace = cfg.SuspendGrace

	// ACL reconciler - leader-gated. Periodically (and on a Redis reconnect)
	// re-provisions every paired node's + route-only link's scoped Redis ACL
	// users from the DB-stored per-node secret, so a Valkey restart that lost the
	// aclfile self-heals without a service restart. Same users, same passwords;
	// running services re-auth transparently on their next command.
	aclReconciler := services.NewACLReconciler(pgStore, aclProvisioner, redisClient, cfg.ClusterSecret, cfg.SuspendGrace)
	aclReconciler.SetLeader(coreLeader)
	aclReconciler.Start(bgCtx)
	// The billing lifecycle drops/restores route-only link tunnels on
	// suspend/reactivate; give it the same provisioner, gateway and cluster secret.
	appState.Billing.SetLinkACL(appState.Gateway, redisClient, aclProvisioner, cfg.ClusterSecret)
	appState.Billing.Start(bgCtx)
	aclHandshake := redisacl.NewHandshake(
		&aclHandshakeStore{store: pgStore, flags: appState.FeatureFlags},
		aclProvisioner,
		cfg.ClusterSecret,
	)
	// P0b-5 admission gate: consulted only on the ACL-on enroll path for unknown nodes.
	admissionGate := services.NewAdmissionGate(pgStore)
	// Pre-placement ACL hook: re-apply a node's Redis ACL right before sending a
	// server-placement command, closing the window where a freshly created
	// server's keys are NOPERM for the node until its next reconnect. nil-safe.
	appState.Queue.SetACL(aclHandshake)
	// Serves in its own goroutine; the handle comes back so shutdown can drain
	// node streams instead of severing them. A bind failure surfaces here,
	// synchronously, rather than from inside a goroutine after boot continued.
	grpcServer, err := nodegrpc.StartGRPCServer(cfg.GRPCPort, grpcRegistry, grpcLookup, cfg.CoreID, aclHandshake, appState.Gateway, admissionGate, pgStore, cfg.GRPCTLSEnabled, cfg.ClusterSecret)
	if err != nil {
		log.Fatalf("gRPC server error: %v", err)
	}

	// Core Heartbeat in Redis (so Nodes can discover this Core)
	coreHeartbeat := services.NewCoreHeartbeatService(redisClient, cfg.CoreID, cfg.Region, cfg.GRPCPort)
	coreHeartbeat.Start()

	// Warn once, here, if this instance is joining a deployment that stores
	// files on a host path. That combination is only correct on a single Core,
	// and the panel's warning is only seen by an admin who opens the storage
	// tab - a scale-up is exactly the case where nobody does.
	//
	// Placed AFTER coreHeartbeat.Start() on purpose: Start writes this Core's
	// key before returning, so the count includes us. Running it earlier would
	// have the second Core see only the first and stay silent.
	appState.WarnAboutHostPathAtBoot(context.Background())

	// Backup scheduler — ticks once a minute, dispatches due jobs to nodes.
	// Wire the gRPC mesh in so retention deletes can reach node-local stores.
	// Leader-gated: tick + Pub/Sub result processing run only on
	// the elected Core to avoid double-dispatch and double-result-write.
	backupScheduler := services.NewBackupScheduler(pgStore, redisClient, appState.Queue)
	backupScheduler.SetRegistry(grpcRegistry)
	// Same builder handlers/backup.go supplies, so a job stored on the shared
	// Core file storage behaves identically whether it is reached from a
	// request or from the scheduler. Without it the scheduler is refused by
	// backupstorage.Open for exactly those jobs.
	backupScheduler.SetCoreStorage(appState.CoreStorageBackupBuilder())
	backupScheduler.SetLeader(coreLeader)
	backupScheduler.Start(bgCtx)

	// Scheduled-tasks executor — per-server cron jobs (restart, say).
	// Leader-gated, 30s tick. Publishes scheduled_tasks.changed via the SSE
	// channel after each dispatch so the panel updates last-run/next-run.
	scheduledTasksService := services.NewScheduledTaskService(pgStore, redisClient, appState.Queue, appState.Events)
	scheduledTasksService.SetLeader(coreLeader)
	scheduledTasksService.Start(bgCtx)

	// Telemetry heartbeat. Posts anonymous platform stats to
	// dylaris.dev every 10min for the live counter on the website.
	// Leader-gated so multi-Core deployments don't double-count.
	telemetryHeartbeat := services.NewTelemetryHeartbeat(pgStore, cfg.ClusterSecret, cfg.Region)
	telemetryHeartbeat.SetLeader(coreLeader)
	telemetryHeartbeat.Start(bgCtx)

	// Recovery-token printer. Background loop that logs either the
	// Fresh-Install hint or the Lost-Admin token + URL every 30s as long as
	// the platform has no admin. Stops at the ctx cancel triggered by SIGTERM.
	services.StartSetupRecoveryLoop(bgCtx, pgStore, cfg.FrontendURL)

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
		Handler:           corsObj(root),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Core API crashed: %v", err)
		}
	}()

	// Origin-isolated tab-proxy data plane (spec B5). When TAB_PROXY_PORT is set,
	// Core binds a SECOND HTTP server on that port serving ONLY the proxy data
	// plane (InDashboard + Public + their {rest:.*} variants) with the SAME
	// proxyHandler. It carries NO CORS and NO AuthMiddleware: those routes trust
	// only the host-only dyl_tabproxy ticket cookie (which the browser delivers
	// to this same-host port), and the browser reaches this port as a distinct
	// ORIGIN (different port), so a proxied container's JS runs on this origin and
	// can never read the panel token from the panel origin's localStorage. Route
	// patterns mirror the root-router registrations exactly. The mint endpoints
	// stay on the panel origin's main listener (behind AuthMiddleware) and are
	// deliberately NOT served here. ReadHeaderTimeout/IdleTimeout mirror the main
	// server; ReadTimeout/WriteTimeout stay unset because the proxy streams.
	var tabProxySrv *http.Server
	if cfg.TabProxyPort != "" {
		tpRouter := mux.NewRouter()
		tpRouter.HandleFunc("/api/servers/{id:[0-9]+}/tabs/{tabId:[0-9]+}/proxy", extras.proxyHandler.InDashboard)
		tpRouter.HandleFunc("/api/servers/{id:[0-9]+}/tabs/{tabId:[0-9]+}/proxy/{rest:.*}", extras.proxyHandler.InDashboard)
		tpRouter.HandleFunc("/api/tabproxy/{token}", extras.proxyHandler.Public)
		tpRouter.HandleFunc("/api/tabproxy/{token}/{rest:.*}", extras.proxyHandler.Public)
		tabProxySrv = &http.Server{
			Addr:              ":" + cfg.TabProxyPort,
			Handler:           tpRouter,
			ReadHeaderTimeout: 15 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		log.Printf("Dylaris Core tab-proxy (origin-isolated) listener running on port %s", cfg.TabProxyPort)
		go func() {
			if err := tabProxySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Core tab-proxy listener crashed: %v", err)
			}
		}()
	}

	// Graceful shutdown: mirrors node/main.go's signal handling. Rolling
	// deploys (docker-stack.yml start-first, 2 core replicas) SIGTERM the
	// outgoing task while the new one is already serving; without this the
	// old task hard-kills mid-request, dropping in-flight backup/migration
	// jobs and live SSE (/api/system/events) subscribers.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("Shutting down Core gracefully...")

	// Teardown order is load-bearing and runs outside-in: stop taking work,
	// then stop doing work, then drop the connections both needed.
	//
	//  1. Drain the HTTP listeners. Requests in flight still have every
	//     background service and both Redis and Postgres under them.
	//  2. Drain node gRPC streams, which HTTP requests drive (file transfers,
	//     the tab-proxy bridge), so they cannot be cut from under a request.
	//  3. Cancel the background loops.
	//  4. Give up this Core's identity in Redis: delete the heartbeat, release
	//     the leader lease. Both write to Redis, so both must precede its close.
	//  5. Close Redis last.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		// Shutdown waits for active connections and gives up when the context
		// expires; it does not hang up on the stragglers. Close does, and
		// without it they are cut by process exit anyway - a beat later and
		// without their connection being closed.
		log.Printf("Core graceful shutdown error, closing remaining connections: %v", err)
		if cerr := srv.Close(); cerr != nil {
			log.Printf("Core listener close error: %v", cerr)
		}
	}
	if tabProxySrv != nil {
		// Own fresh timeout: the main server's Shutdown above may have already
		// consumed most/all of shutdownCtx's budget, which would leave the
		// tab-proxy listener no drain window and hard-kill in-flight proxied
		// streams. Give it an independent 30s to drain.
		tpCtx, tpCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer tpCancel()
		if err := tabProxySrv.Shutdown(tpCtx); err != nil {
			log.Printf("Core tab-proxy graceful shutdown error, closing remaining connections: %v", err)
			if cerr := tabProxySrv.Close(); cerr != nil {
				log.Printf("Core tab-proxy listener close error: %v", cerr)
			}
		}
	}

	// GracefulStop sends GOAWAY and then waits for every pending RPC. A node
	// stream is a long-lived RPC, so a node that has gone silent rather than
	// disconnected keeps this waiting until server keepalive tears its
	// transport down (Time 30s / Timeout 10s). Bound it and fall back to Stop,
	// which closes the transports outright; that also releases a GracefulStop
	// still blocked on them, which is why the goroutine is joined afterwards
	// instead of abandoned.
	grpcStopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(grpcStopped)
	}()
	select {
	case <-grpcStopped:
	case <-time.After(10 * time.Second):
		log.Println("gRPC: node streams did not drain in 10s, closing them")
		grpcServer.Stop()
		<-grpcStopped
	}

	// Background loops stop here rather than earlier: until this point a
	// request being drained above could still depend on one of them.
	bgCancel()

	// Stop being discoverable before Redis goes away. The heartbeat key carries
	// a 30s TTL and the leader lease a 30s TTL, so skipping either leaves this
	// instance counted as online, and its lease unavailable to a successor, for
	// that long after the process is gone.
	heartbeatCtx, heartbeatCancel := context.WithTimeout(context.Background(), 5*time.Second)
	coreHeartbeat.Stop(heartbeatCtx)
	heartbeatCancel()
	coreLeader.Stop()

	if err := redisClient.Close(); err != nil {
		log.Printf("Redis close error: %v", err)
	}

	log.Println("Core shutdown complete")
}
