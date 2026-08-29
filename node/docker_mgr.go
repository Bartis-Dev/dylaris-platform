package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

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
	selfHostNet   bool                  // true when this node's own container uses --network host

	// Bridge gateway per Docker network id: where a container on it reaches the
	// host, and therefore the warp proxy. Only consulted in warp-proxy mode, and
	// cached because every container create asks for it.
	netGWMu sync.Mutex
	netGW   map[string]string
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

	dm.selfHostNet = dm.detectSelfHostNet()
	if dm.selfHostNet {
		log.Printf("node runs with --network host; MC servers use the local dylaris_net and are reached via the Docker daemon, not Docker DNS")
	}

	return dm, nil
}

// detectSelfHostNet reports whether this node's own container uses host
// networking. A host-net node cannot join a bridge/overlay network, which
// changes how MC servers are networked (D1/D3) and reached (D2) - see the BYON
// host-net spec. Fails safe to false (behave like a normal node).
func (dm *DockerManager) detectSelfHostNet() bool {
	ref := selfContainerRef()
	if ref == "" {
		return false
	}
	info, err := dm.cli.ContainerInspect(dm.ctx, ref)
	if err != nil {
		return false
	}
	return parseHostNetFromMode(string(info.HostConfig.NetworkMode))
}

// parseHostNetFromMode reports whether a Docker NetworkMode string means host
// networking. Only the literal "host" qualifies; "container:<id>" shares another
// container's netns and is not host-net for our purposes.
func parseHostNetFromMode(mode string) bool {
	return mode == "host"
}

// buildHostPathCache resolves host paths for all storage paths by inspecting container mounts.
func (dm *DockerManager) buildHostPathCache() {
	if dm.storageMgr == nil {
		return
	}

	ref := selfContainerRef()
	if ref == "" {
		return
	}

	info, err := dm.cli.ContainerInspect(dm.ctx, ref)
	if err != nil {
		log.Printf("Cannot inspect own container for host path resolution (ref=%s): %v", ref, err)
		return
	}

	mounts := make([]hostMount, 0, len(info.Mounts))
	for _, m := range info.Mounts {
		mounts = append(mounts, hostMount{Destination: m.Destination, Source: m.Source})
	}

	for _, p := range dm.storageMgr.Paths() {
		hostPath, ok := resolveHostPath(p, mounts)
		if !ok {
			// Assume local = host (non-containerized)
			dm.hostPathCache[p] = p
			continue
		}
		dm.hostPathCache[p] = hostPath
		log.Printf("storage: host path %s → %s", p, hostPath)
	}
}

// ResolveMCContainerIP returns the private IPv4 of mc_<uuid> straight from the
// Docker daemon. The daemon is authoritative (no DNS), so this works from a
// host-net node whose resolver is the host's, and it removes the wildcard-DNS
// answer the old net.LookupIP path had to defend against. Prefers the
// dylaris_net / tenant endpoint; errors when the container is absent or has no
// IP (stopped). The caller still runs the private-IP guard on the result.
func (dm *DockerManager) ResolveMCContainerIP(uuid string) (net.IP, error) {
	name := "mc_" + uuid
	info, err := dm.cli.ContainerInspect(dm.ctx, name)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", name, err)
	}
	if info.NetworkSettings == nil {
		return nil, fmt.Errorf("%s has no network settings (is it running?)", name)
	}
	if ip := pickContainerIP(info.NetworkSettings.Networks); ip != nil {
		return ip, nil
	}
	return nil, fmt.Errorf("%s has no network IP (is it running?)", name)
}

// pickContainerIP selects the IPv4 to reach a container for a control-plane dial:
// prefer a dylaris_net / *_dylaris_net / tenant endpoint, else the first endpoint
// that carries an IP. Returns nil when no endpoint has an IPv4 (stopped). The
// caller still runs guardPrivateAddr on the result, so a non-private pick is
// refused, not dialled.
func pickContainerIP(networks map[string]*network.EndpointSettings) net.IP {
	for netName, ep := range networks {
		if ep == nil || ep.IPAddress == "" {
			continue
		}
		if isGlobalNetName(netName) || strings.HasPrefix(netName, "dylaris_tenant_") {
			if ip := net.ParseIP(ep.IPAddress); ip != nil {
				return ip
			}
		}
	}
	for _, ep := range networks {
		if ep != nil && ep.IPAddress != "" {
			if ip := net.ParseIP(ep.IPAddress); ip != nil {
				return ip
			}
		}
	}
	return nil
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

// selfContainerRef returns a reference (container id or hostname) that
// ContainerInspect can resolve to THIS node's own container. Under `--network
// host` - which every BYON node uses, and which Docker Desktop imposes -
// os.Hostname() returns the HOST's name (e.g. "docker-desktop"), not the
// container id, so inspecting by it fails. The real id still appears in
// /proc/self/mountinfo, where Docker bind-mounts /etc/hostname, /etc/hosts and
// /etc/resolv.conf from /var/lib/docker/containers/<id>/. We fall back to
// os.Hostname() for non-host-net containers (e.g. the cloud Swarm nodes) and
// return "" outside any container (bare metal), where the caller's local==host
// fallback is the correct answer.
func selfContainerRef() string {
	if f, err := os.Open("/proc/self/mountinfo"); err == nil {
		defer f.Close()
		if id := parseContainerIDFromMountinfo(f); id != "" {
			return id
		}
	}
	host, _ := os.Hostname()
	return host
}

// parseContainerIDFromMountinfo extracts the 64-hex container id from the
// "/containers/<id>/" path Docker bind-mounts for /etc/hostname et al. Returns ""
// when no such id is present (bare metal, or a runtime without that layout).
func parseContainerIDFromMountinfo(r io.Reader) string {
	const marker = "/containers/"
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		_, after, found := strings.Cut(scanner.Text(), marker)
		if !found {
			continue
		}
		if id, _, ok := strings.Cut(after, "/"); ok && isHex64(id) {
			return id
		}
	}
	return ""
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// resolveHostDataPath inspects our own container to find the host filesystem path
// of the dylaris_data volume mount. MC containers spawned via Docker API need the
// HOST path for bind mounts, not the container-internal path.
func (dm *DockerManager) resolveHostDataPath() string {
	ref := selfContainerRef()
	if ref == "" {
		return ""
	}

	info, err := dm.cli.ContainerInspect(dm.ctx, ref)
	if err != nil {
		log.Printf("Could not inspect own container (ref=%s): %v", ref, err)
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

// networkGateway returns the IPv4 gateway of a Docker network - the address a
// container on it uses to reach the host. Cached: it cannot change for the life
// of the network, and every container create asks.
func (dm *DockerManager) networkGateway(netID string) (string, error) {
	dm.netGWMu.Lock()
	defer dm.netGWMu.Unlock()
	if gw, ok := dm.netGW[netID]; ok {
		return gw, nil
	}
	insp, err := dm.cli.NetworkInspect(dm.ctx, netID, network.InspectOptions{})
	if err != nil {
		return "", fmt.Errorf("inspect network %s: %w", netID, err)
	}
	for _, c := range insp.IPAM.Config {
		ip := net.ParseIP(c.Gateway)
		if ip == nil {
			continue
		}
		// Private v4 only, matching what warp is willing to bind. A public
		// gateway on a local bridge is a misconfiguration, and handing one to a
		// container would name an address warp is not listening on.
		v4 := ip.To4()
		if v4 == nil || !v4.IsPrivate() {
			continue
		}
		if dm.netGW == nil {
			dm.netGW = map[string]string{}
		}
		dm.netGW[netID] = v4.String()
		return v4.String(), nil
	}
	return "", fmt.Errorf("network %s has no private IPv4 gateway", netID)
}

// sidecarRedisAddr is the Redis address baked into a container joining netID.
//
// Usually the configured value. In warp-proxy mode there is no single answer:
// the container reaches warp through the gateway of ITS network, so the address
// is resolved per network here rather than once at startup. Failing is correct
// when it cannot be resolved - a container started with an address that cannot
// work looks healthy and ships no console output at all.
func (dm *DockerManager) sidecarRedisAddr(netID string) (string, error) {
	if mcRedisAddr != "" {
		return mcRedisAddr, nil
	}
	gw, err := dm.networkGateway(netID)
	if err != nil {
		return "", fmt.Errorf("redis address for a container on %s: %w", netID, err)
	}
	return resolveSidecarRedisAddr("", redisAddr, redisViaWarpProxy, gw), nil
}

// buildRedisEnv returns the env-slice that the log-shipper inside the container
// needs to connect to Redis and identify the server stream.
//
// sidecarAddr is resolved for the CONTAINER's network by the caller and is
// deliberately a parameter, not the node's own package-global redisAddr: those
// two differ on the warp proxy, where the node's is loopback and a container's
// is the bridge gateway.
func buildRedisEnv(uuid, subServer, sidecarAddr string) []string {
	// Redis ACL is mandatory: every MC container gets a SHIPPER credential,
	// derived deterministically here and provisioned by Core to match.
	//
	// Scoped PER SERVER. It used to be one user per node, granted
	// ~dylaris:server:<u>:* for every server on the machine with +@read +@write.
	// On a shared platform node those are other tenants' servers, and
	// dylaris:server:<u>:input is a stdin bridge into their JVM (log-shipper's
	// forwardInput BLPops it straight into the process) - so the credential baked
	// into one tenant's container could read AND write a neighbour's console.
	//
	// The uuid is part of the PASSWORD derivation as well as the username, so two
	// containers on this node cannot compute each other's credential either.
	user := aclShipperUsername(nodeID, uuid)
	pass := aclShipperPassword(getNodeSecret(), nodeID, uuid)
	env := []string{
		fmt.Sprintf("REDIS_ADDR=%s", sidecarAddr),
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

// linkPrefersPublicEdge decides which of an edge's two addresses the
// node-managed link tries first.
//
// NODE_EXTERNAL and NODE_TAGS are node-local env on the customer's own machine,
// so a BYON owner who clears them gets the private address preferred - and warp
// masquerades, so that address ANSWERS and every player's bytes ride the
// WireGuard tunnel. Holding no CLUSTER_SECRET is what makes a node BYON, and
// that is not something a machine can claim its way out of: an in-cluster node
// needs the secret for the cluster proof and the gRPC TLS pin, so clearing it
// to look in-cluster only stops the node from starting.
//
// This is a preference, not the control. The warp leader's spoke allowlist is
// what ENFORCES that player traffic stays off the overlay; deciding it here
// only saves the link a dial timeout on the address it must not use anyway.
func linkPrefersPublicEdge() bool {
	return nodeExternal || clusterSecret == ""
}

// buildLinkEnv builds the env for a node-managed Link sidecar. Link authenticates
// to Redis with its own per-node ACL user (derived from nodeSecret, provisioned by
// Core), and presents the Core-delivered tunnel token + discovery proof. Redis addr
// uses the SIDECAR (mc) address for the same non-Swarm-DNS reason as MC containers.
func buildLinkEnv(nodeID, linkSecret, linkDiscoveryProof, sidecarAddr string) []string {
	// Redis ACL is mandatory: Link always authenticates with its own per-node ACL
	// user (nodeSecret guaranteed non-nil after the startup bootstrap).
	user := aclLinkUsername(nodeID)
	pass := aclLinkPassword(getNodeSecret(), nodeID)
	return []string{
		fmt.Sprintf("NODE_ID=%s", nodeID),
		fmt.Sprintf("LINK_SECRET=%s", linkSecret),
		fmt.Sprintf("LINK_DISCOVERY_PROOF=%s", linkDiscoveryProof),
		fmt.Sprintf("REDIS_ADDR=%s", sidecarAddr),
		fmt.Sprintf("REDIS_USER=%s", user),
		fmt.Sprintf("REDIS_PASS=%s", pass),
		fmt.Sprintf("REDIS_DB=%s", mcRedisDB),
		// Which of an edge's two addresses the link tries first. On an external
		// machine the private one is reachable too - warp routes the overlay -
		// so preferring it succeeds and puts every player's bytes through the
		// WireGuard tunnel, sharing one leader with this node's own Redis
		// traffic and Beam uploads.
		fmt.Sprintf("LINK_EXTERNAL=%t", linkPrefersPublicEdge()),
	}
}

// EnsureLinkContainer (re)creates the node-managed Link sidecar on dylaris_net.
// Idempotent recreate: always force-removes any stale same-name container first.
func (dm *DockerManager) EnsureLinkContainer(image, nodeID, linkSecret, linkDiscoveryProof string) error {
	dm.pullImage(image)
	netID, netName, err := dm.ensureGlobalNetwork()
	if err != nil {
		return err
	}
	sidecarAddr, err := dm.sidecarRedisAddr(netID)
	if err != nil {
		return err
	}
	cc := &container.Config{
		Image:    image,
		Hostname: linkContainerName,
		Env:      buildLinkEnv(nodeID, linkSecret, linkDiscoveryProof, sidecarAddr),
	}
	// Unlike MC containers, the Link sidecar has no node-side liveness reconciler,
	// so Docker's own restart policy is what keeps it alive across an internal crash.
	// StopLinkContainer still does an explicit ContainerStop+ContainerRemove, which
	// tears it down regardless of restart policy whenever the node wants it gone.
	hc := &container.HostConfig{RestartPolicy: container.RestartPolicy{Name: "unless-stopped"}}
	// Keyed by the resolved name, not the literal: see ensureGlobalNetwork.
	nc := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			netName: {NetworkID: netID},
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
	// Isolated servers live on their owner's tenant net, and the route points at
	// mc_<uuid> by NAME - so a Link that is not on those networks cannot resolve
	// a single one of them. No-op when isolation is off.
	if dm.tenant != nil {
		dm.tenant.AttachLinkToAll()
	}
	return nil
}

// LinkImageStatus refreshes the configured Link image reference and reports the
// image the running container was created from alongside the image that
// reference now resolves to. Either may be empty (no container, or the pull
// failed and nothing is cached locally); a caller must treat "" as unknown and
// not as drift, or a registry hiccup would recreate the Link on every tick.
//
// This mirrors what the edge already does for its splice sidecar
// (gateway/edge/internal/sidecar/manager.go): pull, compare image IDs, recreate
// on a real change. Comparing IDs rather than the tag string is the whole point
// - a moving tag is invisible to a string comparison.
func (dm *DockerManager) LinkImageStatus(image string) (running, available string) {
	if c, err := dm.cli.ContainerInspect(dm.ctx, linkContainerName); err == nil {
		running = c.Image
	}
	dm.pullImage(image)
	if ins, _, err := dm.cli.ImageInspectWithRaw(dm.ctx, image); err == nil {
		available = ins.ID
	}
	return running, available
}

// LinkEndpoint returns host:port for the Link's management server, resolved from
// the container's own network settings.
//
// Deliberately not the container NAME: Docker DNS only resolves it for
// containers attached to the same user-defined network, and a host-networked
// node (every BYON node, and anything on Docker Desktop) is not. Asking the
// Docker API works in every networking mode this node runs in.
func (dm *DockerManager) LinkEndpoint() string {
	c, err := dm.cli.ContainerInspect(dm.ctx, linkContainerName)
	if err != nil || c.NetworkSettings == nil {
		return ""
	}
	for _, ep := range c.NetworkSettings.Networks {
		if ep.IPAddress != "" {
			return fmt.Sprintf("%s:%d", ep.IPAddress, linkMgmtPort)
		}
	}
	return ""
}

// StopLinkContainer stops + removes the node-managed Link sidecar (best-effort).
func (dm *DockerManager) StopLinkContainer() {
	timeout := 15
	dm.cli.ContainerStop(dm.ctx, linkContainerName, container.StopOptions{Timeout: &timeout})
	dm.cli.ContainerRemove(dm.ctx, linkContainerName, container.RemoveOptions{Force: true})
}

// ensureGlobalNetwork resolves the shared server network and returns BOTH its id
// and its real name.
//
// The name matters as much as the id. Lookup deliberately accepts a
// compose/stack prefix ("platform_dylaris_net"), so on most deployments the
// network is NOT called "dylaris_net" - and an endpoint has to be attached under
// the name Docker actually knows. Returning only the id let callers key their
// EndpointsConfig on the literal "dylaris_net", which on a prefixed deployment
// names nothing: the container is then created with no endpoint at all, on the
// default bridge, and Docker reports success.
//
// The result is a server that looks perfectly healthy and cannot reach anything.
// Observed live: a container recreated this way sat "Up" for nine minutes with
// no networks, its log-shipper retrying "Redis not reachable ... network is
// unreachable" every 32 seconds, never starting Java at all.
func (dm *DockerManager) ensureGlobalNetwork() (id string, name string, err error) {
	const netName = "dylaris_net"
	nets, lerr := dm.cli.NetworkList(dm.ctx, network.ListOptions{})
	if lerr != nil {
		return "", "", fmt.Errorf("network list error: %v", lerr)
	}
	for _, n := range nets {
		if isGlobalNetName(n.Name) {
			log.Printf("Found overlay network %q (ID: %s)", n.Name, n.ID[:12])
			return n.ID, n.Name, nil
		}
	}

	// Not found. Only a host-net node (a standalone BYON node with no Swarm overlay
	// to supply dylaris_net) may create a LOCAL bridge here. On any other node the
	// overlay is expected and its absence must stay a loud error: silently making a
	// local bridge would let MC containers attach to it, pass the len(Networks)!=0
	// post-check in startMinecraftContainer, and run "Up but unreachable" instead of
	// failing where the cause is visible.
	if !dm.selfHostNet {
		return "", "", fmt.Errorf("overlay network %q not found — ensure it exists in the Swarm stack", netName)
	}

	var dockerSubnets []*net.IPNet
	for _, n := range nets {
		for _, c := range n.IPAM.Config {
			if _, ipn, perr := net.ParseCIDR(c.Subnet); perr == nil {
				dockerSubnets = append(dockerSubnets, ipn)
			}
		}
	}
	route, _ := os.ReadFile("/proc/net/route")
	used := reservedSubnets(dockerSubnets, string(route), dm.selfHostNet)
	opts := network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{"dylaris.role": "global-network"},
	}
	if free, ferr := nextFreeSubnet(used, 24); ferr == nil {
		opts.IPAM = &network.IPAM{Config: []network.IPAMConfig{{Subnet: free.String()}}}
	} else {
		log.Printf("local dylaris_net: no subnet avoids all reserved ranges (%v); letting Docker auto-assign", ferr)
	}
	res, cErr := dm.cli.NetworkCreate(dm.ctx, netName, opts)
	if cErr != nil {
		// A concurrent first-boot create (the 8 command consumers plus the link
		// reconciler can all reach here) loses to the winner with a duplicate-name
		// error. Adopt the winner's network rather than fail the command.
		if id2, name2, found2 := dm.findExistingGlobalNetwork(); found2 {
			return id2, name2, nil
		}
		return "", "", fmt.Errorf("create local network %q: %w", netName, cErr)
	}
	subnetLog := "auto"
	if opts.IPAM != nil {
		subnetLog = opts.IPAM.Config[0].Subnet
	}
	log.Printf("Created local network %q (ID: %s, subnet %s)", netName, res.ID[:12], subnetLog)
	return res.ID, netName, nil
}

// isGlobalNetName matches the shared server network by exact name or a compose/
// stack prefix ("platform_dylaris_net").
func isGlobalNetName(name string) bool {
	return name == "dylaris_net" || strings.HasSuffix(name, "_dylaris_net")
}

// findExistingGlobalNetwork re-lists networks and returns the shared server
// network if present. Used to adopt a network a concurrent creator just made.
func (dm *DockerManager) findExistingGlobalNetwork() (id, name string, found bool) {
	nets, err := dm.cli.NetworkList(dm.ctx, network.ListOptions{})
	if err != nil {
		return "", "", false
	}
	for _, n := range nets {
		if isGlobalNetName(n.Name) {
			return n.ID, n.Name, true
		}
	}
	return "", "", false
}

// tenantEndpoints builds the NetworkingConfig for a server container. Isolation
// off (dm.tenant == nil) or any resolution/allocation error falls back to
// dylaris_net so a server is never left unstartable by the isolation layer. The
// enlarge-on-overflow retry is added in a later task.
func (dm *DockerManager) tenantEndpoints(serverUUID, ownerID, globalNetID, globalNetName string) *network.NetworkingConfig {
	// Keyed by the network's REAL name, which carries the compose/stack prefix
	// on most deployments. Keying it "dylaris_net" there names no network and
	// Docker creates the container with no endpoint at all - see
	// ensureGlobalNetwork.
	if globalNetName == "" {
		globalNetName = "dylaris_net"
	}
	fallback := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			globalNetName: {NetworkID: globalNetID},
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

	netID, netName, err := dm.ensureGlobalNetwork()
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

	// Resolved against the network the container joins. In warp-proxy mode that
	// is always dylaris_net: the proxy leaves mcRedisAddr empty, which turns
	// tenant isolation off, so there is no second network in play.
	sidecarAddr, err := dm.sidecarRedisAddr(netID)
	if err != nil {
		return err
	}

	cc := &container.Config{
		Image:      config.Docker.Image,
		WorkingDir: "/data",
		Hostname:   containerName,
		Env:        buildRedisEnv(config.UUID, "", sidecarAddr),
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

	nc := dm.tenantEndpoints(config.UUID, config.OwnerID, netID, netName)

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
	if pl := getPidsLimit(); pl > 0 {
		hc.Resources.PidsLimit = &pl
	}
}

// applyIOWeight sets the cgroup blkio relative weight (10–1000) on the container
// when configured via dylaris:placement:io_weight. 0 = unset (no weight). This is
// a relative fair-share between containers, not a hard cap, and only takes effect
// with an I/O scheduler that honours blkio weight (BFQ/CFQ); it is a harmless
// no-op otherwise. Applied at both container-build sites alongside applyPidsLimit.
func applyIOWeight(hc *container.HostConfig) {
	if io := getIOWeight(); io >= 10 && io <= 1000 {
		hc.Resources.BlkioWeight = io
	}
}

// RecreateWithCommand stops + removes + creates a container with a new sub-server command.
func (dm *DockerManager) RecreateWithCommand(config ServerConfig) error {
	log.Printf("Recreating container %s with sub-server: %s", config.UUID, config.ActiveSubServer)

	containerName := fmt.Sprintf("mc_%s", config.UUID)
	timeout := 15
	dm.cli.ContainerStop(dm.ctx, containerName, container.StopOptions{Timeout: &timeout})
	dm.cli.ContainerRemove(dm.ctx, containerName, container.RemoveOptions{Force: true})

	netID, netName, err := dm.ensureGlobalNetwork()
	if err != nil {
		return err
	}

	_, err = dm.startMinecraftContainer(config, netID, netName)
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
		// Released whatever the removal reported. The ledger is keyed by SERVER,
		// not by container, and this action's only caller goes on to delete the
		// server's directory outright - so by the time it returns there is no
		// server left to own a port either way.
		//
		// Gating it on err == nil stranded the entry in exactly the case that
		// needs it most: "No such container", from a retried delete or a
		// container someone removed by hand (which reconcileDeletedContainers
		// exists because it happens). Nothing else ever reclaims a port -
		// AdoptExistingBindings only adds - and the default range is about a
		// hundred wide, so each one is permanent.
		if dm.portMgr != nil {
			dm.portMgr.ReleasePort(uuid)
		}
		return err
	}

	return nil
}

func (dm *DockerManager) startMinecraftContainer(config ServerConfig, netID, netName string) (string, error) {
	// Docker accepts an empty image and builds a container with nothing in it,
	// so the failure surfaces much later as `exec: "java": executable file not
	// found in $PATH` - after the previous container is already gone. Refuse it
	// here like RunInstallerContainer does, so a bad command cannot brick a
	// running server.
	if config.Docker.Image == "" {
		return "", fmt.Errorf("server image is required")
	}
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

	sidecarAddr, err := dm.sidecarRedisAddr(netID)
	if err != nil {
		return "", err
	}

	cc := &container.Config{
		Image:      config.Docker.Image,
		Cmd:        cmdParts,
		WorkingDir: fmt.Sprintf("/data/%s", config.ActiveSubServer),
		Hostname:   containerName,
		Env:        buildRedisEnv(config.UUID, config.ActiveSubServer, sidecarAddr),
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
	if getRoutingMode() != "gateway" && dm.portMgr != nil {
		cPort := config.Docker.ContainerPort
		if cPort == 0 {
			cPort = getContainerPort()
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

	nc := dm.tenantEndpoints(config.UUID, config.OwnerID, netID, netName)

	dm.cli.ContainerRemove(dm.ctx, containerName, container.RemoveOptions{Force: true})

	resp, err := dm.cli.ContainerCreate(dm.ctx, cc, hc, nc, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("mc container create error: %v", err)
	}

	if err := dm.cli.ContainerStart(dm.ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("mc container start error: %v", err)
	}

	// Confirm the container actually landed on a network. Docker accepts an
	// EndpointsConfig naming a network that does not exist and creates the
	// container anyway, on the default bridge with no endpoint - a container
	// that starts, stays Up, and can reach nothing. From the outside that is
	// indistinguishable from a healthy server: the reconciler sees it running,
	// the stats collector sees it running, the panel says online, and the
	// log-shipper inside sits retrying "Redis not reachable" until someone looks.
	//
	// Reported rather than repaired: this is a deployment mismatch (the shared
	// network is missing or renamed), and quietly re-attaching would hide it. A
	// hard error surfaces at the point of creation, which is the only place the
	// cause is still visible.
	if info, ierr := dm.cli.ContainerInspect(dm.ctx, resp.ID); ierr == nil && len(info.NetworkSettings.Networks) == 0 {
		dm.cli.ContainerRemove(dm.ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("mc container %s was created with no network attached (wanted %q, id %s) — "+
			"the shared network is missing or named differently on this host; refusing to leave a server "+
			"that is Up but unreachable", containerName, netName, netID)
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

// ExistingHostPortBindings maps serverUUID -> published host port for every mc_
// container on this host, running or not. A stopped container still owns its
// port: it gets it back the moment the reconciler starts it again.
//
// Read from HostConfig rather than the container list's Ports field, because
// the latter is empty for anything not currently running.
func (dm *DockerManager) ExistingHostPortBindings() map[string]int {
	out := map[string]int{}
	containers, err := dm.ListAllMCContainers()
	if err != nil {
		log.Printf("ExistingHostPortBindings: cannot list containers: %v", err)
		return out
	}
	for _, c := range containers {
		info, err := dm.cli.ContainerInspect(dm.ctx, c.ContainerName)
		if err != nil || info.HostConfig == nil {
			continue
		}
		for _, bindings := range info.HostConfig.PortBindings {
			for _, b := range bindings {
				port, err := strconv.Atoi(b.HostPort)
				if err != nil || port <= 0 {
					continue
				}
				out[c.UUID] = port
				break
			}
			if _, ok := out[c.UUID]; ok {
				break
			}
		}
	}
	return out
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

// redisConnKeys are the container env vars that decide whether the log-shipper
// inside can reach Redis at all. All three are baked in at container-create time
// and buildRedisEnv derives all three, so all three have to be reconciled.
var redisConnKeys = []string{"REDIS_ADDR", "REDIS_USER", "REDIS_PASS"}

// envValue returns the value of key in a "KEY=VALUE" env slice, "" when absent.
func envValue(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, prefix); ok {
			return v
		}
	}
	return ""
}

// redisEnvDrift names the first Redis connection variable whose value in a
// running container differs from what a container created now would get, or ""
// when they agree. Only the NAME is returned: REDIS_PASS is a live credential
// and the caller logs this.
func redisEnvDrift(current, want []string) string {
	for _, k := range redisConnKeys {
		if envValue(current, k) != envValue(want, k) {
			return k
		}
	}
	return ""
}

// ReconcileRedisEnv checks all running MC containers and restarts any whose
// baked-in Redis connection env doesn't match what a container created NOW
// would get. This handles the case where Node is redeployed with a new
// SIDECAR_REDIS_ADDR — running containers still have the old value from
// creation time.
//
// It compares the CREDENTIALS too, not just the address, because they drift for
// a different and less visible reason. buildRedisEnv derives REDIS_USER and
// REDIS_PASS from the per-node secret, and a pairing reset replaces that secret
// while every MC container keeps running (they are host siblings; restarting
// the agent does not touch them). Core then provisions the shipper user at the
// NEW password, so every container on the machine holds a credential that will
// never authenticate again. Nothing about that is visible from outside: Java
// keeps serving players, the container stays Up, and only the console goes
// silent - the shipper retries forever (log-shipper connectRedis never gives
// up) and the stdin bridge that carries panel commands is gone with it.
//
// The expected value goes through the same resolver as container creation, not
// the startup global: on the warp proxy that global is deliberately empty, and
// comparing against it would restart every server on the machine on every node
// start, forever, without ever converging.
func (dm *DockerManager) ReconcileRedisEnv() {
	running, err := dm.ListRunningMCContainers()
	if err != nil {
		log.Printf("ReconcileRedisEnv: failed to list containers: %v", err)
		return
	}
	if len(running) == 0 {
		return
	}
	netID, _, err := dm.ensureGlobalNetwork()
	if err != nil {
		log.Printf("ReconcileRedisEnv: cannot resolve the server network: %v", err)
		return
	}
	wantAddr, err := dm.sidecarRedisAddr(netID)
	if err != nil {
		log.Printf("ReconcileRedisEnv: %v", err)
		return
	}

	for _, mc := range running {
		info, err := dm.cli.ContainerInspect(dm.ctx, mc.ContainerName)
		if err != nil {
			continue
		}

		// Built through buildRedisEnv rather than re-deriving the three values
		// here, so there is exactly one producer of what a container should
		// hold. The sub-server is irrelevant to this comparison (redisEnvDrift
		// only reads the connection keys), so it is left empty.
		want := buildRedisEnv(mc.UUID, "", wantAddr)
		if key := redisEnvDrift(info.Config.Env, want); key != "" {
			log.Printf("ReconcileRedisEnv: %s has a stale %s — restarting", mc.ContainerName, key)
			if err := dm.RestartContainer(mc.UUID); err != nil {
				log.Printf("ReconcileRedisEnv: failed to restart %s: %v", mc.ContainerName, err)
			}
		}
	}
}
