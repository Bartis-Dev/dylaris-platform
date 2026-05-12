package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"dylaris-core/config"
	"dylaris-core/database"
	beamEmbed "dylaris-core/embed"
	nodegrpc "dylaris-core/grpc"
	"dylaris-core/handlers"
	"dylaris-core/services"
	"dylaris-core/store"

	gorillaHandlers "github.com/gorilla/handlers"
	"github.com/gorilla/mux"
)

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
		Store:        pgStore,
		GRPCRegistry: grpcRegistry,
	}

	redisClient, err := database.InitRedis(cfg)
	if err != nil {
		log.Fatalf("FATAL: Redis Error: %v", err)
	}

	// Dev only: flush Redis so cached routes/links/heartbeats from the wiped DB
	// don't linger. Pairs with database.WipeAll() in InitDB.
	if cfg.ClearDev {
		if err := redisClient.FlushDB(context.Background()).Err(); err != nil {
			log.Printf("⚠ DEV CLEAR — Redis FLUSHDB failed: %v", err)
		} else {
			log.Println("⚠ DEV CLEAR — Redis flushed")
		}
	}

	appState.Redis = redisClient
	appState.Queue = services.NewQueueService(redisClient)

	discovery := services.NewDiscoveryService(pgStore, redisClient, cfg.ClusterSecret)
	discovery.Start()

	nodeCleanup := services.NewNodeCleanupService(pgStore, 24*time.Hour)
	nodeCleanup.Start()

	statusWatcher := services.NewStatusWatcherService(pgStore, redisClient)
	statusWatcher.Start()

	statsConsumer := services.NewStatsConsumerService(pgStore, redisClient, cfg.CoreID)
	statsConsumer.Start()

	sftpSync := services.NewSFTPSyncService(pgStore, redisClient)
	sftpSync.Start()

	// Fallback cleanup if TimescaleDB retention policy is not active
	go func() {
		for range time.NewTicker(1 * time.Hour).C {
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
	systemHandler := handlers.NewSystemHandler()
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
	coreHeartbeat := services.NewCoreHeartbeatService(redisClient, cfg.CoreID, cfg.GRPCPort)
	coreHeartbeat.Start()

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
	api.HandleFunc("/node/connect", nodeGRPCHandler.NodeConnectHandler).Methods("GET", "POST")

	// --- PROTECTED ENDPOINTS ---
	api.HandleFunc("/auth/profile", authHandler.AuthMiddleware(authHandler.GetProfileHandler)).Methods("GET")
	api.HandleFunc("/auth/profile", authHandler.AuthMiddleware(authHandler.UpdateProfileHandler)).Methods("PUT")
	api.HandleFunc("/auth/2fa/setup", authHandler.AuthMiddleware(authHandler.SetupTOTPHandler)).Methods("POST")
	api.HandleFunc("/auth/2fa/verify", authHandler.AuthMiddleware(authHandler.VerifyTOTPHandler)).Methods("POST")
	api.HandleFunc("/auth/2fa/disable", authHandler.AuthMiddleware(authHandler.DisableTOTPHandler)).Methods("POST")
	api.HandleFunc("/users/{id:[0-9]+}/2fa", authHandler.AuthMiddleware(authHandler.AdminResetTOTPHandler)).Methods("DELETE")

	api.HandleFunc("/users", authHandler.AuthMiddleware(userHandler.GetAllUsers)).Methods("GET")
	api.HandleFunc("/users", authHandler.AuthMiddleware(userHandler.CreateUser)).Methods("POST")
	api.HandleFunc("/users/{id:[0-9]+}", authHandler.AuthMiddleware(userHandler.DeleteUser)).Methods("DELETE")
	api.HandleFunc("/users/{id:[0-9]+}/password", authHandler.AuthMiddleware(userHandler.ResetUserPassword)).Methods("PUT")
	api.HandleFunc("/users/{id:[0-9]+}/route-limit", authHandler.AuthMiddleware(userHandler.GetUserRouteLimit)).Methods("GET")
	api.HandleFunc("/users/{id:[0-9]+}/route-limit", authHandler.AuthMiddleware(userHandler.SetUserRouteLimit)).Methods("PUT")

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

	api.HandleFunc("/servers", authHandler.AuthMiddleware(serverHandler.GetServers)).Methods("GET")
	api.HandleFunc("/servers", authHandler.AuthMiddleware(serverHandler.CreateServer)).Methods("POST")
	api.HandleFunc("/servers/{id:[0-9]+}/power", authHandler.AuthMiddleware(serverHandler.ServerPowerHandler)).Methods("POST")
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
	api.HandleFunc("/servers/{id:[0-9]+}/members/{userId:[0-9]+}", authHandler.AuthMiddleware(memberHandler.UpdateMemberPermissions)).Methods("PATCH")
	api.HandleFunc("/servers/{id:[0-9]+}/members/{userId:[0-9]+}", authHandler.AuthMiddleware(memberHandler.RemoveMember)).Methods("DELETE")
	api.HandleFunc("/servers/{id:[0-9]+}", authHandler.AuthMiddleware(serverHandler.DeleteServer)).Methods("DELETE")
	api.HandleFunc("/servers/{id:[0-9]+}/sub-servers/{subServerName}", authHandler.AuthMiddleware(serverHandler.DeleteSubServer)).Methods("DELETE")
	api.HandleFunc("/servers/{id:[0-9]+}/proxy", authHandler.AuthMiddleware(serverHandler.LinkServerToProxy)).Methods("PUT")
	api.HandleFunc("/servers/{id:[0-9]+}/proxy", authHandler.AuthMiddleware(serverHandler.UnlinkServerFromProxy)).Methods("DELETE")
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
	api.HandleFunc("/gateway/gates", authHandler.AuthMiddleware(gatewayHandler.GetGates)).Methods("GET")
	api.HandleFunc("/gateway/routes", authHandler.AuthMiddleware(gatewayHandler.GetAllRoutes)).Methods("GET")
	api.HandleFunc("/gateway/routes/{domain:.+}", authHandler.AuthMiddleware(gatewayHandler.AdminDeleteRoute)).Methods("DELETE")
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

	// --- Beam Endpoints ---
	api.HandleFunc("/beam/servers", authHandler.AuthMiddleware(beamHandler.GetBeamServers)).Methods("GET")
	api.HandleFunc("/beam/ticket", authHandler.AuthMiddleware(beamHandler.GetBeamTicket)).Methods("POST")
	api.HandleFunc("/beam/config", authHandler.AuthMiddleware(beamHandler.GetBeamConfig)).Methods("GET")
	api.HandleFunc("/tools/beam", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// ?list=1 — return availability without serving binary
		if r.URL.Query().Get("list") == "1" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":   true,
				"available": beamEmbed.ListAvailablePlatforms(),
			})
			return
		}
		platform := r.URL.Query().Get("platform")
		if platform == "" {
			platform = beamEmbed.DetectPlatform(r.UserAgent())
		}
		data := beamEmbed.GetBeamBinary(platform)
		if data == nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":   false,
				"message":   "Beam binary not available for platform: " + platform,
				"available": beamEmbed.ListAvailablePlatforms(),
			})
			return
		}
		filename := beamEmbed.GetBeamFilename(platform)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
		w.Write(data)
	}).Methods("GET")

	corsObj := gorillaHandlers.CORS(
		gorillaHandlers.AllowedOrigins([]string{cfg.FrontendURL, "http://localhost:25510"}),
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
