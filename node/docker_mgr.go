package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	dockerimage "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
)

type DockerManager struct {
	cli           *client.Client
	ctx           context.Context
	hostDataPath  string                // Host filesystem path for dylaris_data (resolved from volume mount, legacy)
	localDataPath string                // Container-local path for dylaris_data (for file I/O inside this container, legacy)
	storageMgr    *StorageManager       // Multi-path storage manager
	hostPathCache map[string]string     // local storage path -> host path (resolved from container mounts)
	portMgr       *PortManager          // nil when gateway is enabled (port binding not needed)
	tenant        *TenantNetworkManager // nil = isolation disabled (redis guard); servers stay on dylaris_net
}

func NewDockerManager(storageMgr *StorageManager) (*DockerManager, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithVersion("1.44"),
	)
	if err != nil {
		return nil, err
	}

	dm := &DockerManager{
		cli:           cli,
		ctx:           context.Background(),
		storageMgr:    storageMgr,
		hostPathCache: make(map[string]string),
	}

	baseDir, _ := os.Getwd()
	dm.localDataPath = filepath.Join(baseDir, "dylaris_data")
	dm.hostDataPath = dm.resolveHostDataPath()

	if dm.hostDataPath != "" {
		log.Printf("Resolved host data path: %s (local: %s)", dm.hostDataPath, dm.localDataPath)
	} else {
		// Fallback: assume local path = host path (non-containerized / bind-mount setup)
		dm.hostDataPath = dm.localDataPath
		log.Printf("Running outside container or volume not detected — using local path for binds: %s", dm.hostDataPath)
	}

	// Build host path cache for all storage paths
	dm.buildHostPathCache()

	return dm, nil
}

// buildHostPathCache resolves host paths for all storage paths by inspecting container mounts.
func (dm *DockerManager) buildHostPathCache() {
	if dm.storageMgr == nil {
		return
	}

	hostname, err := os.Hostname()
	if err != nil {
		return
	}

	info, err := dm.cli.ContainerInspect(dm.ctx, hostname)
	if err != nil {
		log.Printf("Cannot inspect own container for host path resolution: %v", err)
		return
	}

	for _, p := range dm.storageMgr.Paths() {
		resolved := false
		for _, m := range info.Mounts {
			if strings.HasPrefix(p, m.Destination) || p == m.Destination {
				// Map local path to host path
				hostPath := strings.Replace(p, m.Destination, m.Source, 1)
				dm.hostPathCache[p] = hostPath
				log.Printf("storage: host path %s → %s", p, hostPath)
				resolved = true
				break
			}
		}
		if !resolved {
			// Assume local = host (non-containerized)
			dm.hostPathCache[p] = p
		}
	}
}

// resolveHostServerPath returns the HOST filesystem path for a server's data directory.
// Used for Docker bind mounts (Docker API needs the host path, not the container path).
func (dm *DockerManager) resolveHostServerPath(serverUUID string) string {
	if dm.storageMgr != nil {
		storagePath := dm.storageMgr.GetServerPath(serverUUID)
		if hostBase, ok := dm.hostPathCache[storagePath]; ok {
			return filepath.Join(hostBase, serverUUID)
		}
	}
	// Legacy fallback
	return filepath.Join(dm.hostDataPath, "servers", serverUUID)
}

// resolveLocalServerPath returns the container-local path for a server's data directory.
func (dm *DockerManager) resolveLocalServerPath(serverUUID string) string {
	if dm.storageMgr != nil {
		return dm.storageMgr.GetServerDir(serverUUID)
	}
	return filepath.Join(dm.localDataPath, "servers", serverUUID)
}

// resolveHostDataPath inspects our own container to find the host filesystem path
// of the dylaris_data volume mount. MC containers spawned via Docker API need the
// HOST path for bind mounts, not the container-internal path.
func (dm *DockerManager) resolveHostDataPath() string {
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}

	info, err := dm.cli.ContainerInspect(dm.ctx, hostname)
	if err != nil {
		log.Printf("Could not inspect own container (hostname=%s): %v", hostname, err)
		return ""
	}

	for _, m := range info.Mounts {
		if strings.Contains(m.Destination, "dylaris_data") {
			return filepath.Join(m.Source) // Host path (e.g. /var/lib/docker/volumes/.../_data)
		}
	}

	return ""
}

type DockerConfig struct {
	Image         string  `json:"image"`
	RAM           int     `json:"ram"`
	CPULimit      float64 `json:"cpuLimit"`
	CpusetCpus    string  `json:"cpusetCpus"`
	DiskLimit     int64   `json:"diskLimit"`
	Command       string  `json:"command"`
	ExtraJvmFlags string  `json:"extraJvmFlags"` // JVM flags forwarded from Core (Aikar + server-specific)
	HostPort      int     `json:"hostPort"`      // 0 = auto-allocate from range
	ContainerPort int     `json:"containerPort"` // 0 = use global containerPort var
}

type ServerConfig struct {
	UUID            string       `json:"uuid"`
	OwnerID         string       `json:"ownerId"` // tenant key for network isolation; empty on restart/reconcile (resolved from allocator)
	Docker          DockerConfig `json:"docker"`
	ActiveSubServer string       `json:"activeSubServer"`
	// ExistingBinds, when non-empty, overrides the default bind-mount
	// derivation in startMinecraftContainer. Used by RestartContainer to
	// preserve the previous container's exact mount source, so world data
	// is never lost to a path-resolution change.
	ExistingBinds []string `json:"-"`
}

// buildRedisEnv returns the env-slice that the log-shipper inside the container
// needs to connect to Redis and identify the server stream.
// MC containers are non-Swarm and may not resolve Swarm DNS, so we use
// mcRedis* vars (from SIDECAR_REDIS_ADDR or fallback to REDIS_ADDR).
func buildRedisEnv(uuid, subServer string) []string {
	// Redis ACL is mandatory: MC containers always get the per-node SHIPPER user,
	// scoped to this node's assigned server keys only (no node-scoped keys, no
	// :cmds), so a compromised container can't reach sibling-tenant data or the
	// command stream. Derived deterministically; Core provisions the matching user.
	user := aclShipperUsername(nodeID)
	pass := aclShipperPassword(getNodeSecret(), nodeID)
	env := []string{
		fmt.Sprintf("REDIS_ADDR=%s", mcRedisAddr),
		fmt.Sprintf("REDIS_USER=%s", user),
		fmt.Sprintf("REDIS_PASS=%s", pass),
		fmt.Sprintf("REDIS_DB=%s", mcRedisDB),
		fmt.Sprintf("SERVER_UUID=%s", uuid),
		"TERM=xterm-256color",
	}
	if subServer != "" {
		env = append(env, fmt.Sprintf("SUB_SERVER=%s", subServer))
	}
	return env
}

const linkContainerName = "dylaris_link"

// buildLinkEnv builds the env for a node-managed Link sidecar. Link authenticates
// to Redis with its own per-node ACL user (derived from nodeSecret, provisioned by
// Core), and presents the Core-delivered tunnel token + discovery proof. Redis addr
// uses the SIDECAR (mc) address for the same non-Swarm-DNS reason as MC containers.
func buildLinkEnv(nodeID, linkSecret, linkDiscoveryProof string) []string {
	// Redis ACL is mandatory: Link always authenticates with its own per-node ACL
	// user (nodeSecret guaranteed non-nil after the startup bootstrap).
	user := aclLinkUsername(nodeID)
	pass := aclLinkPassword(getNodeSecret(), nodeID)
	return []string{
		fmt.Sprintf("NODE_ID=%s", nodeID),
		fmt.Sprintf("LINK_SECRET=%s", linkSecret),
		fmt.Sprintf("LINK_DISCOVERY_PROOF=%s", linkDiscoveryProof),
		fmt.Sprintf("REDIS_ADDR=%s", mcRedisAddr),
		fmt.Sprintf("REDIS_USER=%s", user),
		fmt.Sprintf("REDIS_PASS=%s", pass),
		fmt.Sprintf("REDIS_DB=%s", mcRedisDB),
	}
}

// EnsureLinkContainer (re)creates the node-managed Link sidecar on dylaris_net.
// Idempotent recreate: always force-removes any stale same-name container first.
func (dm *DockerManager) EnsureLinkContainer(image, nodeID, linkSecret, linkDiscoveryProof string) error {
	dm.pullImage(image)
	netID, err := dm.ensureGlobalNetwork()
	if err != nil {
		return err
	}
	cc := &container.Config{
		Image:    image,
		Hostname: linkContainerName,
		Env:      buildLinkEnv(nodeID, linkSecret, linkDiscoveryProof),
	}
	// Unlike MC containers, the Link sidecar has no node-side liveness reconciler,
	// so Docker's own restart policy is what keeps it alive across an internal crash.
	// StopLinkContainer still does an explicit ContainerStop+ContainerRemove, which
	// tears it down regardless of restart policy whenever the node wants it gone.
	hc := &container.HostConfig{RestartPolicy: container.RestartPolicy{Name: "unless-stopped"}}
	nc := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"dylaris_net": {NetworkID: netID},
		},
	}
	dm.cli.ContainerRemove(dm.ctx, linkContainerName, container.RemoveOptions{Force: true})
	resp, err := dm.cli.ContainerCreate(dm.ctx, cc, hc, nc, nil, linkContainerName)
	if err != nil {
		return fmt.Errorf("link container create error: %v", err)
	}
	if err := dm.cli.ContainerStart(dm.ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("link container start error: %v", err)
	}
	return nil
}

// StopLinkContainer stops + removes the node-managed Link sidecar (best-effort).
func (dm *DockerManager) StopLinkContainer() {
	timeout := 15
	dm.cli.ContainerStop(dm.ctx, linkContainerName, container.StopOptions{Timeout: &timeout})
	dm.cli.ContainerRemove(dm.ctx, linkContainerName, container.RemoveOptions{Force: true})
}

func (dm *DockerManager) ensureGlobalNetwork() (string, error) {
	netName := "dylaris_net"
	nets, err := dm.cli.NetworkList(dm.ctx, network.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("network list error: %v", err)
	}
	for _, n := range nets {
		if n.Name == netName || strings.HasSuffix(n.Name, "_"+netName) {
			log.Printf("Found overlay network %q (ID: %s)", n.Name, n.ID[:12])
			return n.ID, nil
		}
	}
	return "", fmt.Errorf("overlay network %q not found — ensure it exists in the Swarm stack", netName)
}

// proxyNetworkName returns the Docker network name for a given proxy UUID.
// Swarm's overlay-network names are case-sensitive but we lowercase for safety.
func proxyNetworkName(proxyUUID string) string {
	return "dylaris_proxy_" + strings.ToLower(proxyUUID)
}

// EnsureProxyNetwork creates the per-proxy overlay network if it does not
// already exist. Idempotent — safe to call on every proxy boot. Returns the
// network ID for reuse in subsequent connect calls.
func (dm *DockerManager) EnsureProxyNetwork(proxyUUID string) (string, error) {
	if proxyUUID == "" {
		return "", fmt.Errorf("proxyUUID is required")
	}
	netName := proxyNetworkName(proxyUUID)
	nets, err := dm.cli.NetworkList(dm.ctx, network.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("network list error: %v", err)
	}
	for _, n := range nets {
		if n.Name == netName {
			return n.ID, nil
		}
	}
	res, err := dm.cli.NetworkCreate(dm.ctx, netName, network.CreateOptions{
		Driver:     "overlay",
		Attachable: true,
		Labels: map[string]string{
			"dylaris.role":  "proxy-network",
			"dylaris.proxy": proxyUUID,
		},
	})
	if err != nil {
		return "", fmt.Errorf("proxy network create error: %v", err)
	}
	log.Printf("Created proxy network %q (ID: %s)", netName, res.ID[:12])
	return res.ID, nil
}

// RemoveProxyNetwork removes the per-proxy overlay network. Safe no-op when
// the network doesn't exist. Will error if containers are still attached —
// caller is expected to disconnect game-servers first.
func (dm *DockerManager) RemoveProxyNetwork(proxyUUID string) error {
	netName := proxyNetworkName(proxyUUID)
	if err := dm.cli.NetworkRemove(dm.ctx, netName); err != nil {
		// Already-gone is fine.
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("proxy network remove error: %v", err)
	}
	log.Printf("Removed proxy network %q", netName)
	return nil
}

// ConnectToProxyNetwork attaches an existing container to a proxy overlay
// network without restarting it. Idempotent. Returns the container's IP in
// the network so callers can surface "private endpoints" to users.
func (dm *DockerManager) ConnectToProxyNetwork(containerUUID, proxyUUID string) (string, error) {
	containerName := fmt.Sprintf("mc_%s", containerUUID)
	if _, err := dm.EnsureProxyNetwork(proxyUUID); err != nil {
		return "", err
	}
	netName := proxyNetworkName(proxyUUID)

	// Inspect first to see if the container is already attached.
	inspect, err := dm.cli.ContainerInspect(dm.ctx, containerName)
	if err != nil {
		return "", fmt.Errorf("container inspect error: %v", err)
	}
	if inspect.NetworkSettings != nil {
		if existing, ok := inspect.NetworkSettings.Networks[netName]; ok && existing != nil {
			return existing.IPAddress, nil
		}
	}

	if err := dm.cli.NetworkConnect(dm.ctx, netName, containerName, &network.EndpointSettings{}); err != nil {
		return "", fmt.Errorf("network connect error: %v", err)
	}

	// Inspect again so we can return the freshly-assigned IP.
	inspect, err = dm.cli.ContainerInspect(dm.ctx, containerName)
	if err != nil {
		return "", nil // attached, just couldn't read IP
	}
	if inspect.NetworkSettings != nil {
		if attached, ok := inspect.NetworkSettings.Networks[netName]; ok && attached != nil {
			return attached.IPAddress, nil
		}
	}
	return "", nil
}

// DisconnectFromProxyNetwork detaches a container from a proxy overlay
// network. Safe no-op when the container is not currently attached.
func (dm *DockerManager) DisconnectFromProxyNetwork(containerUUID, proxyUUID string) error {
	containerName := fmt.Sprintf("mc_%s", containerUUID)
	netName := proxyNetworkName(proxyUUID)
	if err := dm.cli.NetworkDisconnect(dm.ctx, netName, containerName, true); err != nil {
		if strings.Contains(err.Error(), "not connected") || strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("network disconnect error: %v", err)
	}
	return nil
}

// tenantEndpoints builds the NetworkingConfig for a server container. Isolation
// off (dm.tenant == nil) or any resolution/allocation error falls back to
// dylaris_net so a server is never left unstartable by the isolation layer. The
// enlarge-on-overflow retry is added in a later task.
func (dm *DockerManager) tenantEndpoints(serverUUID, ownerID, globalNetID string) *network.NetworkingConfig {
	fallback := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"dylaris_net": {NetworkID: globalNetID},
		},
	}
	if dm.tenant == nil {
		return fallback
	}
	nc, err := dm.tenant.endpointsFor(serverUUID, ownerID)
	if errors.Is(err, errSubnetFull) {
		if owner, ok := dm.tenant.resolveOwner(serverUUID, ownerID); ok {
			if eErr := dm.EnlargeTenant(owner); eErr != nil {
				log.Printf("tenant-net: enlarge failed for owner %s: %v", owner, eErr)
			} else {
				nc, err = dm.tenant.endpointsFor(serverUUID, ownerID)
			}
		}
	}
	if err != nil {
		log.Printf("tenant-net: falling back to dylaris_net for %s: %v", serverUUID, err)
		return fallback
	}
	return nc
}

// ReleaseTenant frees a deleted server's tenant assignment; no-op when isolation
// is off. Removes the owner's network when it was the last server.
func (dm *DockerManager) ReleaseTenant(serverUUID string) {
	if dm.tenant == nil {
		return
	}
	dm.tenant.release(serverUUID)
}

// EnlargeTenant recreates an owner's tenant network with a larger subnet and
// rejoins the owner's servers with remapped fixed IPs. Docker cannot resize a
// subnet in place, so the same-named network is force-recreated. Rare: only when
// a tenant exceeds ~60 servers on one node. Reshuffles IPs (mc_<uuid> survives).
func (dm *DockerManager) EnlargeTenant(ownerID string) error {
	t := dm.tenant
	if t == nil {
		return fmt.Errorf("tenant isolation disabled")
	}

	t.mu.Lock()
	used, err := t.discoverUsedSubnets()
	if err != nil {
		t.mu.Unlock()
		return err
	}
	_, newNet, err := t.alloc.enlarge(ownerID, used)
	if err != nil {
		t.mu.Unlock()
		return err
	}
	uuids := t.alloc.serversForOwner(ownerID)
	name := tenantNetworkName(ownerID)
	t.mu.Unlock()

	log.Printf("tenant-net: enlarging %s to %s (%d servers)", name, newNet, len(uuids))

	// Remove the owner's containers so their endpoints release the old network.
	had := make(map[string]bool, len(uuids))
	for _, u := range uuids {
		if _, ierr := dm.cli.ContainerInspect(dm.ctx, "mc_"+u); ierr == nil {
			had[u] = true
			dm.cli.ContainerRemove(dm.ctx, "mc_"+u, container.RemoveOptions{Force: true})
		}
	}

	// Drop the old network (disconnect node first) and recreate at the new subnet.
	t.mu.Lock()
	if id, found, _ := t.findNetwork(name); found {
		_ = t.api.NetworkDisconnect(t.ctx, id, t.nodeContainer, true)
		if rerr := t.api.NetworkRemove(t.ctx, id); rerr != nil {
			log.Printf("tenant-net: enlarge remove old %s: %v", name, rerr)
		}
	}
	_, err = t.EnsureTenantNetwork(ownerID)
	t.mu.Unlock()
	if err != nil {
		return fmt.Errorf("enlarge: recreate net: %w", err)
	}

	// Recreate the servers that had a container (rejoin new net at remapped IP).
	for _, u := range uuids {
		if !had[u] {
			continue
		}
		cfg, ok := dm.loadSavedConfig(u)
		if !ok {
			log.Printf("tenant-net: enlarge cannot recreate %s (no saved config)", u)
			continue
		}
		if rerr := dm.RecreateWithCommand(cfg); rerr != nil {
			log.Printf("tenant-net: enlarge recreate %s: %v", u, rerr)
		}
	}
	return nil
}

// loadSavedConfig reads a server's persisted .node_config.json (written by
// saveNodeConfig). Used by the enlarge path to recreate containers.
func (dm *DockerManager) loadSavedConfig(uuid string) (ServerConfig, bool) {
	var cfg ServerConfig
	if dm.storageMgr == nil {
		return cfg, false
	}
	data, err := os.ReadFile(filepath.Join(dm.storageMgr.GetServerDir(uuid), ".node_config.json"))
	if err != nil {
		return cfg, false
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, false
	}
	return cfg, true
}

// RunInstallerContainer runs a one-shot container with the given image,
// mounting the server's data directory at /data, executing `cmd` from /data
// and returning the combined stdout+stderr output. The container is
// auto-removed when the process exits. Used by Forge/NeoForge installers
// that need a real JVM — we pick whatever Java image the user selected for
// the actual MC server, so the installer matches the runtime version.
//
// Errors are returned with the container logs appended so callers can
// surface a useful failure reason to the panel.
func (dm *DockerManager) RunInstallerContainer(ctx context.Context, serverUUID, subServerName, image string, cmd []string) (string, error) {
	if image == "" {
		return "", fmt.Errorf("installer image is required")
	}
	if subServerName == "" {
		return "", fmt.Errorf("installer container requires a sub-server name")
	}
	hostServerPath := dm.resolveHostServerPath(serverUUID)
	if hostServerPath == "" {
		return "", fmt.Errorf("could not resolve host path for server %s", serverUUID)
	}
	// Mount the SUB-SERVER directory at /data so /data/<jar> resolves
	// to where the installer downloads its JAR. Mounting the server root
	// was a stale shortcut from the single-sub-server era; now the JAR
	// lives one level deeper inside the active sub-server's folder, and
	// java -jar /data/<jar> would otherwise miss it and exit 1 silently.
	hostSubServerPath := filepath.Join(hostServerPath, subServerName)

	// Pull the image first — many user-selected Java images won't be on
	// the node yet at first setup. Ignore errors; container create will
	// re-surface the real problem if it's missing.
	dm.PullImage(image)

	cc := &container.Config{
		Image: image,
		// The mc-java* images set ENTRYPOINT to the log-shipper wrapper so
		// the actual MC server stdout streams to Redis. For one-shot
		// installer runs we want plain `java -jar ...` -- the log-shipper
		// would otherwise refuse to start (it fatals out without
		// SERVER_UUID set) and the installer JAR would never be invoked.
		// Clearing the entrypoint forces Docker to exec our Cmd directly.
		Entrypoint:   strslice.StrSlice{},
		Cmd:          cmd,
		WorkingDir:   "/data",
		AttachStdout: true,
		AttachStderr: true,
		Tty:          false,
	}
	hc := &container.HostConfig{
		Binds: []string{fmt.Sprintf("%s:/data", hostSubServerPath)},
	}
	resp, err := dm.cli.ContainerCreate(ctx, cc, hc, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("installer container create: %w", err)
	}
	cid := resp.ID

	// Remove the container ourselves once done. We deliberately avoid
	// HostConfig.AutoRemove: with auto-remove Docker can delete the container
	// the instant it exits, racing the ContainerLogs call below and losing
	// the installer diagnostics. Remove explicitly after the logs are read.
	defer func() {
		_ = dm.cli.ContainerRemove(context.Background(), cid, container.RemoveOptions{Force: true})
	}()

	if err := dm.cli.ContainerStart(ctx, cid, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("installer container start: %w", err)
	}

	// Wait for completion (or until ctx is cancelled, which the caller can
	// use to enforce a timeout).
	statusCh, errCh := dm.cli.ContainerWait(ctx, cid, container.WaitConditionNotRunning)
	var exitCode int64
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case waitErr := <-errCh:
		if waitErr != nil {
			return "", fmt.Errorf("installer container wait: %w", waitErr)
		}
	case status := <-statusCh:
		exitCode = status.StatusCode
	}

	// Grab the logs even on success — Forge/NeoForge installers print useful
	// diagnostics we want to surface in the panel error if anything goes
	// wrong later (missing libraries, etc.).
	logReader, logErr := dm.cli.ContainerLogs(ctx, cid, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	logs := ""
	if logErr == nil {
		// Non-TTY container logs are a multiplexed stream (8-byte frame
		// headers); demux stdout+stderr into one readable buffer instead of
		// dumping the raw framed bytes.
		var logBuf bytes.Buffer
		_, _ = stdcopy.StdCopy(&logBuf, &logBuf, logReader)
		logReader.Close()
		logs = strings.TrimSpace(logBuf.String())
	}

	if exitCode != 0 {
		return logs, fmt.Errorf("installer exited with code %d", exitCode)
	}
	return logs, nil
}

// PullImage pulls an image with no auth — best-effort, used by the installer
// path to fault in user-selected Java images before running the installer.
func (dm *DockerManager) PullImage(image string) {
	reader, err := dm.cli.ImagePull(dm.ctx, image, dockerimage.PullOptions{})
	if err != nil {
		log.Printf("PullImage(%s): %v", image, err)
		return
	}
	io.Copy(io.Discard, reader)
	reader.Close()
}

// CreateServerPodStopped creates a container without starting it (Step 1 - pending_setup).
// No image pull, no EULA - just directory + stopped container.
func (dm *DockerManager) CreateServerPodStopped(config ServerConfig) error {
	log.Printf("Creating stopped container for: %s", config.UUID)

	netID, err := dm.ensureGlobalNetwork()
	if err != nil {
		return err
	}

	// Create directory locally (via StorageManager or legacy path)
	localServerPath := dm.resolveLocalServerPath(config.UUID)
	os.MkdirAll(localServerPath, 0755)

	// Host path for Docker bind mount
	hostServerPath := dm.resolveHostServerPath(config.UUID)

	// RAM: user-specified + 512MB OOM buffer
	bookedRAM := int64(config.Docker.RAM) * 1024 * 1024
	oomPadding := int64(512) * 1024 * 1024
	nanoCpus := int64(config.Docker.CPULimit * 1e9)

	containerName := fmt.Sprintf("mc_%s", config.UUID)

	cc := &container.Config{
		Image:      config.Docker.Image,
		WorkingDir: "/data",
		Hostname:   containerName,
		Env:        buildRedisEnv(config.UUID, ""),
	}

	hc := &container.HostConfig{
		Resources: container.Resources{
			Memory:     bookedRAM + oomPadding,
			MemorySwap: bookedRAM + oomPadding, // same as Memory = no swap
			NanoCPUs:   nanoCpus,
			CpusetCpus: sanitizeCpusetForHost(config.Docker.CpusetCpus, config.UUID),
		},
		Binds:         []string{fmt.Sprintf("%s:/data", hostServerPath)},
		RestartPolicy: container.RestartPolicy{Name: "no"},
	}
	applyPidsLimit(hc)
	applyIOWeight(hc)

	nc := dm.tenantEndpoints(config.UUID, config.OwnerID, netID)

	dm.cli.ContainerRemove(dm.ctx, containerName, container.RemoveOptions{Force: true})

	_, err = dm.cli.ContainerCreate(dm.ctx, cc, hc, nc, nil, containerName)
	if err != nil {
		return fmt.Errorf("container create error: %v", err)
	}

	log.Printf("Container %s created (stopped, awaiting setup)", containerName)
	return nil
}

// applyPidsLimit sets the cgroup pids cap (anti fork-bomb / process exhaustion)
// on the container when a positive platform limit is configured via
// dylaris:placement:pids_limit. 0 = unlimited, so the field stays nil and Docker
// imposes no cap. Applied at both container-build sites, so every path (create /
// setup / start / recreate / migration restart) is covered. The cgroup pids
// controller counts threads too, so the configured value must be generous enough
// for heavy modded (many-thread) servers.
func applyPidsLimit(hc *container.HostConfig) {
	if pidsLimit > 0 {
		pl := pidsLimit
		hc.Resources.PidsLimit = &pl
	}
}

// applyIOWeight sets the cgroup blkio relative weight (10–1000) on the container
// when configured via dylaris:placement:io_weight. 0 = unset (no weight). This is
// a relative fair-share between containers, not a hard cap, and only takes effect
// with an I/O scheduler that honours blkio weight (BFQ/CFQ); it is a harmless
// no-op otherwise. Applied at both container-build sites alongside applyPidsLimit.
func applyIOWeight(hc *container.HostConfig) {
	if ioWeight >= 10 && ioWeight <= 1000 {
		hc.Resources.BlkioWeight = ioWeight
	}
}

// RecreateWithCommand stops + removes + creates a container with a new sub-server command.
func (dm *DockerManager) RecreateWithCommand(config ServerConfig) error {
	log.Printf("Recreating container %s with sub-server: %s", config.UUID, config.ActiveSubServer)

	containerName := fmt.Sprintf("mc_%s", config.UUID)
	timeout := 15
	dm.cli.ContainerStop(dm.ctx, containerName, container.StopOptions{Timeout: &timeout})
	dm.cli.ContainerRemove(dm.ctx, containerName, container.RemoveOptions{Force: true})

	netID, err := dm.ensureGlobalNetwork()
	if err != nil {
		return err
	}

	_, err = dm.startMinecraftContainer(config, netID)
	return err
}

func (dm *DockerManager) PowerAction(uuid string, action string) error {
	mcName := fmt.Sprintf("mc_%s", uuid)

	switch action {
	case "start":
		err := dm.cli.ContainerStart(dm.ctx, mcName, container.StartOptions{})
		if err != nil && strings.Contains(err.Error(), "already exists") {
			// A stale network endpoint left by an ungraceful prior exit blocks the
			// start ("endpoint with name mc_... already exists in network ..."),
			// which otherwise wedges the reconciler's crash-restart loop. Force-
			// disconnect the container from every network it is attached to (that
			// clears the dangling endpoint) and retry once; ContainerStart re-creates
			// the endpoint from the container's own network config.
			if info, ierr := dm.cli.ContainerInspect(dm.ctx, mcName); ierr == nil {
				for netName := range info.NetworkSettings.Networks {
					_ = dm.cli.NetworkDisconnect(dm.ctx, netName, mcName, true)
				}
			}
			err = dm.cli.ContainerStart(dm.ctx, mcName, container.StartOptions{})
		}
		return err
	case "stop":
		timeout := 30
		return dm.cli.ContainerStop(dm.ctx, mcName, container.StopOptions{Timeout: &timeout})
	case "kill":
		return dm.cli.ContainerKill(dm.ctx, mcName, "SIGKILL")
	case "delete":
		err := dm.cli.ContainerRemove(dm.ctx, mcName, container.RemoveOptions{Force: true})
		if err == nil && dm.portMgr != nil {
			dm.portMgr.ReleasePort(uuid)
		}
		return err
	}

	return nil
}

func (dm *DockerManager) startMinecraftContainer(config ServerConfig, netID string) (string, error) {
	dm.pullImage(config.Docker.Image)

	// Create directory locally (via StorageManager or legacy path)
	localServerPath := dm.resolveLocalServerPath(config.UUID)
	os.MkdirAll(localServerPath, 0755)

	// Host path for Docker bind mount
	hostServerPath := dm.resolveHostServerPath(config.UUID)

	// RAM: user-specified + 512MB OOM buffer
	bookedRAM := int64(config.Docker.RAM) * 1024 * 1024
	oomPadding := int64(512) * 1024 * 1024
	nanoCpus := int64(config.Docker.CPULimit * 1e9)
	cmdParts := strings.Fields(config.Docker.Command)

	containerName := fmt.Sprintf("mc_%s", config.UUID)

	cc := &container.Config{
		Image:      config.Docker.Image,
		Cmd:        cmdParts,
		WorkingDir: fmt.Sprintf("/data/%s", config.ActiveSubServer),
		Hostname:   containerName,
		Env:        buildRedisEnv(config.UUID, config.ActiveSubServer),
	}

	binds := []string{fmt.Sprintf("%s:/data", hostServerPath)}
	if len(config.ExistingBinds) > 0 {
		// Preserve the previous container's exact mounts on restart so a
		// silent storage-path change can never strand the world data.
		binds = config.ExistingBinds
	}

	hc := &container.HostConfig{
		Resources: container.Resources{
			Memory:     bookedRAM + oomPadding,
			MemorySwap: bookedRAM + oomPadding, // same as Memory = no swap
			NanoCPUs:   nanoCpus,
			CpusetCpus: sanitizeCpusetForHost(config.Docker.CpusetCpus, config.UUID),
		},
		Binds:         binds,
		RestartPolicy: container.RestartPolicy{Name: "no"},
	}
	applyPidsLimit(hc)
	applyIOWeight(hc)

	// Port binding: only in direct port mode (routing_mode != "gateway").
	// Reuse an already-allocated port if one exists, otherwise allocate a new one.
	if routingMode != "gateway" && dm.portMgr != nil {
		cPort := config.Docker.ContainerPort
		if cPort == 0 {
			cPort = containerPort
		}
		hostP := dm.portMgr.GetPort(config.UUID)
		if hostP == 0 {
			if config.Docker.HostPort > 0 {
				if err := dm.portMgr.SetPort(config.UUID, config.Docker.HostPort); err != nil {
					return "", fmt.Errorf("port assignment failed: %w", err)
				}
				hostP = config.Docker.HostPort
			} else {
				var portErr error
				hostP, portErr = dm.portMgr.AllocatePort(config.UUID)
				if portErr != nil {
					return "", fmt.Errorf("port allocation failed: %w", portErr)
				}
			}
		}
		cPortKey := nat.Port(fmt.Sprintf("%d/tcp", cPort))
		hc.PortBindings = nat.PortMap{
			cPortKey: []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: fmt.Sprint(hostP)}},
		}
		cc.ExposedPorts = nat.PortSet{cPortKey: struct{}{}}
		log.Printf("Container %s: binding host port %d → container port %d/tcp", containerName, hostP, cPort)
	}

	nc := dm.tenantEndpoints(config.UUID, config.OwnerID, netID)

	dm.cli.ContainerRemove(dm.ctx, containerName, container.RemoveOptions{Force: true})

	resp, err := dm.cli.ContainerCreate(dm.ctx, cc, hc, nc, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("mc container create error: %v", err)
	}

	if err := dm.cli.ContainerStart(dm.ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("mc container start error: %v", err)
	}

	return containerName, nil
}

// RestartContainer inspects the existing container to capture its full config,
// removes the old container, and creates + starts a fresh one with identical settings.
// This is more reliable than ContainerStart on an exited container.
func (dm *DockerManager) RestartContainer(uuid string) error {
	containerName := fmt.Sprintf("mc_%s", uuid)

	info, err := dm.cli.ContainerInspect(dm.ctx, containerName)
	if err != nil {
		return fmt.Errorf("cannot inspect %s for restart: %v", containerName, err)
	}

	// Build a ServerConfig from the inspected container
	config := ServerConfig{UUID: uuid}
	config.Docker.Image = info.Config.Image
	if info.Config.WorkingDir != "" && strings.HasPrefix(info.Config.WorkingDir, "/data/") {
		config.ActiveSubServer = strings.TrimPrefix(info.Config.WorkingDir, "/data/")
	}
	// Restore resource limits
	config.Docker.RAM = int(info.HostConfig.Memory/(1024*1024)) - 512 // subtract OOM padding
	if config.Docker.RAM < 0 {
		config.Docker.RAM = 0
	}
	config.Docker.CPULimit = float64(info.HostConfig.NanoCPUs) / 1e9
	config.Docker.CpusetCpus = info.HostConfig.CpusetCpus

	// Preserve the previous bind mounts verbatim so the world data on disk
	// can't be lost to a storage-path resolution change between create and
	// restart (e.g. storageMgr reseeded with a different host path cache).
	if len(info.HostConfig.Binds) > 0 {
		config.ExistingBinds = append([]string{}, info.HostConfig.Binds...)
	}

	// Rebuild the start command from disk so the correct launch form (jar vs
	// argfile) is always used. Extract any extra JVM flags from the old
	// command so Aikar / admin flags survive the restart.
	if config.ActiveSubServer != "" {
		subServerDir := filepath.Join(dm.resolveLocalServerPath(uuid), config.ActiveSubServer)
		oldCmd := ""
		if len(info.Config.Cmd) > 0 {
			oldCmd = strings.Join(info.Config.Cmd, " ")
		}
		extraFlags := extractJvmFlagsFromCommand(oldCmd)
		if startCmd, err := buildStartCommand(subServerDir, config.Docker.RAM, extraFlags, config.Docker.Image); err == nil {
			config.Docker.Command = startCmd
		} else {
			// Fall back to the stored command so the container still starts.
			log.Printf("RestartContainer %s: buildStartCommand failed (%v), reusing stored command", uuid, err)
			config.Docker.Command = oldCmd
		}
	} else if len(info.Config.Cmd) > 0 {
		config.Docker.Command = strings.Join(info.Config.Cmd, " ")
	}

	log.Printf("RestartContainer %s: image=%s cmd=%s sub=%s ram=%dMB cpu=%.1f binds=%v",
		uuid, config.Docker.Image, config.Docker.Command, config.ActiveSubServer, config.Docker.RAM, config.Docker.CPULimit, config.ExistingBinds)

	return dm.RecreateWithCommand(config)
}

// UpdateResources stops the container, then recreates it with new RAM/CPU/Port settings,
// preserving the existing image, command, and bind mounts from the running/last container inspect.
func (dm *DockerManager) UpdateResources(config ServerConfig) error {
	containerName := fmt.Sprintf("mc_%s", config.UUID)

	// Inspect existing container to preserve image + active sub-server + binds.
	info, err := dm.cli.ContainerInspect(dm.ctx, containerName)
	if err == nil {
		if config.Docker.Image == "" {
			config.Docker.Image = info.Config.Image
		}
		if config.ActiveSubServer == "" && info.Config.WorkingDir != "" &&
			strings.HasPrefix(info.Config.WorkingDir, "/data/") {
			config.ActiveSubServer = strings.TrimPrefix(info.Config.WorkingDir, "/data/")
		}
		if len(info.HostConfig.Binds) > 0 {
			config.ExistingBinds = append([]string{}, info.HostConfig.Binds...)
		}

		// Rebuild start command from disk with the new RAM value so memory
		// flags stay in sync. Fall back to the stored command on error.
		if config.ActiveSubServer != "" {
			subServerDir := filepath.Join(dm.resolveLocalServerPath(config.UUID), config.ActiveSubServer)
			oldCmd := ""
			if len(info.Config.Cmd) > 0 {
				oldCmd = strings.Join(info.Config.Cmd, " ")
			}
			extraFlags := extractJvmFlagsFromCommand(oldCmd)
			if startCmd, buildErr := buildStartCommand(subServerDir, config.Docker.RAM, extraFlags, config.Docker.Image); buildErr == nil {
				config.Docker.Command = startCmd
			} else {
				log.Printf("UpdateResources %s: buildStartCommand failed (%v), reusing stored command", config.UUID, buildErr)
				if config.Docker.Command == "" {
					config.Docker.Command = oldCmd
				}
			}
		} else if config.Docker.Command == "" && len(info.Config.Cmd) > 0 {
			config.Docker.Command = strings.Join(info.Config.Cmd, " ")
		}
	}

	return dm.RecreateWithCommand(config)
}

// PullContainerImage inspects a container to get its image, then pulls the latest version.
func (dm *DockerManager) PullContainerImage(uuid string) {
	containerName := fmt.Sprintf("mc_%s", uuid)
	info, err := dm.cli.ContainerInspect(dm.ctx, containerName)
	if err != nil {
		log.Printf("Cannot inspect %s for image pull: %v", containerName, err)
		return
	}
	dm.pullImage(info.Config.Image)
}

func (dm *DockerManager) pullImage(image string) {
	log.Printf("Pulling Image: %s ...", image)
	reader, err := dm.cli.ImagePull(dm.ctx, image, dockerimage.PullOptions{})
	if err != nil {
		return
	}
	io.Copy(io.Discard, reader)
	reader.Close()
}

// ContainerStats holds a single snapshot of container resource usage.
type ContainerStats struct {
	CPUPercent float64 // percentage of one core (e.g. 145.2 = 1.45 cores)
	MemUsedMB  int64
	MemLimitMB int64
}

// PrevCPUStats stores previous CPU counters for accurate delta calculation.
type PrevCPUStats struct {
	TotalUsage  uint64
	SystemUsage uint64
}

// GetContainerStats reads a one-shot stats snapshot from the Docker API.
// Uses caller-provided previous CPU counters for accurate delta calculation,
// since ContainerStatsOneShot may return empty/stale PreCPUStats.
func (dm *DockerManager) GetContainerStats(containerName string, prev *PrevCPUStats) (*ContainerStats, *PrevCPUStats, error) {
	resp, err := dm.cli.ContainerStatsOneShot(dm.ctx, containerName)
	if err != nil {
		return nil, prev, err
	}
	defer resp.Body.Close()

	var stats container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, prev, fmt.Errorf("stats decode error: %v", err)
	}

	// Save current CPU counters for next call
	currentTotal := stats.CPUStats.CPUUsage.TotalUsage
	currentSystem := stats.CPUStats.SystemUsage
	newPrev := &PrevCPUStats{TotalUsage: currentTotal, SystemUsage: currentSystem}

	// CPU calculation using our own delta (not Docker's PreCPUStats)
	numCPUs := float64(stats.CPUStats.OnlineCPUs)
	if numCPUs == 0 {
		numCPUs = float64(len(stats.CPUStats.CPUUsage.PercpuUsage))
	}

	var cpuPercent float64
	if prev != nil {
		cpuDelta := float64(currentTotal - prev.TotalUsage)
		systemDelta := float64(currentSystem - prev.SystemUsage)
		if systemDelta > 0 && cpuDelta >= 0 {
			cpuPercent = (cpuDelta / systemDelta) * numCPUs * 100.0
		}
	}

	// Memory: subtract filesystem cache (like docker stats CLI).
	// Different kernel/cgroup versions expose cache under different keys.
	memUsage := stats.MemoryStats.Usage
	cacheSubtracted := false
	if v, ok := stats.MemoryStats.Stats["inactive_file"]; ok && v > 0 {
		memUsage -= v // cgroup v2
		cacheSubtracted = true
	}
	if !cacheSubtracted {
		if v, ok := stats.MemoryStats.Stats["total_inactive_file"]; ok && v > 0 {
			memUsage -= v // cgroup v1
			cacheSubtracted = true
		}
	}
	if !cacheSubtracted {
		if v, ok := stats.MemoryStats.Stats["file"]; ok && v > 0 {
			memUsage -= v // cgroup v2 fallback
			cacheSubtracted = true
		}
	}
	if !cacheSubtracted {
		if v, ok := stats.MemoryStats.Stats["cache"]; ok && v > 0 {
			memUsage -= v // legacy Docker API
		}
	}
	// memUsage is unsigned: a cache subtraction larger than the raw usage wraps
	// to a huge value rather than going negative, so detect that underflow by
	// comparing against the original usage and clamp to 0 (like docker stats).
	if memUsage > stats.MemoryStats.Usage {
		memUsage = 0
	}

	return &ContainerStats{
		CPUPercent: cpuPercent,
		MemUsedMB:  int64(memUsage) / (1024 * 1024),
		MemLimitMB: int64(stats.MemoryStats.Limit) / (1024 * 1024),
	}, newPrev, nil
}

// MCContainer holds info about a running Minecraft container.
type MCContainer struct {
	UUID          string
	ContainerName string
}

// ListRunningMCContainers returns all running containers with the mc_ prefix.
func (dm *DockerManager) ListRunningMCContainers() ([]MCContainer, error) {
	containers, err := dm.cli.ContainerList(dm.ctx, container.ListOptions{})
	if err != nil {
		return nil, err
	}

	var result []MCContainer
	for _, c := range containers {
		for _, name := range c.Names {
			clean := strings.TrimPrefix(name, "/")
			if strings.HasPrefix(clean, "mc_") {
				uuid := strings.TrimPrefix(clean, "mc_")
				result = append(result, MCContainer{UUID: uuid, ContainerName: clean})
				break
			}
		}
	}
	return result, nil
}

// MCContainerInfo holds info about a Minecraft container including its state.
type MCContainerInfo struct {
	UUID          string
	ContainerName string
	State         string // "running", "exited", "created", etc.
}

// ListAllMCContainers returns all mc_ containers (running + stopped/exited).
func (dm *DockerManager) ListAllMCContainers() ([]MCContainerInfo, error) {
	containers, err := dm.cli.ContainerList(dm.ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	var result []MCContainerInfo
	for _, c := range containers {
		for _, name := range c.Names {
			clean := strings.TrimPrefix(name, "/")
			if strings.HasPrefix(clean, "mc_") {
				uuid := strings.TrimPrefix(clean, "mc_")
				result = append(result, MCContainerInfo{
					UUID:          uuid,
					ContainerName: clean,
					State:         c.State,
				})
				break
			}
		}
	}
	return result, nil
}

// CountLinkContainers returns the number of running Link containers on this host.
// It identifies them by checking if the image name contains "dylaris-link".
func (dm *DockerManager) CountLinkContainers() int {
	containers, err := dm.cli.ContainerList(dm.ctx, container.ListOptions{})
	if err != nil {
		return 0
	}
	count := 0
	for _, c := range containers {
		if strings.Contains(c.Image, "dylaris-link") {
			count++
		}
	}
	return count
}

// ReconcileRedisEnv checks all running MC containers and restarts any whose
// REDIS_ADDR env var doesn't match the current mcRedisAddr. This handles the
// case where Node is redeployed with a new SIDECAR_REDIS_ADDR — running
// containers still have the old value baked in from creation time.
func (dm *DockerManager) ReconcileRedisEnv() {
	running, err := dm.ListRunningMCContainers()
	if err != nil {
		log.Printf("ReconcileRedisEnv: failed to list containers: %v", err)
		return
	}

	for _, mc := range running {
		info, err := dm.cli.ContainerInspect(dm.ctx, mc.ContainerName)
		if err != nil {
			continue
		}

		var currentAddr string
		for _, e := range info.Config.Env {
			if strings.HasPrefix(e, "REDIS_ADDR=") {
				currentAddr = strings.TrimPrefix(e, "REDIS_ADDR=")
				break
			}
		}

		if currentAddr != mcRedisAddr {
			log.Printf("ReconcileRedisEnv: %s has REDIS_ADDR=%s, expected %s — restarting",
				mc.ContainerName, currentAddr, mcRedisAddr)
			if err := dm.RestartContainer(mc.UUID); err != nil {
				log.Printf("ReconcileRedisEnv: failed to restart %s: %v", mc.ContainerName, err)
			}
		}
	}
}
