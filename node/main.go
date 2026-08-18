package main

// ci: trigger full pipeline run (no-op, safe to remove)

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
	"dylaris-pkg/queue"

	"github.com/docker/docker/api/types/container"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

var (
	nodeID        string
	clusterSecret string

	// Node Redis
	redisAddr string
	redisDB   int

	// Redis address passed to MC containers (non-Swarm, can't resolve Swarm DNS)
	mcRedisAddr string
	mcRedisDB   string

	// tenantIsolationEnabled gates per-owner Docker-network isolation for this
	// node. Set in parseConfig from the Redis-reachability guard; false keeps MC
	// servers on the shared dylaris_net.
	tenantIsolationEnabled bool

	nodeTags          string
	nodeRegion        string
	defaultCpusetCpus string
	statsBufferMaxLen int64
	statsStreamMaxLen int64
	storagePaths      string

	// Port-range config
	portRangeStart int
	portRangeEnd   int
	// portRangeNotice explains a fallback to the default range (unset or
	// unparseable PORT_RANGE). Empty when the env value was used as-is.
	// Published in the heartbeat so the panel can surface the typo.
	portRangeNotice string
	portMode        string
	containerPort   int // port MC listens on inside the container

	sftpPort string

	// Dynamic routing modes — loaded from Redis, refreshed every 30s
	routingMode    string // "ip_port" | "both" | "gateway"
	fileAccessMode string // "sftp" | "both" | "beam"

	// pidsLimit is the per-container process/thread cap (cgroup pids controller),
	// loaded from Redis (dylaris:placement:pids_limit) and refreshed every 30s.
	// 0 = unlimited (default). Anti fork-bomb / process-exhaustion guard. Note it
	// counts threads too, so a too-low value would throttle heavy modded servers.
	pidsLimit int64

	// ioWeight is the per-container blkio relative weight (10–1000), loaded from
	// Redis (dylaris:placement:io_weight), refreshed every 30s. 0 = unset.
	// Relative fair-share, not a hard cap; effective only with a blkio-weight
	// scheduler (BFQ/CFQ).
	ioWeight uint16

	// linkImage is the image the node-managed Link sidecar runs (LINK_IMAGE).
	linkImage string
	// nodeManagesLink: when true, the node spawns + manages its own Link sidecar
	// instead of relying on an operator-deployed one. Defaults to nodeExternal;
	// NODE_MANAGES_LINK overrides.
	nodeManagesLink bool
)

// nodeExternal is set at startup: an external/home node forces routing=gateway
// and file access=beam locally, regardless of the platform-global setting
// (spec §9 per-node override), so the panel offers Beam rather than SFTP and
// traffic goes through the gateway instead of a published host port.
//
// It is an ADVERTISED mode, not an enforcement: the SFTP listener is started
// unconditionally further down, so an external node still binds SFTP_PORT.
// Whether it should is a product question, not a comment fix; until it is
// answered, do not read this flag as "SFTP is off here".
var nodeExternal bool

// grpcTLSEnabled gates node<->core gRPC TLS with Core-cert fingerprint pinning.
// Off (default) = plaintext, byte-identical to before. Must match Core's
// GRPC_TLS_ENABLED; skew makes the control channel fail to connect (management
// plane only - running MC servers are unaffected).
var grpcTLSEnabled bool

// grpcTLSFingerprint pins the Core control-channel cert for a node that does NOT
// hold CLUSTER_SECRET (BYON). Delivered out-of-band at enroll (the fingerprint is
// public pinning material, not a secret). Platform nodes leave it empty and derive
// the fingerprint from CLUSTER_SECRET instead.
var grpcTLSFingerprint string

// Redis ACL bootstrap config. Redis ACL is mandatory: the node always fetches its
// per-node secret and derives scoped credentials.
var (
	// coreGRPCAddr is host:port of a Core gRPC endpoint, used for the one-shot
	// secret-bootstrap handshake. Required for a node's first boot, to bootstrap
	// its per-node secret via gRPC (also used to re-confirm after a Redis auth
	// failure); a node with a cached .node_secret can start without it.
	coreGRPCAddr string
	// nodeSecretDir is the directory that holds the cached .node_secret. Set to
	// the first persisted storage path so it survives restarts.
	nodeSecretDir string
	// nodeEnrollToken mirrors NODE_ENROLL_TOKEN (BYON per-user enroll token).
	// Reused by the heartbeat AND the gRPC secret bootstrap.
	nodeEnrollToken string
	// nodeRecoveryToken mirrors NODE_RECOVERY_TOKEN — an admin-minted, single-use
	// token to re-pair a node under its EXISTING identity after its secret was reset.
	nodeRecoveryToken string
)

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

// beamAdvertiseEnabled reports whether this node should publish its Beam
// endpoint to Redis: only when Beam is actually reachable (file mode beam/both,
// or an external node which always forces beam locally). Reads the current
// package-level mode vars, which the 30s mode-refresh loop keeps in sync, so a
// runtime switch into/out of beam starts/stops advertising.
func beamAdvertiseEnabled() bool {
	fam := getFileAccessMode()
	return nodeExternal || fam == "beam" || fam == "both"
}

type NodeCommand struct {
	Action     string          `json:"action"`
	Config     ServerConfig    `json:"config"`
	Installer  InstallerConfig `json:"installer"`
	TargetPath string          `json:"targetPath,omitempty"`

	// migrate_in (auto-move) parameters. Carried as top-level fields like
	// TargetPath rather than stuffed into Config, since they describe
	// the move, not the server.
	SourceNodeID   string `json:"sourceNodeId,omitempty"`
	MigrateToken   string `json:"migrateToken,omitempty"`
	ExpectedSha256 string `json:"expectedSha256,omitempty"`
	// SourcePrivateIPs: the source node's LAN IPs to probe before the overlay
	// (BYON same-LAN fast path). Empty = overlay-only (platform moves).
	SourcePrivateIPs []string `json:"sourcePrivateIps,omitempty"`
	// Pre-signed S3/R2 URLs for the cross-LAN BYON R2 transfer fallback:
	// migrate_push_r2 uploads to PresignedPutURL, migrate_pull_r2 downloads from
	// PresignedGetURL. The node never receives bucket credentials.
	PresignedPutURL string `json:"presignedPutUrl,omitempty"`
	PresignedGetURL string `json:"presignedGetUrl,omitempty"`
}

func main() {
	parseConfig()

	log.Printf("Starting Dylaris Node '%s'...", nodeID)
	log.Printf("Connecting to Redis at %s (DB: %d)", redisAddr, redisDB)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	secret := ensureNodeSecret(ctx)
	if secret == nil {
		log.Fatal("redisacl: shutdown before node secret could be obtained")
	}
	redisUserEff := aclNodeUsername(nodeID)
	redisPassEff := aclNodePassword(secret, nodeID)

	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Username: redisUserEff,
		Password: redisPassEff,
		DB:       redisDB,
	})

	// Non-fatal: retry. On auth failure re-confirm with Core (which re-applies
	// the ACL) and rebuild the client if the secret changed. MC containers keep
	// running throughout, so a slow Core/Valkey never takes the node down.
	backoff := time.Second
	for {
		if err := rdb.Ping(ctx).Err(); err == nil {
			break
		} else {
			log.Printf("redisacl: Redis ping failed, re-confirming ACL with Core: %v", err)
		}
		if s, berr := bootstrapSecretViaGRPC(ctx); berr == nil && len(s) == 32 {
			// setNodeSecret restarts the agent (log.Fatal) if this differs from
			// the secret we just tried against Redis - a genuine rotation, not
			// just Valkey having lost the aclfile. The rebuild below only runs
			// when the secret is unchanged (Core just re-applied the same ACL),
			// clearing any connections poisoned by the earlier auth failure.
			setNodeSecret(s, true)
			_ = rdb.Close()
			rdb = redis.NewClient(&redis.Options{
				Addr: redisAddr, Username: aclNodeUsername(nodeID),
				Password: aclNodePassword(s, nodeID), DB: redisDB,
			})
		} else if berr != nil {
			log.Printf("redisacl: re-confirm with Core failed: %v", berr)
		}
		select {
		case <-ctx.Done():
			log.Fatal("redisacl: shutdown during Redis bootstrap")
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
	log.Println("Connected to Redis (ACL mode)")

	// Redis-ACL recovery watchdog: if a Valkey restart drops this node's scoped
	// ACL user, go-redis re-auth fails (NOAUTH/NOPERM) with no self-heal. On
	// sustained auth failure the watchdog re-bootstraps over gRPC (Redis-
	// independent), making Core re-provision the SAME user + password so this
	// existing rdb re-auths transparently. Fail-closed; capped backoff.
	go redisACLWatchdog(ctx, rdb)

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

	// Wire the control-plane (RCON / tab-proxy) address resolver to the Docker
	// daemon. Daemon-authoritative resolution works from a host-net node and has
	// no DNS-wildcard SSRF surface; the guard still runs on the result.
	resolveMCContainerIP = dockerMgr.ResolveMCContainerIP

	// Tenant network isolation: build the per-owner network manager when the
	// Redis guard allows it. The allocator persists beside .node_secret.
	if tenantIsolationEnabled && !dockerMgr.selfHostNet {
		host, _ := os.Hostname()
		dockerMgr.tenant = newTenantNetworkManager(dockerMgr.cli, dockerMgr.ctx, loadTenantAllocator(nodeSecretDir), host)
		log.Printf("tenant-net: manager active (node container %q, state dir %s)", host, nodeSecretDir)
	} else if tenantIsolationEnabled && dockerMgr.selfHostNet {
		// A host-net node cannot join a per-tenant bridge network, so isolation is
		// unavailable here; MC servers stay on the shared local dylaris_net.
		log.Printf("tenant-net: isolation disabled on this host-net node; MC servers stay on the shared local dylaris_net")
	}

	// Port manager always active — routing mode (from Redis) decides at runtime whether to bind ports
	dockerMgr.portMgr = NewPortManager(rdb, nodeID, portRangeStart, portRangeEnd, getPortMode)
	// Redis holds the port ledger but is in-memory only, so a host reboot wipes
	// it while the containers keep their ports. Rebuild it from them.
	dockerMgr.portMgr.AdoptExistingBindings(dockerMgr.ExistingHostPortBindings())

	// Load routing modes from Redis, refresh every 30s
	// Defaults before the first poll; setModes so nothing reads them unguarded.
	setModes("ip_port", "sftp", portMode, containerPort, ioWeight, pidsLimit)
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

	// One quota provider per storage path: project quotas are per filesystem,
	// so a single provider would silently skip every server not on the first path.
	quotaProvider := NewQuotaSet(storageMgr)
	globalQuotaSet = quotaProvider

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

	// Shared-storage detection. A storage path mounted into two nodes cannot
	// work - node identity itself lives there - and the failure is otherwise
	// silent until a migration destroys a server. Diagnostic only: it warns
	// loudly and reports through the heartbeat, it never refuses to start.
	globalSharedStorage = newSharedStorageDetector(nodeID, storageMgr.Paths)
	globalSharedStorage.Run(ctx.Done())

	go startDiscoveryLoop(ctx, rdb, nodeID, nodeTags, nodeRegion, mon, dockerMgr)
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

	// Beam: file transfer gRPC server (BEAM_GRPC_PORT, default :25521).
	// BEAM_JWT_SECRET must match the gateway's beam-relay so tickets that
	// the relay validated also pass the node-side Authenticate gate.
	beamThrottle := NewBeamThrottle(ctx, rdb)
	beamJWTSecret := getSecretEnv("BEAM_JWT_SECRET")
	if beamJWTSecret == "" {
		log.Printf("BEAM_JWT_SECRET unset — Beam authentication will reject all tickets")
	}
	go StartBeamServer(ctx, rdb, storageMgr, beamThrottle, beamJWTSecret, nodeID)

	// Migration (auto-move) pull endpoint (MIGRATION_PORT, default :25522).
	// archivePathFor serves only a staged archive produced by migrate_out.
	go StartMigrationServer(ctx, rdb, nodeID, migrationArchivePathFor(storageMgr))

	// Node-managed Link sidecar (no-op unless NODE_MANAGES_LINK).
	go startLinkReconciler(ctx, dockerMgr)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	log.Println("Shutting down node gracefully...")
}

// getSecretEnv resolves a secret with Docker/Portainer secrets support.
// Precedence: contents of "<key>_FILE" (trimmed) -> plain "<key>" -> "". This
// lets operators mount a secret at a path instead of putting it in plain env.
// An unreadable/empty *_FILE logs and falls through to the env value.
func getSecretEnv(key string) string {
	if path := os.Getenv(key + "_FILE"); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if v := strings.TrimSpace(string(data)); v != "" {
				return v
			}
			log.Printf("config: %s_FILE (%s) is empty; falling back to %s", key, path, key)
		} else {
			log.Printf("config: failed to read %s_FILE (%s): %v; falling back to %s", key, path, err, key)
		}
	}
	return os.Getenv(key)
}

func parseConfig() {
	if _, err := os.Stat(".env"); os.IsNotExist(err) {
		log.Println("No .env file found. Using system environment variables.")
	} else {
		godotenv.Load()
	}

	// 1. Node Basics
	nodeID = os.Getenv("NODE_ID")
	clusterSecret = getSecretEnv("CLUSTER_SECRET")
	nodeTags = os.Getenv("NODE_TAGS")
	nodeExternal = os.Getenv("NODE_EXTERNAL") == "true" || hasTag(nodeTags, "external")
	if nodeExternal {
		log.Println("Node flagged EXTERNAL — forcing gateway routing + beam file access locally")
	}
	nodeRegion = os.Getenv("NODE_REGION")

	// CLUSTER_SECRET is OPTIONAL: a node authenticates to Redis via its
	// gRPC-bootstrapped per-node secret, not the cluster secret. In-cluster nodes
	// still use CLUSTER_SECRET for the cluster proof + gRPC TLS pin when present.
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
	mcRedisDB = os.Getenv("SIDECAR_REDIS_DB")
	if mcRedisDB == "" {
		mcRedisDB = strconv.Itoa(redisDB)
	}

	// Tenant isolation requires isolated containers to still reach Redis by host
	// IP (they leave dylaris_net). Classify SIDECAR_REDIS_ADDR and fail safe.
	tenantIsolationEnabled = redisAddrIsolationSafe(mcRedisAddr)
	if tenantIsolationEnabled {
		log.Printf("Tenant network isolation ENABLED (SIDECAR_REDIS_ADDR=%q is host-reachable)", mcRedisAddr)
	} else {
		log.Printf("Tenant network isolation DISABLED: SIDECAR_REDIS_ADDR=%q looks Docker-DNS-only; keeping MC servers on dylaris_net so Redis stays reachable", mcRedisAddr)
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

	// Redis ACL bootstrap config. nodeEnrollToken is read here (mirrors the
	// heartbeat's NODE_ENROLL_TOKEN) so the gRPC bootstrap can reuse it.
	coreGRPCAddr = os.Getenv("CORE_GRPC_ADDR")
	nodeEnrollToken = os.Getenv("NODE_ENROLL_TOKEN")
	nodeRecoveryToken = os.Getenv("NODE_RECOVERY_TOKEN")
	// Cache the per-node secret on the first persisted storage path so it
	// survives restarts (resolved the same way StorageManager picks paths[0]).
	nodeSecretDir = firstPersistedStoragePath(storagePaths)
	log.Printf("Redis ACL: node uses scoped credentials (secret dir: %s)", nodeSecretDir)
	if coreGRPCAddr == "" {
		log.Println("WARNING: CORE_GRPC_ADDR is empty. A first-boot node cannot bootstrap its per-node secret without a reachable Core gRPC endpoint; a node with a cached secret can still run.")
	}

	linkImage = os.Getenv("LINK_IMAGE")
	nodeManagesLink = resolveNodeManagesLink(os.Getenv("NODE_MANAGES_LINK"), linkImage)
	if nodeManagesLink && linkImage == "" {
		log.Println("NODE_MANAGES_LINK is on but LINK_IMAGE is empty — the node will not spawn a Link sidecar.")
	}

	grpcTLSEnabled = os.Getenv("GRPC_TLS_ENABLED") == "true"
	grpcTLSFingerprint = os.Getenv("GRPC_TLS_FINGERPRINT")
	if grpcTLSEnabled {
		log.Println("gRPC TLS ENABLED - node pins the Core control-channel cert fingerprint (must match Core GRPC_TLS_ENABLED).")
	} else {
		log.Println("WARNING: GRPC_TLS_ENABLED is false; node<->Core gRPC is UNENCRYPTED. Rely on an encrypted overlay (WireGuard/VPN) between node and Core, or set GRPC_TLS_ENABLED=true.")
	}

	// Port range stays env-only because firewall rules on the host must
	// match. Allocation strategy + container port move to admin settings
	// (published to Redis by Core) and are loaded later in loadModesFromRedis.
	portRangeStart, portRangeEnd, portRangeNotice = resolvePortRange()
	if portRangeNotice != "" {
		log.Printf("Port config: %s", portRangeNotice)
	}
	portMode = "sequential" // default until loadModesFromRedis overrides
	containerPort = 25565   // default MC port; admin can change globally in Settings → Nodes → Placement
	log.Printf("Port config: range=%d-%d (mode/container_port load from settings)", portRangeStart, portRangeEnd)

	sftpPort = os.Getenv("SFTP_PORT")
	if sftpPort == "" {
		sftpPort = "25520"
	}

	// Extra hosts operators trust for Core-minted pack-build .mrpack mirror
	// URLs (installer_modpack.go). Merged once here, before any command
	// processing, so there's no concurrent-write race on modpackAllowedHosts.
	loadExtraModpackHosts()
}

// firstPersistedStoragePath resolves the directory used to cache .node_secret.
// It mirrors how StorageManager picks paths[0]: the first non-empty entry of
// STORAGE_PATHS (as an absolute path), or the default ./dylaris_data/servers
// when unset. Keeping it consistent with StorageManager ensures the cache lands
// on a persisted volume that survives container restarts.
func firstPersistedStoragePath(pathsCSV string) string {
	for _, p := range strings.Split(pathsCSV, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	baseDir, _ := os.Getwd()
	return filepath.Join(baseDir, "dylaris_data", "servers")
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
	// Read the current set, overwrite only what Redis actually carries, then
	// install the whole thing at once. Assigning the globals field by field would
	// let a reader see a half-applied round, and every one of them is read from
	// another goroutine.
	routing, fileAccess, port, cPort, io, pids := getModes()

	if v, err := rdb.Get(ctx, "dylaris:routing_mode").Result(); err == nil && v != "" {
		routing = v
	}
	if v, err := rdb.Get(ctx, "dylaris:file_access_mode").Result(); err == nil && v != "" {
		fileAccess = v
	}
	if v, err := rdb.Get(ctx, "dylaris:placement:port_mode").Result(); err == nil && (v == "sequential" || v == "random") {
		port = v
	}
	if v, err := rdb.Get(ctx, "dylaris:placement:container_port").Result(); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
			cPort = n
		}
	}
	// Per-container pids cap (anti fork-bomb). Missing key / parse error / negative
	// leaves it at 0 = unlimited.
	if v, err := rdb.Get(ctx, "dylaris:placement:pids_limit").Result(); err == nil && v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			pids = n
		}
	}
	// Per-container blkio weight (fair-share). 0 or out-of-range leaves it unset.
	if v, err := rdb.Get(ctx, "dylaris:placement:io_weight").Result(); err == nil && v != "" {
		if n, err := strconv.ParseUint(v, 10, 16); err == nil && (n == 0 || (n >= 10 && n <= 1000)) {
			io = uint16(n)
		}
	}
	// The external-node override is part of the same round, so it is applied
	// BEFORE the values are published rather than as a second write afterwards.
	routing, fileAccess = applyExternalOverride(routing, fileAccess, nodeExternal)
	setModes(routing, fileAccess, port, cPort, io, pids)
	// Per-node storage placement for NEW servers. Node-scoped, because the paths
	// differ per node: a global setting could not name them. Absent/unparseable
	// leaves the node on auto (most free space), which is the historical default.
	if globalStorageMgr != nil {
		mode, order := placementAuto, []string(nil)
		// Our own policy wins; without one we take the fleet default. A stored
		// plain "auto" with no order carries no information, so it falls through
		// rather than pinning this node to auto against the fleet setting.
		for _, key := range []string{
			fmt.Sprintf("dylaris:node:%s:storage_placement", nodeID),
			"dylaris:placement:storage_default",
		} {
			v, err := rdb.Get(ctx, key).Result()
			if err != nil || v == "" {
				continue
			}
			var cfg struct {
				Mode  string   `json:"mode"`
				Order []string `json:"order"`
			}
			if err := json.Unmarshal([]byte(v), &cfg); err != nil {
				log.Printf("storage placement: ignoring unparseable %s: %v", key, err)
				continue
			}
			if cfg.Mode == placementManual || len(cfg.Order) > 0 {
				mode, order = cfg.Mode, cfg.Order
				break
			}
		}
		globalStorageMgr.SetPlacement(mode, order)
	}
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

// globalQuotaSet mirrors globalStorageMgr so the heartbeat can report per-path
// quota enforceability without threading the set through every caller.
var globalQuotaSet *QuotaSet

// globalSharedStorage detects a storage path mounted into more than one node.
// Read by the heartbeat so the conflict reaches the panel, not just the node log.
var globalSharedStorage *sharedStorageDetector

func startDiscoveryLoop(ctx context.Context, rdb *redis.Client, id, tags, region string, mon *agent.Monitor, dm *DockerManager) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	sendHeartbeat(ctx, rdb, id, tags, region, mon, dm)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendHeartbeat(ctx, rdb, id, tags, region, mon, dm)
		}
	}
}

func sendHeartbeat(ctx context.Context, rdb *redis.Client, id, tags, region string, mon *agent.Monitor, dm *DockerManager) {
	key := fmt.Sprintf("dylaris:discovery:%s", id)

	// IP-hiding: only expose public IP when at least one mode uses direct access
	ipHidden := getRoutingMode() == "gateway" && getFileAccessMode() == "beam"
	publicIP := ""
	if !ipHidden {
		publicIP = getOutboundIP()
	}

	ts := time.Now().Unix()
	data := map[string]interface{}{
		"id": id, "name": id, "ip": publicIP,
		"tags": tags, "region": region, "timestamp": ts,
		"ips": map[string]interface{}{
			"public":  publicIP,
			"private": getPrivateIPs(),
		},
	}
	// Auth: the node stamps a per-node HMAC signature instead of shipping the raw
	// CLUSTER_SECRET over Redis. The secret is guaranteed non-nil after the
	// startup bootstrap (main fatals otherwise); read through the guarded
	// accessor since the ACL watchdog / gRPC mesh can rotate it concurrently.
	data["sig"] = aclHeartbeatSig(getNodeSecret(), id, ts)

	// BYON: advertise the per-user enroll token so Core can bind this node to its
	// owner on first discovery. Only present when the operator brought the node
	// with NODE_ENROLL_TOKEN set; platform nodes omit it.
	if nodeEnrollToken != "" {
		data["enrollToken"] = nodeEnrollToken
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

	// Include storage info in heartbeat, enriched with whether each path can
	// actually enforce a disk limit (see StorageInfo.QuotaEnforceable).
	if globalStorageMgr != nil {
		infos := globalStorageMgr.GetStorageInfo()
		enforceable := globalQuotaSet.AvailabilityByPath()
		for i := range infos {
			infos[i].QuotaEnforceable = enforceable[infos[i].Path]
		}
		data["storage"] = infos
	}

	// A storage path shared with another node. Reported every beat rather than
	// once, because the condition persists until an operator changes the mounts
	// and the panel must keep showing it.
	if conflicts := globalSharedStorage.Conflicts(); len(conflicts) > 0 {
		data["sharedStorage"] = conflicts
	}

	// The effective MC host-port range, so an admin can read it in the panel
	// instead of off the stack file - these are the ports the host firewall has
	// to open. The notice is only set when the node fell back to the default.
	data["portRange"] = fmt.Sprintf("%d-%d", portRangeStart, portRangeEnd)
	if portRangeNotice != "" {
		data["portRangeNotice"] = portRangeNotice
	}
	// Which update-feed position this IMAGE was built at. Core cannot see it any
	// other way, and without it the panel assumes the node moved whenever Core
	// did - so an operator who updates Core and leaves the nodes alone is told
	// the node's changes are installed. Omitted entirely when unstamped, so an
	// older node reads as "unknown" rather than as "built at zero".
	if b := feedBaselineValue(); b > 0 {
		data["feedBaseline"] = b
	}
	jsonData, _ := json.Marshal(data)
	if err := rdb.Set(ctx, key, jsonData, 15*time.Second).Err(); err != nil {
		log.Printf("Heartbeat warning: %v", err)
	}

	// Publish host CPU topology (scanned once at startup) so Core + panel can show
	// cores and P/E for the pinning UI. Re-published each beat to keep it alive;
	// a hardware change is picked up on the next node restart.
	if topo := getCPUTopology(); topo != nil {
		if b, err := json.Marshal(topo); err == nil {
			rdb.Set(ctx, fmt.Sprintf("dylaris:node:%s:cpu", id), b, 60*time.Second)
		}
	}
}

// privateIPv4s walks the host's interfaces and returns every non-loopback
// IPv4 address in an RFC1918 range (10/8, 172.16/12, 192.168/16). Single
// source of truth for private-IP enumeration shared by getPrivateIPs (the
// heartbeat) and getNodeIPs (grpc_mesh auth).
func privateIPv4s() []string {
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

func getPrivateIPs() []string {
	return privateIPv4s()
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

func listenForCommands(ctx context.Context, rdb *redis.Client, dm *DockerManager, id string, quota *QuotaSet, storage *StorageManager) {
	stream := fmt.Sprintf("dylaris:node:%s:cmds", id)
	log.Printf("Listening for core commands on stream: %s", stream)

	consumer := queue.NewConsumer(rdb, stream, "node", id)
	// Independent per-server commands may run in parallel; the durable queue
	// ACKs each only after its handler returns, so a crash redelivers in-flight
	// work on restart instead of losing it (the old RPUSH/BLPOP list lost it).
	consumer.Concurrency = 8

	err := consumer.Run(ctx, func(ctx context.Context, payloadBytes []byte) error {
		var cmd NodeCommand
		if err := json.Unmarshal(payloadBytes, &cmd); err != nil {
			log.Printf("Invalid command payload, dropping: %v", err)
			return nil // ack + drop malformed (same as the old loop's skip)
		}
		processCommand(ctx, cmd, string(payloadBytes), rdb, dm, id, quota, storage)
		return nil
	})
	if err != nil && ctx.Err() == nil {
		log.Printf("listenForCommands: consumer stopped: %v", err)
	}
}

// processCommand runs one node command. It is invoked by the durable-queue
// consumer; returning normally lets the queue ACK the message. Handler errors
// are logged (preserving the previous behaviour) rather than surfaced, since
// redelivery is driven by crash-before-return, not per-command logical failure.
func processCommand(ctx context.Context, cmd NodeCommand, payload string, rdb *redis.Client, dm *DockerManager, id string, quota *QuotaSet, storage *StorageManager) {
	log.Printf("Pulled command from queue: '%s'", cmd.Action)

	// Whether a container gets a published host port depends on the routing
	// mode, and Core queues the redeploy IMMEDIATELY after publishing a mode
	// switch to Redis — far sooner than the 30s refresh ticker. Acting on the
	// previous mode leaves servers either unreachable (host binding skipped
	// after a switch back to ip_port) or still exposing the node IP the
	// gateway exists to hide. Re-read here so a command always sees the mode
	// Core published before queueing it; the modes stay node-derived, so
	// applyExternalOverride still forces BYON nodes to gateway.
	loadModesFromRedis(ctx, rdb)

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
				recordDiskLimit(ctx, rdb, cmd.Config.UUID, cmd.Config.Docker.DiskLimit)
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
		if !commandsInFlight.enter("setup:" + cmd.Config.UUID) {
			log.Printf("setup: a setup of %s is already running on this node, ignoring the duplicate", cmd.Config.UUID)
			return
		}
		defer commandsInFlight.leave("setup:" + cmd.Config.UUID)
		subName := cmd.Config.ActiveSubServer
		if subName == "" {
			subName = "server"
		}
		log.Printf("Setting up server %s (sub-server: %s)...", cmd.Config.UUID, subName)

		// Set install-start timestamp for cooldown tracking
		rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:install-start", cmd.Config.UUID), "1", 30*time.Second)

		// Same interlock as the reinstall path: InstallServer can run for
		// minutes (Forge/NeoForge spin up an installer container) and
		// RecreateWithCommand below stop+remove+creates, so without this the
		// reconciler can start a half-installed server or recreate one it
		// thinks was deleted by hand, straight into the installer's directory.
		releaseSetupBusy := holdBusyStatus(rdb, cmd.Config.UUID, "installing", busyStatusTTL)
		defer releaseSetupBusy()
		// An install ends by starting the server, so it is a deliberate retry and
		// clears the give-up marker like start/restart do. Without this the marker
		// outlives the install and Core keeps forcing the freshly installed server
		// back to offline/stopped.
		rdb.Del(ctx, fmt.Sprintf("dylaris:server:%s:reconcile_failed", cmd.Config.UUID))

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
		// A deliberate start is the retry after the reconciler gave up, so it
		// clears the give-up marker. Without this the marker outlives the
		// decision it recorded: Core reads it (status_watcher's
		// consumeReconcileFailures), concludes the server should not be running,
		// and stops the server the user just asked for.
		rdb.Del(ctx, fmt.Sprintf("dylaris:server:%s:reconcile_failed", cmd.Config.UUID))
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
		// Same reason as "start" above: this is a deliberate retry.
		rdb.Del(ctx, fmt.Sprintf("dylaris:server:%s:reconcile_failed", cmd.Config.UUID))
		gracefulStop(rdb, cmd.Config.UUID, dm)
		// gracefulStop leaves "stopping" behind, which is true while the JVM is
		// winding down but reads to a player as "it is not coming back". From
		// here on it is a restart, so say that instead.
		rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "restarting", 30*time.Second)
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
			recordDiskLimit(ctx, rdb, cmd.Config.UUID, cmd.Config.Docker.DiskLimit)
			if err := quota.SetLimit(cmd.Config.UUID, cmd.Config.Docker.DiskLimit); err != nil {
				log.Printf("Quota limit update warning for %s: %v", cmd.Config.UUID, err)
			}
		}

	case "delete":
		// Mirrors the delete_sub_server guard below. This one deletes a whole
		// server directory, so an unnamed target is the more dangerous of the
		// two: see GetServerPath for what an empty UUID used to resolve to.
		if strings.TrimSpace(cmd.Config.UUID) == "" {
			log.Printf("delete: server UUID is empty, aborting")
			return
		}
		log.Printf("Deleting Server %s ...", cmd.Config.UUID)
		dm.PowerAction(cmd.Config.UUID, "delete")

		if quota != nil {
			quota.RemoveQuota(cmd.Config.UUID)
		}

		serverPath := storage.GetServerDir(cmd.Config.UUID)
		if serverPath == "" {
			log.Printf("delete %s: no server directory resolved, skipping the data removal", cmd.Config.UUID)
			return
		}
		os.RemoveAll(serverPath)
		storage.RemoveServerPath(cmd.Config.UUID)
		// The cached disk limit has no TTL and its only other delete fires when
		// Core pushes "unlimited", so without this every deleted server left a
		// key behind for good. The port and storage keys next to it were already
		// released on this path; this one was missed.
		forgetDiskLimit(ctx, rdb, cmd.Config.UUID)
		dm.ReleaseTenant(cmd.Config.UUID)
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

		if !commandsInFlight.enter("reinstall:" + cmd.Config.UUID) {
			log.Printf("reinstall: a reinstall of %s is already running on this node, ignoring the duplicate", cmd.Config.UUID)
			return
		}
		defer commandsInFlight.leave("reinstall:" + cmd.Config.UUID)
		// Hold the reconciler off for the whole reinstall: we are about to stop a
		// container whose desired_state is still "online" and then delete its JARs.
		releaseBusy := holdBusyStatus(rdb, cmd.Config.UUID, "installing", busyStatusTTL)
		defer releaseBusy()
		// Same as the setup path: a reinstall ends by starting the server.
		rdb.Del(ctx, fmt.Sprintf("dylaris:server:%s:reconcile_failed", cmd.Config.UUID))

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

		// Same reason as the reinstall path above, and the stakes are higher: the
		// server directory is about to MOVE, so a reconciler restart mid-migration
		// would recreate the container against a path that is being emptied.
		if !commandsInFlight.enter("migrate_storage:" + cmd.Config.UUID) {
			log.Printf("migrate_storage: a migration of %s is already running on this node, ignoring the duplicate", cmd.Config.UUID)
			return
		}
		defer commandsInFlight.leave("migrate_storage:" + cmd.Config.UUID)
		releaseMigrateBusy := holdBusyStatus(rdb, cmd.Config.UUID, "installing", busyStatusTTL)
		defer releaseMigrateBusy()

		// Stop the server first
		gracefulStop(rdb, cmd.Config.UUID, dm)

		if err := storage.MigrateServerPath(cmd.Config.UUID, targetPath); err != nil {
			log.Printf("Storage migration failed for %s: %v", cmd.Config.UUID, err)
			rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)
			return
		}

		// The container still binds the OLD path, and it will keep doing so: a
		// restart preserves HostConfig.Binds verbatim (see resolveExistingConfig)
		// precisely so a storage-path resolution drift cannot point a live server
		// at the wrong directory. A migration is the one case where the path is
		// MEANT to move, so the stale container has to go - the next start
		// recreates it from .node_config.json at the new path, which is what
		// migrate_in already relies on.
		//
		// Without this the move looks like a success and then the server crash
		// loops: its /data is the directory we just emptied.
		//
		// ContainerRemove rather than PowerAction("delete") because that also
		// releases the host port, and the server keeps its port across a move.
		mcName := "mc_" + cmd.Config.UUID
		if rmErr := dm.cli.ContainerRemove(ctx, mcName, container.RemoveOptions{Force: true}); rmErr != nil {
			log.Printf("migrate_storage %s: ContainerRemove: %v (probably gone already - the next start recreates it)", cmd.Config.UUID, rmErr)
		}

		rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", cmd.Config.UUID), "stopped", 30*time.Second)
		log.Printf("Migration complete for server %s → %s", cmd.Config.UUID, targetPath)

	case "migrate_out":
		// Source side: stage the (already-stopped) server dir as a zip.
		handleMigrateOut(ctx, rdb, storage, cmd.Config.UUID)

	case "migrate_in":
		// Target side: pull the staged archive and extract it. No
		// container start here — the orchestrator sends start next.
		handleMigrateIn(ctx, rdb, storage, cmd.Config.UUID, cmd.SourceNodeID, cmd.MigrateToken, cmd.ExpectedSha256, cmd.SourcePrivateIPs)

	case "migrate_cleanup":
		// Source side: drop the staged archive + original dir.
		handleMigrateCleanup(ctx, rdb, storage, cmd.Config.UUID)

	case "migrate_push_r2":
		// Source side (cross-LAN BYON fallback): upload the staged archive to R2.
		handleMigratePushR2(ctx, rdb, storage, cmd.Config.UUID, cmd.PresignedPutURL)

	case "migrate_pull_r2":
		// Target side (cross-LAN BYON fallback): download from R2, verify, extract.
		handleMigratePullR2(ctx, rdb, storage, cmd.Config.UUID, cmd.PresignedGetURL, cmd.ExpectedSha256)

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
		// Same interlock as setup/reinstall/migrate_storage, and for the same
		// reason: the restore stops a container whose desired_state is still
		// "online" and then renames its whole directory away. Without this the
		// reconciler starts the server back up mid-restore, against the very
		// directory the swap is about to replace. Caught live on the testbed
		// with a 646MB archive - the window there is only ~2s and the
		// reconciler still hit it; on remote storage it is minutes wide.
		releaseRestoreBusy := holdBusyStatus(rdb, rcmd.ServerUUID, "restarting", busyStatusTTL)
		defer releaseRestoreBusy()
		RunRestore(ctx, rdb, storage, dm, rcmd)

	case "install_mod":
		runInstallMod(storage, payload)
	case "remove_mod":
		runRemoveMod(storage, payload)

	default:
		log.Printf("Unknown action: %s", cmd.Action)
	}
}

// busyStatusTTL is the lifetime of one busy marker. It is refreshed at a third
// of this, so the marker survives as long as the work does, and it expires on
// its own if the node dies mid-operation rather than pinning the server down
// forever.
const busyStatusTTL = 30 * time.Second

// holdBusyStatus holds the node's reconciler off a server while this node works
// on its files, and reports the reason to the panel. Released via the returned
// func.
//
// Callers stop a possibly-RUNNING container and then do long work on it. Without
// this the reconciler sees a container that is not running while desired_state
// is still "online", calls that a crash, and starts it again in the middle of
// the work - against half-installed JARs, or a directory being moved out from
// under it.
//
// TWO keys, because they are two different things and the first cannot do the
// second's job:
//
//   - the status key is a report for the panel. It is a one-shot mailbox: Core's
//     status watcher GETs it and DELETEs it every 5 seconds. Anything written
//     there is gone within 5s however often it is refreshed. Writing the reason
//     there is still right (the panel shows "installing"), it just cannot be the
//     interlock.
//   - the node_busy key is the interlock. Nothing else reads or drains it, so it
//     holds for exactly as long as this node keeps it alive. reconciler.go's
//     isNodeBusy is the only consumer.
//
// This was learned the hard way: the first version of this helper wrote only the
// status key, and a live reinstall showed the key alternating between the value
// and absent - with the reconciler logging "restarting crashed container" in one
// of the gaps.
//
// ttl is a parameter only so the tests can drive the refresh loop; every caller
// passes busyStatusTTL.
func holdBusyStatus(rdb *redis.Client, uuid, status string, ttl time.Duration) func() {
	statusKey := fmt.Sprintf("dylaris:server:%s:status", uuid)
	busyKey := nodeBusyKey(uuid)
	ctx := context.Background()
	write := func() {
		rdb.Set(ctx, busyKey, status, ttl)
		rdb.Set(ctx, statusKey, status, ttl)
	}
	write()
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(ttl / 3)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				write()
			}
		}
	}()
	return func() {
		close(done)
		// The interlock is dropped immediately so the reconciler can resume
		// looking after this server. The status key is left for the caller, which
		// writes the real terminal status right after.
		rdb.Del(ctx, busyKey)
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

	// Announce "stopping" BEFORE the wait, not after it. Every caller used to
	// publish only the final status, and this function takes 5-15s on a real
	// server (3s for save-all, then up to 30s waiting for the JVM to exit). So
	// for that whole window the platform still claimed the server was online:
	// the panel showed it green, and the gateway edge - which renders the player
	// MOTD from this status - told players the NODE was offline, because the MC
	// container's tunnel had already gone while the status had not moved.
	//
	// Callers overwrite this with the outcome (stopped / starting) as before.
	rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:status", uuid), "stopping", 30*time.Second)

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
