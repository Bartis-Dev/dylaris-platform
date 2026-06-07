package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	agent "dylaris-agent"

	"github.com/docker/docker/api/types/container"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

var (
	nodeID        string
	clusterSecret string

	// Node Redis
	redisAddr string
	redisUser string
	redisPass string
	redisDB   int

	// Redis address passed to MC containers (non-Swarm, can't resolve Swarm DNS)
	mcRedisAddr string
	mcRedisUser string
	mcRedisPass string
	mcRedisDB   string

	nodeTags          string
	nodeRegion        string
	defaultCpusetCpus string
	statsBufferMaxLen int64
	statsStreamMaxLen int64
	storagePaths      string

	// Port-range config
	portRangeStart int
	portRangeEnd   int
	portMode       string
	containerPort  int // port MC listens on inside the container

	sftpPort string

	// Dynamic routing modes — loaded from Redis, refreshed every 30s
	routingMode    string // "ip_port" | "both" | "gateway"
	fileAccessMode string // "sftp" | "both" | "beam"
)

// nodeExternal is set at startup: an external/home node forces gateway+beam
// locally so it never binds host ports or exposes SFTP, regardless of the
// platform-global routing/file-access setting (spec §9 per-node override).
var nodeExternal bool

// hasTag reports whether comma-separated tags contains target (trimmed).
func hasTag(tags, target string) bool {
	for _, t := range strings.Split(tags, ",") {
		if strings.TrimSpace(t) == target {
			return true
		}
	}
	return false
}

// applyExternalOverride forces gateway+beam when external; otherwise passes through.
func applyExternalOverride(routing, file string, external bool) (string, string) {
	if external {
		return "gateway", "beam"
	}
	return routing, file
}

type NodeCommand struct {
	Action     string          `json:"action"`
	Config     ServerConfig    `json:"config"`
	Installer  InstallerConfig `json:"installer"`
	TargetPath string          `json:"targetPath,omitempty"`
	ProxyUUID  string          `json:"proxyUuid,omitempty"` // used by proxy_network_* commands
}

func main() {
	parseConfig()

	log.Printf("Starting Dylaris Node '%s'...", nodeID)
	log.Printf("Connecting to Redis at %s (User: '%s', DB: %d)", redisAddr, redisUser, redisDB)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Username: redisUser,
		Password: redisPass,
		DB:       redisDB,
	})

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Println("Connected to Redis")

	// Multi-storage: init StorageManager with configured paths
	storageMgr := NewStorageManager(storagePaths, rdb)
	storageMgr.LogStorageInfo()
	globalStorageMgr = storageMgr

	dockerMgr, err := NewDockerManager(storageMgr)
	if err != nil {
		log.Fatalf("Failed to init Docker Manager: %v", err)
	}
	// Make the docker manager available to installers that need a JVM
	// container (Forge / NeoForge).
	SetDockerManager(dockerMgr)

	// Port manager always active — routing mode (from Redis) decides at runtime whether to bind ports
	dockerMgr.portMgr = NewPortManager(rdb, nodeID, portRangeStart, portRangeEnd, portMode)

	// Load routing modes from Redis, refresh every 30s
	routingMode = "ip_port"
	fileAccessMode = "sftp"
	loadModesFromRedis(ctx, rdb)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				loadModesFromRedis(ctx, rdb)
			}
		}
	}()

	// SFTP server: gives users SSH/SFTP access to their server directories
	sftpSrv := NewSFTPServer(rdb, storageMgr, nodeID)
	go sftpSrv.Start(ctx, sftpPort)

	// Quota provider for first storage path (legacy compat) — multi-path quota handled per-path
	quotaProvider := NewQuotaProvider(storageMgr.Paths()[0])

	// On startup, restart any running MC containers with stale Redis config
	dockerMgr.ReconcileRedisEnv()

	// Shared agent monitor for heartbeat + system stats (single gopsutil instance)
	mon, monErr := agent.NewMonitor(agent.MonitorConfig{})
	if monErr != nil {
		log.Printf("Warning: system monitor init failed: %v", monErr)
		mon = nil
	}
	// 1-second poll goroutine for accurate speed delta calculations
	if mon != nil {
		go func() {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					mon.Shutdown()
					return
				case <-ticker.C:
					mon.GetCurrentStats()
				}
			}
		}()
	}

	go startDiscoveryLoop(ctx, rdb, nodeID, clusterSecret, nodeTags, nodeRegion, mon, dockerMgr)
	go listenForCommands(ctx, rdb, dockerMgr, nodeID, quotaProvider, storageMgr)
	go StartStatsCollector(ctx, rdb, dockerMgr, nodeID, statsBufferMaxLen, quotaProvider)
	go StartNodeSystemStats(ctx, rdb, nodeID, statsStreamMaxLen, mon)
	go StartReconciler(ctx, rdb, dockerMgr, storageMgr)
	// Purge .pre-restore-* dirs left over from crashed restores.
	StartRestoreCleanup(ctx, storageMgr)

	// gRPC Mesh: connect outbound to all Cores
	streamHandler := NewStreamHandler(storageMgr)
	meshMgr := NewMeshManager(nodeID, rdb, streamHandler)
	go meshMgr.Run(ctx)

	// Beam: file transfer gRPC server on localhost:9091.
	// BEAM_JWT_SECRET must match the gateway's beam-relay so tickets that
	// the relay validated also pass the node-side Authenticate gate.
	beamThrottle := NewBeamThrottle(ctx, rdb)
	beamJWTSecret := os.Getenv("BEAM_JWT_SECRET")
	if beamJWTSecret == "" {
		log.Printf("BEAM_JWT_SECRET unset — Beam authentication will reject all tickets")
	}
	go StartBeamServer(ctx, rdb, storageMgr, beamThrottle, beamJWTSecret, nodeID)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	log.Println("Shutting down node gracefully...")
}

func parseConfig() {
	if _, err := os.Stat(".env"); os.IsNotExist(err) {
		log.Println("No .env file found. Using system environment variables.")
	} else {
		godotenv.Load()
	}

	// 1. Node Basics
	nodeID = os.Getenv("NODE_ID")
	clusterSecret = os.Getenv("CLUSTER_SECRET")
	nodeTags = os.Getenv("NODE_TAGS")
	nodeExternal = os.Getenv("NODE_EXTERNAL") == "true" || hasTag(nodeTags, "external")
	if nodeExternal {
		log.Println("Node flagged EXTERNAL — forcing gateway routing + beam file access locally")
	}
	nodeRegion = os.Getenv("NODE_REGION")

	if clusterSecret == "" {
		log.Fatal("FATAL: CLUSTER_SECRET is missing!")
	}
	if nodeID == "" {
		hostname, err := os.Hostname()
		if err != nil || hostname == "" {
			log.Fatal("FATAL: NODE_ID is missing and hostname could not be determined!")
		}
		nodeID = hostname
		log.Printf("No Node ID provided. Automatically using system hostname: '%s'", nodeID)
	}

	// 2. Node Redis
	redisAddr = os.Getenv("REDIS_ADDR")
	redisUser = os.Getenv("REDIS_USER")
	redisPass = os.Getenv("REDIS_PASSWORD")
	redisDB = 0
	if dbStr := os.Getenv("REDIS_DB"); dbStr != "" {
		if db, err := strconv.Atoi(dbStr); err == nil {
			redisDB = db
		}
	}

	if redisAddr == "" {
		log.Fatal("FATAL: REDIS_ADDR is missing!")
	}

	// 2b. MC Container Redis (non-Swarm containers can't resolve Swarm DNS)
	mcRedisAddr = os.Getenv("SIDECAR_REDIS_ADDR")
	if mcRedisAddr == "" {
		mcRedisAddr = redisAddr // fallback: works if MC containers can reach Swarm DNS
	}
	mcRedisUser = os.Getenv("SIDECAR_REDIS_USER")
	if mcRedisUser == "" {
		mcRedisUser = redisUser
	}
	mcRedisPass = os.Getenv("SIDECAR_REDIS_PASSWORD")
	if mcRedisPass == "" {
		mcRedisPass = redisPass
	}
	mcRedisDB = os.Getenv("SIDECAR_REDIS_DB")
	if mcRedisDB == "" {
		mcRedisDB = strconv.Itoa(redisDB)
	}

	// 3. CPU Pinning (cpuset-cpus) default for all MC containers on this node
	defaultCpusetCpus = os.Getenv("DYLARIS_CPUSET_CPUS")
	if defaultCpusetCpus != "" {
		log.Printf("Default CPU pinning (cpuset-cpus): %s", defaultCpusetCpus)
	}

	// 4. Stats buffer stream MAXLEN (default: 1800 = 1 hour at 2s intervals)
	// RAM per entry: ~150 bytes (stream overhead + JSON payload)
	//
	// Examples (entries x 150 bytes x server count):
	//   100 servers  x 1800 = 27 MB
	//   10k servers  x 1800 = 2.7 GB
	//   100k servers x 1800 = 27 GB
	//
	// Reduce for large deployments, e.g. 150 (5 min buffer):
	//   100k servers x 150 = 2.25 GB
	statsBufferMaxLen = 1800
	if v := os.Getenv("DYLARIS_STATS_BUFFER_MAXLEN"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			statsBufferMaxLen = n
		}
	}
	log.Printf("Stats buffer MAXLEN: %d entries", statsBufferMaxLen)

	// Node system stats stream (CPU/RAM/Net of the host)
	statsStreamMaxLen = 360 // ~3h at 30s interval
	if v := os.Getenv("STATS_STREAM_MAXLEN"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			statsStreamMaxLen = n
		}
	}

	// Storage paths (comma-separated, default: ./dylaris_data/servers)
	storagePaths = os.Getenv("STORAGE_PATHS")

	// Port range stays env-only because firewall rules on the host must
	// match. Allocation strategy + container port move to admin settings
	// (published to Redis by Core) and are loaded later in loadModesFromRedis.
	portRangeStart = 25600
	portRangeEnd = 30000
	if v := os.Getenv("PORT_RANGE"); v != "" {
		parts := strings.SplitN(v, "-", 2)
		if len(parts) == 2 {
			if s, err := strconv.Atoi(parts[0]); err == nil {
				portRangeStart = s
			}
			if e, err := strconv.Atoi(parts[1]); err == nil {
				portRangeEnd = e
			}
		}
	} else {
		if v := os.Getenv("PORT_RANGE_START"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				portRangeStart = n
			}
		}
		if v := os.Getenv("PORT_RANGE_END"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				portRangeEnd = n
			}
		}
	}
	portMode = "sequential"     // default until loadModesFromRedis overrides
	containerPort = 25565        // default MC port; admin can change globally in Settings → Nodes → Placement
	log.Printf("Port config: range=%d-%d (mode/container_port load from settings)", portRangeStart, portRangeEnd)

	sftpPort = os.Getenv("SFTP_PORT")
	if sftpPort == "" {
		sftpPort = "2222"
	}
}

// getOutboundIP returns the node's public IP address.
// Inside a Docker Swarm stack the UDP-dial trick returns the overlay IP,
// so we hit ipify first and fall back to a secondary service before the UDP trick.
func getOutboundIP() string {
	client := &http.Client{Timeout: 3 * time.Second}
	for _, url := range []string{
		"https://api4.ipify.org",
		"https://api.ipify.org",
		"https://checkip.amazonaws.com",
	} {
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}
		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	// Last resort: UDP routing trick (returns overlay IP inside Swarm)
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// loadModesFromRedis reads routing_mode, file_access_mode, port_mode and
// container_port from Redis. Called on startup and every 30s so the node
// reacts to admin setting changes without a restart.
func loadModesFromRedis(ctx context.Context, rdb *redis.Client) {
	if v, err := rdb.Get(ctx, "dylaris:routing_mode").Result(); err == nil && v != "" {
		routingMode = v
	}
	if v, err := rdb.Get(ctx, "dylaris:file_access_mode").Result(); err == nil && v != "" {
		fileAccessMode = v
	}
	if v, err := rdb.Get(ctx, "dylaris:placement:port_mode").Result(); err == nil && (v == "sequential" || v == "random") {
		portMode = v
	}
	if v, err := rdb.Get(ctx, "dylaris:placement:container_port").Result(); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
			containerPort = n
		}
	}
	routingMode, fileAccessMode = applyExternalOverride(routingMode, fileAccessMode, nodeExternal)
}

// saveNodeConfig persists the ServerConfig as .node_config.json in the server directory.
// The reconciler reads this file to recreate containers that were manually deleted.
func saveNodeConfig(serverDir string, config ServerConfig) {
	data, err := json.Marshal(config)
	if err != nil {
		log.Printf("saveNodeConfig: marshal error for %s: %v", config.UUID, err)
		return
	}
	configPath := filepath.Join(serverDir, ".node_config.json")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		log.Printf("saveNodeConfig: write error for %s: %v", config.UUID, err)
	}
}

// storageManager is set during init and used by heartbeat to publish storage info.
var globalStorageMgr *StorageManager

func startDiscoveryLoop(ctx context.Context, rdb *redis.Client, id, secret, tags, region string, mon *agent.Monitor, dm *DockerManager) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	sendHeartbeat(ctx, rdb, id, secret, tags, region, mon, dm)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendHeartbeat(ctx, rdb, id, secret, tags, region, mon, dm)
		}
	}
}

func sendHeartbeat(ctx context.Context, rdb *redis.Client, id, secret, tags, region string, mon *agent.Monitor, dm *DockerManager) {
	key := fmt.Sprintf("dylaris:discovery:%s", id)

	// IP-hiding: only expose public IP when at least one mode uses direct access
	ipHidden := routingMode == "gateway" && fileAccessMode == "beam"
	publicIP := ""
	if !ipHidden {
		publicIP = getOutboundIP()
	}

	data := map[string]interface{}{
		"id": id, "name": id, "ip": publicIP,
		"clusterSecret": secret, "tags": tags, "region": region, "timestamp": time.Now().Unix(),
		"ips": map[string]interface{}{
			"public":  publicIP,
			"private": getPrivateIPs(),
		},
	}

	// Include live CPU/RAM in heartbeat
	if mon != nil {
		if snap, err := mon.Snapshot(); err == nil {
			data["cpuUsage"] = snap.CPUPercent
			data["ramFree"] = int64(snap.RAMTotal) - int64(snap.RAMUsed)
			data["ramTotal"] = snap.RAMTotal
			data["totalCpu"] = float64(runtime.NumCPU())
		}
	}

	// Include link container count
	if dm != nil {
		data["linkCount"] = dm.CountLinkContainers()
	}

	// Include storage info in heartbeat
	if globalStorageMgr != nil {
		data["storage"] = globalStorageMgr.GetStorageInfo()
	}
	jsonData, _ := json.Marshal(data)
	if err := rdb.Set(ctx, key, jsonData, 15*time.Second).Err(); err != nil {
		log.Printf("Heartbeat warning: %v", err)
	}
}

func getPrivateIPs() []string {
	var ips []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}
		if ip[0] == 10 || (ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31) || (ip[0] == 192 && ip[1] == 168) {
			ips = append(ips, ip.String())
		}
	}
	return ips
}

// scanSubServers lists the immediate sub-directories of serverDir (skipping
// dotfiles / non-directories) and returns a SubServerMetadata slice with
// Name and Type populated for each. MinecraftVersion / Build / ExtraJvmFlags
// are left empty here; the assign flow has its own fallback for those.
func scanSubServers(serverDir string) []SubServerMetadata {
	entries, err := os.ReadDir(serverDir)
	if err != nil {
		return nil
	}
	var subs []SubServerMetadata
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		subDir := filepath.Join(serverDir, e.Name())
		subs = append(subs, SubServerMetadata{
			Name: e.Name(),
			Type: subServerType(subDir),
		})
	}
	return subs
}

// refreshServerMetadata rewrites <serverDir>/.dylaris.json from current
// state. Best-effort: a failure is logged and never aborts the caller.
func refreshServerMetadata(serverDir, uuid, name, image string, ramMB int, cpu float64, active string) {
	subs := scanSubServers(serverDir)
	err := writeServerMetadata(serverDir, ServerMetadata{
		ServerUUID: uuid, Name: name, MemoryMB: ramMB, CPULimit: cpu,
		GameImage: image, ActiveSubServer: active, SubServers: subs,
	})
	if err != nil {
		log.Printf("metadata: write %s failed: %v", uuid, err)
	}
}

func listenForCommands(ctx context.Context, rdb *redis.Client, dm *DockerManager, id string, quota *QuotaProvider, storage *StorageManager) {
	queueKey := fmt.Sprintf("dylaris:node:%s:queue", id)
	log.Printf("Listening for core commands on queue: %s", queueKey)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			result, err := rdb.BLPop(ctx, 0, queueKey).Result()
			if err != nil {
				log.Printf("Queue connection error: %v", err)
				time.Sleep(2 * time.Second)
				continue
			}

			payload := result[1]

			go func(payloadStr string) {
				var cmd NodeCommand
				if err := json.Unmarshal([]byte(payloadStr), &cmd); err != nil {
					log.Printf("Invalid command payload: %v", err)
					return
				}

				log.Printf("Pulled command from queue: '%s'", cmd.Action)

				// Apply node-level default cpuset if not set by core
				if cmd.Config.Docker.CpusetCpus == "" && defaultCpusetCpus != "" {
					cmd.Config.Docker.CpusetCpus = defaultCpusetCpus
				}

				switch cmd.Action {

				case "create":
					// Step 1: create a stopped container slot with no software installed.
					log.Printf("Creating server slot for %s (pending setup)...", cmd.Config.UUID)

					// Assign storage path (auto-balances by free space)
					storagePath, err := storage.SelectStoragePath(cmd.Config.UUID, "")
					if err != nil {
						log.Printf("Failed to select storage path for %s: %v", cmd.Config.UUID, err)
						return
					}
					serverPath := filepath.Join(storagePath, cmd.Config.UUID)
					if err := os.MkdirAll(serverPath, 0755); err != nil {
						log.Printf("Failed to create directory for %s: %v", cmd.Config.UUID, err)
						return
					}

					if quota != nil {
						if err := quota.AssignQuota(cmd.Config.UUID); err != nil {
							log.Printf("Quota assign warning for %s: %v", cmd.Config.UUID, err)
						}
						if cmd.Config.Docker.DiskLimit > 0 {
							if err := quota.SetLimit(cmd.Config.UUID, cmd.Config.Docker.DiskLimit); err != nil {
								log.Printf("Quota limit warning for %s: %v", cmd.Config.UUID, err)
							}
						}
					}

					if err := dm.CreateServerPodStopped(cmd.Config); err != nil {
						log.Printf("Failed to create server pod %s: %v", cmd.Config.UUID, err)
					} else {
						log.Printf("Server slot %s created (pending setup)", cmd.Config.UUID)
						saveNodeConfig(serverPath, cmd.Config)
						refreshServerMetadata(serverPath, cmd.Config.UUID, "", cmd.Config.Docker.Image, cmd.Config.Docker.RAM, cmd.Config.Docker.CPULimit, "")
					}

				case "setup":
					// Step 2: install software into a named sub-server directory, then start.
					subName := cmd.Config.ActiveSubServer
					if subName == "" {
						subName = "server"
					}
					log.Printf("Setting up server %s (sub-server: %s)...", cmd.Config.UUID, subName)

					// Set install-start timestamp for cooldown tracking
					rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:install-start", cmd.Config.UUID), "1", 30*time.Second)

					serverPath := storage.GetServerDir(cmd.Config.UUID)

					// Forge / NeoForge installers need to run inside a Java
					// container; copy the Java image + container UUID from
					// the setup config so the installer has everything it
					// needs without an extra round-trip.
					installerCfg := cmd.Installer
					installerCfg.JavaImage = cmd.Config.Docker.Image
					installerCfg.ServerUUID = cmd.Config.UUID

					if err := InstallServer(serverPath, subName, installerCfg); err != nil {
						log.Printf("Installation failed for %s/%s: %v", cmd.Config.UUID, subName, err)
						rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)
						return
					}

					// Always write eula.txt automatically
					eulaPath := filepath.Join(serverPath, subName, "eula.txt")
					if err := os.WriteFile(eulaPath, []byte("eula=true\n"), 0644); err != nil {
						log.Printf("Failed to write eula.txt for %s/%s: %v", cmd.Config.UUID, subName, err)
					}

					// Build the start command via buildStartCommand (type-aware: jar or argfile form).
					// ExtraJvmFlags is passed directly from Core as a dedicated field (Aikar flags
					// and any server-specific custom flags, already combined and trimmed).
					subServerDir := filepath.Join(serverPath, subName)
					extraJvmFlags := cmd.Config.Docker.ExtraJvmFlags
					startCmd, err := buildStartCommand(subServerDir, cmd.Config.Docker.RAM, extraJvmFlags, cmd.Config.Docker.Image)
					if err != nil {
						log.Printf("buildStartCommand failed for %s/%s: %v", cmd.Config.UUID, subName, err)
						rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)
						return
					}
					cmd.Config.Docker.Command = startCmd

					// Track the active sub-server on disk
					activeFile := filepath.Join(serverPath, ".active_server")
					if err := os.WriteFile(activeFile, []byte(subName), 0644); err != nil {
						log.Printf("Failed to write .active_server for %s: %v", cmd.Config.UUID, err)
					}

					// Recreate container with start command pointing to the sub-server
					if err := dm.RecreateWithCommand(cmd.Config); err != nil {
						log.Printf("Failed to start server pod %s: %v", cmd.Config.UUID, err)
					} else {
						log.Printf("Server %s/%s deployed and running!", cmd.Config.UUID, subName)
						saveNodeConfig(serverPath, cmd.Config)
					}
					// Best-effort metadata refresh regardless of RecreateWithCommand result
					// (installation succeeded; the sub-server directory is valid).
					refreshServerMetadata(serverPath, cmd.Config.UUID, "", cmd.Config.Docker.Image, cmd.Config.Docker.RAM, cmd.Config.Docker.CPULimit, subName)

					// Notify Core that installation is complete
					rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)

				case "switch_server":
					// Update active sub-server on disk, then recreate container with new command.
					subName := cmd.Config.ActiveSubServer
					if subName == "" {
						log.Printf("switch_server for %s: ActiveSubServer is empty, aborting", cmd.Config.UUID)
						return
					}
					log.Printf("Switching server %s to sub-server: %s", cmd.Config.UUID, subName)

					serverPath := storage.GetServerDir(cmd.Config.UUID)
					activeFile := filepath.Join(serverPath, ".active_server")

					// Build the start command for the target sub-server.
					// ExtraJvmFlags is passed directly from Core as a dedicated field.
					switchSubDir := filepath.Join(serverPath, subName)
					extraJvmFlags := cmd.Config.Docker.ExtraJvmFlags
					startCmd, err := buildStartCommand(switchSubDir, cmd.Config.Docker.RAM, extraJvmFlags, cmd.Config.Docker.Image)
					if err != nil {
						log.Printf("buildStartCommand failed for switch %s/%s: %v", cmd.Config.UUID, subName, err)
						rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)
						return
					}
					cmd.Config.Docker.Command = startCmd

					if err := dm.RecreateWithCommand(cmd.Config); err != nil {
						log.Printf("Failed to switch server pod %s: %v", cmd.Config.UUID, err)
					} else {
						if err := os.WriteFile(activeFile, []byte(subName), 0644); err != nil {
							log.Printf("Failed to update .active_server for %s: %v", cmd.Config.UUID, err)
						}
						log.Printf("Server %s switched to sub-server %s", cmd.Config.UUID, subName)
						saveNodeConfig(storage.GetServerDir(cmd.Config.UUID), cmd.Config)
						refreshServerMetadata(serverPath, cmd.Config.UUID, "", cmd.Config.Docker.Image, cmd.Config.Docker.RAM, cmd.Config.Docker.CPULimit, subName)
					}

				case "start":
					log.Printf("Power Action 'start' for Server %s ...", cmd.Config.UUID)
					dm.PullContainerImage(cmd.Config.UUID)
					if err := dm.RestartContainer(cmd.Config.UUID); err != nil {
						log.Printf("Failed to start server %s: %v", cmd.Config.UUID, err)
					} else {
						log.Printf("Server %s started", cmd.Config.UUID)
						rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "starting", 30*time.Second)
					}

				case "stop":
					log.Printf("Graceful stop for Server %s ...", cmd.Config.UUID)
					gracefulStop(rdb, cmd.Config.UUID, dm)
					log.Printf("Server %s stopped", cmd.Config.UUID)
					rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)

				case "kill":
					log.Printf("Force kill for Server %s ...", cmd.Config.UUID)
					if err := dm.PowerAction(cmd.Config.UUID, "kill"); err != nil {
						log.Printf("Failed to kill server %s: %v", cmd.Config.UUID, err)
					} else {
						log.Printf("Server %s killed", cmd.Config.UUID)
						rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)
					}

				case "restart":
					log.Printf("Graceful restart for Server %s ...", cmd.Config.UUID)
					gracefulStop(rdb, cmd.Config.UUID, dm)
					// Clean up stop-requested key to prevent race with new log-shipper instance
					rdb.Del(ctx, fmt.Sprintf("dylaris:server:%s:stop-requested", cmd.Config.UUID))
					time.Sleep(2 * time.Second)
					// Recreate container (more reliable than ContainerStart on exited container)
					if err := dm.RestartContainer(cmd.Config.UUID); err != nil {
						log.Printf("Failed to restart server %s: %v", cmd.Config.UUID, err)
						rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)
					} else {
						log.Printf("Server %s restarted", cmd.Config.UUID)
						rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "starting", 30*time.Second)
					}

				case "update_resources":
					log.Printf("Updating resources for Server %s ...", cmd.Config.UUID)
					if err := dm.UpdateResources(cmd.Config); err != nil {
						log.Printf("Failed to update resources for %s: %v", cmd.Config.UUID, err)
					} else {
						log.Printf("Server %s resources updated and restarted", cmd.Config.UUID)
						resServerPath := storage.GetServerDir(cmd.Config.UUID)
						saveNodeConfig(resServerPath, cmd.Config)
						resActiveBytes, _ := os.ReadFile(filepath.Join(resServerPath, ".active_server"))
						refreshServerMetadata(resServerPath, cmd.Config.UUID, "", cmd.Config.Docker.Image, cmd.Config.Docker.RAM, cmd.Config.Docker.CPULimit, strings.TrimSpace(string(resActiveBytes)))
					}
					if quota != nil {
						if err := quota.SetLimit(cmd.Config.UUID, cmd.Config.Docker.DiskLimit); err != nil {
							log.Printf("Quota limit update warning for %s: %v", cmd.Config.UUID, err)
						}
					}

				case "delete":
					log.Printf("Deleting Server %s ...", cmd.Config.UUID)
					dm.PowerAction(cmd.Config.UUID, "delete")

					if quota != nil {
						quota.RemoveQuota(cmd.Config.UUID)
					}

					serverPath := storage.GetServerDir(cmd.Config.UUID)
					os.RemoveAll(serverPath)
					storage.RemoveServerPath(cmd.Config.UUID)
					log.Printf("Server %s data fully deleted", cmd.Config.UUID)

				case "delete_sub_server":
					subName := cmd.Config.ActiveSubServer
					if subName == "" {
						log.Printf("delete_sub_server for %s: sub-server name is empty, aborting", cmd.Config.UUID)
						return
					}
					log.Printf("Deleting sub-server %s/%s ...", cmd.Config.UUID, subName)

					// Tear down the container fully before touching the
					// filesystem. The container's bind is rooted at the
					// server dir (not the sub-server dir), so even an
					// inactive sub-server can show up busy if the kernel
					// hasn't released the overlay mount yet. Steps:
					//   1) SIGKILL the JVM (cheap, non-blocking)
					//   2) Wait for Docker to actually report the
					//      container stopped — ContainerKill returns
					//      after sending the signal, not after exit
					//   3) Remove the container so the bind goes away
					// Earlier we relied on PowerAction("kill") alone and
					// then immediately RemoveAll'd, which lost a race
					// to a still-held bind and looked to the user like
					// "the delete button does nothing".
					mcName := fmt.Sprintf("mc_%s", cmd.Config.UUID)
					killCtx, killCancel := context.WithTimeout(ctx, 15*time.Second)
					if killErr := dm.cli.ContainerKill(killCtx, mcName, "SIGKILL"); killErr != nil {
						log.Printf("delete_sub_server %s: ContainerKill: %v (probably already stopped — continuing)", cmd.Config.UUID, killErr)
					}
					statusCh, errCh := dm.cli.ContainerWait(killCtx, mcName, container.WaitConditionNotRunning)
					select {
					case <-statusCh:
					case waitErr := <-errCh:
						if waitErr != nil {
							log.Printf("delete_sub_server %s: container wait: %v", cmd.Config.UUID, waitErr)
						}
					case <-killCtx.Done():
						log.Printf("delete_sub_server %s: kill wait timed out, proceeding anyway", cmd.Config.UUID)
					}
					killCancel()
					if rmErr := dm.cli.ContainerRemove(ctx, mcName, container.RemoveOptions{Force: true}); rmErr != nil {
						log.Printf("delete_sub_server %s: ContainerRemove: %v (probably gone already — continuing)", cmd.Config.UUID, rmErr)
					}

					// Drop the per-sub-server log stream. The console
					// keeps its history per (server, sub-server), so a
					// deleted sub-server's logs would otherwise linger
					// in Redis forever -- and reappear in the browser
					// if someone created a new sub-server with the
					// same name. Browser-side clearing happens for
					// free: ConsoleView's effect depends on activeSub
					// and re-fetches when that flips.
					logKey := fmt.Sprintf("dylaris:server:%s:logs:%s", cmd.Config.UUID, subName)
					if delErr := rdb.Del(ctx, logKey).Err(); delErr != nil {
						log.Printf("delete_sub_server %s/%s: log stream delete: %v", cmd.Config.UUID, subName, delErr)
					}

					serverPath := storage.GetServerDir(cmd.Config.UUID)
					subServerPath := filepath.Join(serverPath, subName)

					// Two-phase delete to survive busy filesystems.
					//
					// Phase 1 (sync): atomic rename to a hidden
					// `.pending-delete-<name>-<ns>` sibling. Rename only
					// changes the inode-to-name mapping in the parent
					// dir, which works even if the kernel still holds
					// the original dir open via a bind. Once the rename
					// returns success, the sub-server name is gone from
					// every listing (file browser, scanSubServers, etc.)
					// so the user sees the deletion as instant.
					//
					// Phase 2 (async): RemoveAll the tombstone in the
					// background with retries. If it fails the
					// tombstone stays on disk (hidden) until a future
					// node start sweeps it up; the user-facing state
					// is already correct.
					//
					// Previous attempts called RemoveAll directly and
					// aborted the entire command on dir-still-present,
					// which left .dylaris.json untouched and the empty
					// dir visible -- exactly the symptom the user
					// reported.
					pendingPath := filepath.Join(serverPath, fmt.Sprintf(".pending-delete-%s-%d", subName, time.Now().UnixNano()))
					removed := false
					if renameErr := os.Rename(subServerPath, pendingPath); renameErr == nil {
						removed = true
						go func(p string) {
							for attempt := 0; attempt < 12; attempt++ {
								if err := os.RemoveAll(p); err == nil {
									return
								}
								time.Sleep(time.Duration(attempt+1) * time.Second)
							}
							log.Printf("delete_sub_server: background cleanup of %s gave up after retries (will retry on next node start)", p)
						}(pendingPath)
					} else {
						// Rename failed (typically EXDEV across filesystems
						// or ENOENT race) -- fall back to in-place
						// RemoveAll with stat-verified retries.
						log.Printf("delete_sub_server %s/%s: rename to tombstone failed (%v) -- falling back to in-place RemoveAll", cmd.Config.UUID, subName, renameErr)
						var removeErr error
						for attempt := 1; attempt <= 4; attempt++ {
							removeErr = os.RemoveAll(subServerPath)
							if removeErr == nil {
								if _, statErr := os.Stat(subServerPath); os.IsNotExist(statErr) {
									removed = true
									break
								}
								removeErr = fmt.Errorf("dir still present after RemoveAll")
							}
							log.Printf("delete_sub_server %s/%s: remove attempt %d: %v", cmd.Config.UUID, subName, attempt, removeErr)
							time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
						}
						if !removed {
							log.Printf("Failed to delete sub-server directory %s after retries: %v", subServerPath, removeErr)
							return
						}
					}

					// Check if this was the active sub-server
					activeFile := filepath.Join(serverPath, ".active_server")
					activeBytes, _ := os.ReadFile(activeFile)
					delFinalActive := strings.TrimSpace(string(activeBytes))
					if delFinalActive == subName {
						// Find another sub-server to activate
						entries, _ := os.ReadDir(serverPath)
						newActive := ""
						for _, e := range entries {
							if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
								newActive = e.Name()
								break
							}
						}
						if newActive != "" {
							os.WriteFile(activeFile, []byte(newActive), 0644)
							log.Printf("Activated sub-server %s for %s", newActive, cmd.Config.UUID)
							delFinalActive = newActive
						} else {
							os.Remove(activeFile)
							log.Printf("No sub-servers remaining for %s, pending_setup", cmd.Config.UUID)
							delFinalActive = ""
						}
					}
					// Reflect the post-delete reality in Redis-status so
					// the panel doesn't keep showing "online" / "stopped"
					// for a server that no longer has anything to run.
					// Core resets the DB row optimistically when the
					// active sub-server is deleted, but if the user just
					// dropped the last *inactive* one we still need to
					// notice that nothing's left -- and the stats
					// collector won't fire for a non-existent container.
					statusKey := fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID)
					if delFinalActive == "" {
						rdb.Set(ctx, statusKey, "pending_setup", 30*time.Second)
					} else {
						rdb.Set(ctx, statusKey, "stopped", 30*time.Second)
					}
					log.Printf("Sub-server %s/%s deleted", cmd.Config.UUID, subName)
					refreshServerMetadata(serverPath, cmd.Config.UUID, "", cmd.Config.Docker.Image, cmd.Config.Docker.RAM, cmd.Config.Docker.CPULimit, delFinalActive)

				case "reinstall":
					// Reinstall: stop container, clean JARs, re-install, restart
					subName := cmd.Config.ActiveSubServer
					if subName == "" {
						subName = "server"
					}
					log.Printf("Reinstalling server %s (sub-server: %s)...", cmd.Config.UUID, subName)

					// Set install-start for cooldown
					rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:install-start", cmd.Config.UUID), "1", 30*time.Second)

					// Stop the container first
					dm.PowerAction(cmd.Config.UUID, "stop")
					time.Sleep(3 * time.Second)

					serverPath := storage.GetServerDir(cmd.Config.UUID)
					subServerDir := filepath.Join(serverPath, subName)

					// Clean old JARs and generated directories
					if err := CleanServerJars(subServerDir); err != nil {
						log.Printf("Clean failed for %s/%s: %v", cmd.Config.UUID, subName, err)
					}

					// Re-install with new config (Forge/NeoForge need the
					// Java image to spin up a one-shot installer container).
					installerCfg := cmd.Installer
					installerCfg.JavaImage = cmd.Config.Docker.Image
					installerCfg.ServerUUID = cmd.Config.UUID

					if err := InstallServer(serverPath, subName, installerCfg); err != nil {
						log.Printf("Reinstall failed for %s/%s: %v", cmd.Config.UUID, subName, err)
						rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)
						return
					}

					// Build the start command after reinstall (type-aware).
					// ExtraJvmFlags is passed directly from Core as a dedicated field.
					extraJvmFlags := cmd.Config.Docker.ExtraJvmFlags
					startCmd, err := buildStartCommand(subServerDir, cmd.Config.Docker.RAM, extraJvmFlags, cmd.Config.Docker.Image)
					if err != nil {
						log.Printf("buildStartCommand failed for reinstall %s/%s: %v", cmd.Config.UUID, subName, err)
						rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)
						return
					}
					cmd.Config.Docker.Command = startCmd

					// Recreate container with start command
					if err := dm.RecreateWithCommand(cmd.Config); err != nil {
						log.Printf("Failed to restart server pod %s: %v", cmd.Config.UUID, err)
						rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)
					} else {
						log.Printf("Server %s/%s reinstalled and running!", cmd.Config.UUID, subName)
						rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)
					}
					// Best-effort metadata refresh regardless of RecreateWithCommand result
					// (reinstall succeeded; the sub-server directory has new software).
					refreshServerMetadata(serverPath, cmd.Config.UUID, "", cmd.Config.Docker.Image, cmd.Config.Docker.RAM, cmd.Config.Docker.CPULimit, subName)

				case "migrate_storage":
					targetPath := cmd.TargetPath
					if targetPath == "" {
						log.Printf("migrate_storage for %s: TargetPath is empty, aborting", cmd.Config.UUID)
						return
					}
					log.Printf("Migrating storage for server %s → %s", cmd.Config.UUID, targetPath)

					// Stop the server first
					gracefulStop(rdb, cmd.Config.UUID, dm)

					if err := storage.MigrateServerPath(cmd.Config.UUID, targetPath); err != nil {
						log.Printf("Storage migration failed for %s: %v", cmd.Config.UUID, err)
						rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)
						return
					}

					rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)
					log.Printf("Migration complete for server %s → %s", cmd.Config.UUID, targetPath)

				case "proxy_network_create":
					// config.UUID identifies the proxy server. Idempotent.
					if _, err := dm.EnsureProxyNetwork(cmd.Config.UUID); err != nil {
						log.Printf("proxy_network_create failed for %s: %v", cmd.Config.UUID, err)
					}

				case "proxy_network_destroy":
					if err := dm.RemoveProxyNetwork(cmd.Config.UUID); err != nil {
						log.Printf("proxy_network_destroy failed for %s: %v", cmd.Config.UUID, err)
					}

				case "proxy_network_connect":
					// config.UUID = game-server container, ProxyUUID = proxy whose
					// network the container should attach to (hot, no restart).
					ip, err := dm.ConnectToProxyNetwork(cmd.Config.UUID, cmd.ProxyUUID)
					if err != nil {
						log.Printf("proxy_network_connect failed (%s → %s): %v", cmd.Config.UUID, cmd.ProxyUUID, err)
					} else {
						log.Printf("Connected %s to proxy %s (private IP %s)", cmd.Config.UUID, cmd.ProxyUUID, ip)
						// Publish so the panel can read it without re-inspecting.
						rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:proxy_ip:%s", cmd.Config.UUID, cmd.ProxyUUID), ip, 0)
					}

				case "proxy_network_disconnect":
					if err := dm.DisconnectFromProxyNetwork(cmd.Config.UUID, cmd.ProxyUUID); err != nil {
						log.Printf("proxy_network_disconnect failed (%s → %s): %v", cmd.Config.UUID, cmd.ProxyUUID, err)
					}
					rdb.Del(ctx, fmt.Sprintf("dylaris:server:%s:proxy_ip:%s", cmd.Config.UUID, cmd.ProxyUUID))

				case "backup_run":
					// Re-decode the full payload — BackupRunCommand has many fields
					// the generic NodeCommand struct doesn't carry.
					var bcmd BackupRunCommand
					if err := json.Unmarshal([]byte(payload), &bcmd); err != nil {
						log.Printf("backup_run: decode failed: %v", err)
						return
					}
					log.Printf("backup_run: starting run=%d job=%d server=%s sub=%s", bcmd.RunID, bcmd.JobID, bcmd.ServerUUID, bcmd.SubServer)
					RunBackup(ctx, rdb, storage, bcmd)

				case "backup_restore":
					var rcmd BackupRestoreCommand
					if err := json.Unmarshal([]byte(payload), &rcmd); err != nil {
						log.Printf("backup_restore: decode failed: %v", err)
						return
					}
					log.Printf("backup_restore: starting run=%d server=%s sub=%s", rcmd.RunID, rcmd.ServerUUID, rcmd.SubServer)
					RunRestore(ctx, rdb, storage, dm, rcmd)

				case "install_mod":
					runInstallMod(storage, payload)
				case "remove_mod":
					runRemoveMod(storage, payload)

				default:
					log.Printf("Unknown action: %s", cmd.Action)
				}
			}(payload)
		}
	}
}

// gracefulStop sends save-all and stop commands via the Redis stdin queue,
// waits for the container to exit, and falls back to docker stop if needed.
func gracefulStop(rdb *redis.Client, uuid string, dm *DockerManager) {
	ctx := context.Background()
	inputKey := fmt.Sprintf("dylaris:server:%s:input", uuid)
	stopKey := fmt.Sprintf("dylaris:server:%s:stop-requested", uuid)
	mcName := fmt.Sprintf("mc_%s", uuid)

	// Check if container is running
	info, err := dm.cli.ContainerInspect(ctx, mcName)
	if err != nil || !info.State.Running {
		log.Printf("Server %s container not running, skipping graceful stop", uuid)
		return
	}

	// Set stop flag so log-shipper knows not to restart Java
	rdb.Set(ctx, stopKey, "1", 120*time.Second)

	// Send save-all
	log.Printf("Server %s: sending save-all...", uuid)
	rdb.RPush(ctx, inputKey, "save-all")
	time.Sleep(3 * time.Second)

	// Send stop
	log.Printf("Server %s: sending stop...", uuid)
	rdb.RPush(ctx, inputKey, "stop")

	// Wait for container to exit (max 30s)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		cInfo, cErr := dm.cli.ContainerInspect(ctx, mcName)
		if cErr != nil || !cInfo.State.Running {
			log.Printf("Server %s: container stopped gracefully", uuid)
			return
		}
		time.Sleep(1 * time.Second)
	}

	// Fallback: force stop
	log.Printf("Server %s: didn't stop gracefully, forcing...", uuid)
	dm.PowerAction(uuid, "stop")
}
