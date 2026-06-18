package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"dylaris-pkg/migration"

	"github.com/redis/go-redis/v9"
)

// Auto-move (Wave 2b) command handlers. Migration is GATEWAY-ONLY: in gateway
// mode the node binds no host ports (see docker_mgr.go), so a move only relocates
// the server's data directory. No host port is allocated or released here.
//
// Redis keys this file owns:
//   dylaris:migration:<uuid>:meta    JSON {sha256,size,sourceNodeID,stagedAt}, TTL ~1h
//   dylaris:migration:<uuid>:status  JSON {phase,error?}, TTL ~1h
// The endpoint key dylaris:migration:endpoint:<nodeID> is owned by migration_server.go.

const (
	migrationStagingDir = ".dylaris-migration"
	migrationMetaTTL    = time.Hour
	migrationStatusTTL  = time.Hour
)

// migrationMeta is published after staging so core can read the archive hash
// (which it forwards to the target as the Pull integrity check).
type migrationMeta struct {
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	SourceNodeID string `json:"sourceNodeID"`
	StagedAt     int64  `json:"stagedAt"` // unix seconds
}

// migrationStatus tracks a move's progress for core/orchestrator polling.
type migrationStatus struct {
	Phase string `json:"phase"`           // staged | transferred | error
	Error string `json:"error,omitempty"` // populated only on phase=error
}

// stagedArchivePath returns the staged-zip path for a server given its storage
// path: <storagePath>/.dylaris-migration/<uuid>.zip.
func stagedArchivePath(storagePath, serverUUID string) string {
	return filepath.Join(storagePath, migrationStagingDir, serverUUID+".zip")
}

// migrationArchivePathFor is the archivePathFor callback handed to
// StartMigrationServer. It resolves the server's storage path and returns the
// staged archive, ok=true only if that file actually exists. We serve a staged
// archive only — never the live server dir.
func migrationArchivePathFor(storage *StorageManager) func(serverUUID string) (string, bool) {
	return func(serverUUID string) (string, bool) {
		storagePath := storage.GetServerPath(serverUUID)
		if storagePath == "" {
			return "", false
		}
		p := stagedArchivePath(storagePath, serverUUID)
		if stat, err := os.Stat(p); err != nil || stat.IsDir() {
			return "", false
		}
		return p, true
	}
}

// setMigrationStatus writes the migration status key (best-effort).
func setMigrationStatus(ctx context.Context, rdb *redis.Client, serverUUID, phase, errMsg string) {
	data, err := json.Marshal(migrationStatus{Phase: phase, Error: errMsg})
	if err != nil {
		return
	}
	key := fmt.Sprintf("dylaris:migration:%s:status", serverUUID)
	if err := rdb.Set(ctx, key, data, migrationStatusTTL).Err(); err != nil {
		log.Printf("migrate: failed to write status for %s: %v", serverUUID, err)
	}
}

// handleMigrateOut (source side) stages the server's data directory as a zip
// and publishes its hash. The orchestrator guarantees the server is already
// stopped before this runs, so we archive the dir as-is.
func handleMigrateOut(ctx context.Context, rdb *redis.Client, storage *StorageManager, serverUUID string) {
	storagePath := storage.GetServerPath(serverUUID)
	if storagePath == "" {
		log.Printf("migrate_out %s: no storage path found", serverUUID)
		setMigrationStatus(ctx, rdb, serverUUID, "error", "storage path not found")
		return
	}
	srcDir := filepath.Join(storagePath, serverUUID)
	if stat, err := os.Stat(srcDir); err != nil || !stat.IsDir() {
		log.Printf("migrate_out %s: server dir missing at %s", serverUUID, srcDir)
		setMigrationStatus(ctx, rdb, serverUUID, "error", "server directory missing")
		return
	}

	stagingDir := filepath.Join(storagePath, migrationStagingDir)
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		log.Printf("migrate_out %s: cannot create staging dir: %v", serverUUID, err)
		setMigrationStatus(ctx, rdb, serverUUID, "error", "cannot create staging dir")
		return
	}

	destZip := stagedArchivePath(storagePath, serverUUID)
	log.Printf("migrate_out %s: archiving %s → %s", serverUUID, srcDir, destZip)
	sha, size, err := migration.Archive(srcDir, destZip)
	if err != nil {
		log.Printf("migrate_out %s: archive failed: %v", serverUUID, err)
		os.Remove(destZip) // partial archive is useless
		setMigrationStatus(ctx, rdb, serverUUID, "error", fmt.Sprintf("archive failed: %v", err))
		return
	}

	meta := migrationMeta{SHA256: sha, Size: size, SourceNodeID: nodeID, StagedAt: time.Now().Unix()}
	metaJSON, _ := json.Marshal(meta)
	metaKey := fmt.Sprintf("dylaris:migration:%s:meta", serverUUID)
	if err := rdb.Set(ctx, metaKey, metaJSON, migrationMetaTTL).Err(); err != nil {
		log.Printf("migrate_out %s: failed to publish meta: %v", serverUUID, err)
		setMigrationStatus(ctx, rdb, serverUUID, "error", "failed to publish meta")
		return
	}

	setMigrationStatus(ctx, rdb, serverUUID, "staged", "")
	log.Printf("migrate_out %s: staged (%s, sha256=%s)", serverUUID, formatBytes(uint64(size)), sha)
}

// handleMigrateIn (target side) pulls the staged archive from the source node
// and extracts it into a locally-selected storage path. It does NOT allocate a
// host port or start the container — the orchestrator sends a normal start
// afterwards, which recreates the container from the extracted .node_config.json.
// migrationProbeTimeout bounds each LAN-candidate reachability probe so a
// same-LAN move stays within a few seconds before falling back to the overlay.
const migrationProbeTimeout = 2 * time.Second

func handleMigrateIn(ctx context.Context, rdb *redis.Client, storage *StorageManager, serverUUID, sourceNodeID, token, expectedSha256 string, sourcePrivateIPs []string) {
	if sourceNodeID == "" || token == "" || expectedSha256 == "" {
		log.Printf("migrate_in %s: missing sourceNodeID/token/expectedSha256", serverUUID)
		setMigrationStatus(ctx, rdb, serverUUID, "error", "missing migrate_in parameters")
		return
	}

	// Resolve where to pull from. The source node refreshes this key while it
	// is up; if it is absent the source is unreachable/down and we cannot move.
	endpointKey := fmt.Sprintf("dylaris:migration:endpoint:%s", sourceNodeID)
	endpoint, err := rdb.Get(ctx, endpointKey).Result()
	if err != nil || endpoint == "" {
		log.Printf("migrate_in %s: source endpoint %s not found: %v", serverUUID, endpointKey, err)
		setMigrationStatus(ctx, rdb, serverUUID, "error", "source endpoint unavailable")
		return
	}

	// Pick a target storage path by free space; this also creates <path>/<uuid>/
	// and persists the storage Redis key, exactly like create/migrate_storage.
	targetPath, err := storage.SelectStoragePath(serverUUID, "")
	if err != nil {
		log.Printf("migrate_in %s: cannot select storage path: %v", serverUUID, err)
		setMigrationStatus(ctx, rdb, serverUUID, "error", fmt.Sprintf("storage selection failed: %v", err))
		return
	}

	// Choose where to pull from. With no LAN IPs (platform moves) this is exactly
	// today's behavior: the overlay endpoint, no probe. For BYON moves we probe
	// the source's LAN IPs first and pull directly over the LAN when reachable,
	// so a same-LAN transfer never hairpins through the warp overlay. The chosen
	// host's full download is still SHA256-verified, so a wrong host can't corrupt.
	chosen := endpoint
	if len(sourcePrivateIPs) > 0 {
		if picked := chooseMigrationHost(ctx, migrationCandidates(endpoint, sourcePrivateIPs), token); picked != "" {
			chosen = picked
		}
	}

	url := fmt.Sprintf("http://%s/migration", chosen)
	tmpZip := filepath.Join(targetPath, serverUUID+".migration-in.zip")
	log.Printf("migrate_in %s: pulling from %s", serverUUID, url)
	if err := migration.Pull(ctx, url, token, expectedSha256, tmpZip, 3); err != nil {
		log.Printf("migrate_in %s: pull failed: %v", serverUUID, err)
		os.Remove(tmpZip)
		// Hash mismatch after retries aborts before extract — never write
		// unverified bytes into the live server directory.
		setMigrationStatus(ctx, rdb, serverUUID, "error", fmt.Sprintf("pull failed: %v", err))
		return
	}

	targetDir := filepath.Join(targetPath, serverUUID)
	if err := migration.Extract(tmpZip, targetDir); err != nil {
		log.Printf("migrate_in %s: extract failed: %v", serverUUID, err)
		os.Remove(tmpZip)
		os.RemoveAll(targetDir)
		setMigrationStatus(ctx, rdb, serverUUID, "error", fmt.Sprintf("extract failed: %v", err))
		return
	}
	os.Remove(tmpZip)

	// SelectStoragePath already persisted node:<nodeID>:server:<uuid>:storage,
	// so the node knows where the server lives for the follow-up start.

	setMigrationStatus(ctx, rdb, serverUUID, "transferred", "")
	log.Printf("migrate_in %s: transferred into %s", serverUUID, targetDir)
}

// migrationCandidates returns host:port endpoints to try, LAN IPs first then the
// overlay endpoint, deduped. The LAN IPs reuse the overlay endpoint's port (the
// migration server listens on all interfaces on a single port).
func migrationCandidates(overlayEndpoint string, privateIPs []string) []string {
	port := "25522"
	if _, p, err := net.SplitHostPort(overlayEndpoint); err == nil && p != "" {
		port = p
	}
	seen := map[string]bool{}
	var out []string
	add := func(hp string) {
		if hp != "" && !seen[hp] {
			seen[hp] = true
			out = append(out, hp)
		}
	}
	for _, ip := range privateIPs {
		add(net.JoinHostPort(ip, port))
	}
	add(overlayEndpoint)
	return out
}

// chooseMigrationHost probes each candidate with a short HEAD request (bearer
// token) and returns the first that answers 200. Empty when none responded
// within the per-candidate budget; the caller then uses the overlay endpoint.
func chooseMigrationHost(ctx context.Context, candidates []string, token string) string {
	for _, hp := range candidates {
		url := fmt.Sprintf("http://%s/migration", hp)
		cctx, cancel := context.WithTimeout(ctx, migrationProbeTimeout)
		req, err := http.NewRequestWithContext(cctx, http.MethodHead, url, nil)
		if err != nil {
			cancel()
			continue
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil && resp != nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return hp
		}
		if resp != nil {
			resp.Body.Close()
		}
	}
	return ""
}

// handleMigrateCleanup (source side) removes the staged archive and the
// original server directory after the target confirms transfer. The
// orchestrator controls ordering and only sends this once migrate_in reported
// "transferred". No host port to release in gateway mode.
func handleMigrateCleanup(ctx context.Context, rdb *redis.Client, storage *StorageManager, serverUUID string) {
	storagePath := storage.GetServerPath(serverUUID)
	if storagePath == "" {
		log.Printf("migrate_cleanup %s: no storage path found", serverUUID)
		setMigrationStatus(ctx, rdb, serverUUID, "error", "storage path not found")
		return
	}

	zipPath := stagedArchivePath(storagePath, serverUUID)
	if err := os.Remove(zipPath); err != nil && !os.IsNotExist(err) {
		log.Printf("migrate_cleanup %s: could not remove staged archive %s: %v", serverUUID, zipPath, err)
	}

	srcDir := filepath.Join(storagePath, serverUUID)
	if err := os.RemoveAll(srcDir); err != nil {
		log.Printf("migrate_cleanup %s: could not remove server dir %s: %v", serverUUID, srcDir, err)
		setMigrationStatus(ctx, rdb, serverUUID, "error", fmt.Sprintf("cleanup failed: %v", err))
		return
	}

	storage.RemoveServerPath(serverUUID)
	log.Printf("migrate_cleanup %s: source data removed", serverUUID)
}
