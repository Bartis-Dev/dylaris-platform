/**
 * Deploy snippets for an external (BYON) node.
 *
 * The panel used to show four ENV lines plus a bare `docker swarm join`, which
 * is not enough to bring a machine up: the operator still had to know that warp
 * must start before the node, that the node spawns its own link sidecar, that
 * the node must NOT get a CLUSTER_SECRET, and which addresses are reachable only
 * over the overlay. All of that is encoded here instead.
 *
 * Values the operator must fill in are left as obvious <placeholders> rather
 * than guessed: an address that merely looks plausible is worse than a blank,
 * because it fails later and somewhere else. Everything the panel CAN answer
 * (the two keys, the node id, the overlay addresses) is filled in, so a
 * complete deploy is a copy-paste rather than a scavenger hunt.
 *
 * Lines whose value equals the image default are deliberately absent: LEADER is
 * false unless set, and an external node manages its own Link with the built-in
 * image. Every line left here is one the reader has to be able to justify.
 */

export type WarpDeployInput = {
    /** Plaintext warp enrollment key. Shown once at mint time. */
    apiKey: string;
    /** Core's public base URL, no trailing /api. */
    enrollUrl: string;
    /** Overlay CIDR where Redis and core gRPC live, e.g. 10.20.0.0/16. */
    tunnelSubnets?: string;
    /** Single-use node enroll token, when the operator has one. */
    nodeEnrollToken?: string;
    /**
     * Core's gRPC certificate fingerprint, returned when minting the enroll
     * token. Core sends it ONLY when GRPC_TLS_ENABLED is on, so its presence is
     * the signal to turn TLS on for the node too - the two must match or the
     * node cannot reach Core at all.
     */
    grpcTlsFingerprint?: string;
    /** Stable id for the machine. */
    nodeId?: string;
    /** Route-only: the local host the link may dial. Host only, no port. */
    localTarget?: string;
};

const REG = 'ghcr.io/bartis-dev';

/**
 * Port warp binds locally for its Redis proxy.
 *
 * The same number is compiled in two other places - gateway/warp/proxy.go and
 * platform/node/warp_proxy.go - because there is no channel between the three
 * to negotiate a pair. A machine with a collision sets REDIS_ADDR explicitly,
 * which bypasses the proxy entirely.
 */
const WARP_PROXY_REDIS_PORT = '25571';

function or(value: string | undefined, placeholder: string): string {
    const v = (value ?? '').trim();
    return v === '' ? placeholder : v;
}

/**
 * The node's half of the Core gRPC pin, emitted only when there is one.
 *
 * Core returns a fingerprint exclusively while GRPC_TLS_ENABLED is on, so a
 * value here means the control channel IS TLS and the node must match it. It
 * does not verify the hostname - it compares this fingerprint - which is also
 * why warp's local proxy in front of it changes nothing.
 *
 * Absent means Core runs the channel in plaintext, and emitting the pair would
 * make the node fail its handshake against a server that offers no TLS.
 */
function grpcTlsLines(fingerprint: string | undefined): string {
    const fp = (fingerprint ?? '').trim();
    if (fp === '') return '';
    return `      # Core's control channel is TLS. The node pins this fingerprint
      # instead of verifying a hostname, so it must match Core exactly.
      GRPC_TLS_ENABLED: "true"
      GRPC_TLS_FINGERPRINT: "${fp}"
`;
}

/**
 * Turns the location name a customer typed into a usable NODE_ID.
 *
 * NODE_ID ends up in Redis keys, the mesh identity and the environment of every
 * container the node starts, so "My Home PC" cannot be pre-filled verbatim.
 * Returns undefined when nothing usable is left, which leaves the snippet's
 * placeholder in place rather than a value that is silently wrong.
 */
export function nodeIdFromLabel(label: string | undefined): string | undefined {
    const slug = (label ?? '')
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/^-+|-+$/g, '')
        .slice(0, 40)
        .replace(/-+$/, '');
    return slug === '' ? undefined : slug;
}

/**
 * routeOnlyCompose is warp + link: the customer runs the Minecraft server
 * themselves and gets a protected Dylaris address for it. No node, no swarm
 * join, no published ports.
 *
 * Both containers are host-networked, so anything they bind lands on the
 * customer's own machine. The link's management server is therefore pinned to
 * loopback below: nothing reads it in this mode (only a NODE-managed link has a
 * reader), and its /health is unauthenticated.
 *
 * The link is what makes this route-only rather than plain overlay access, and
 * nothing deploys it implicitly: a managed node starts its own link, but here
 * the customer runs it. It fetches its tunnel token and a scoped Redis
 * credential from Core at boot, authenticated by the same warp key, so no
 * second secret has to be handed out and nothing secret lands on disk.
 */
export function routeOnlyCompose(i: WarpDeployInput): string {
    return `# route-only.yml  ->  docker compose -f route-only.yml up -d
# Linux only: kernel WireGuard needs host networking and NET_ADMIN.
services:
  warp:
    image: ${REG}/dylaris-gateway-warp:latest
    restart: unless-stopped
    environment:
      API_KEY: "${i.apiKey}"
      ENROLL_URL: "${or(i.enrollUrl, '<core-url>')}"
      # Overlay CIDR(s) to route through the tunnel - NOT your home LAN.
      TUNNEL_SUBNETS: "${or(i.tunnelSubnets, '<overlay-cidr e.g. 10.20.0.0/16>')}"
    network_mode: host
    cap_add: [NET_ADMIN]

  link:
    image: ${REG}/dylaris-gateway-link:latest
    restart: unless-stopped
    depends_on: [warp]
    environment:
      CORE_URL: "${or(i.enrollUrl, '<core-url>')}"
      # The same warp key: the link exchanges it at boot for its own derived
      # token, so no second secret travels with the kit.
      LINK_BOOT_KEY: "${i.apiKey}"
      # warp's local proxy. warp holds the real overlay address and refreshes
      # it from Core, so this line never changes even if the platform moves.
      REDIS_ADDR: "127.0.0.1:${WARP_PROXY_REDIS_PORT}"
      # Must stay false: the proxy is dialled at 127.0.0.1, so a TLS client
      # would verify the certificate against loopback and fail. The whole path
      # is already inside WireGuard.
      REDIS_USE_TLS: "false"
      # Your own Minecraft server. Host only, NO port - compared as an exact
      # string, so "127.0.0.1:25565" never matches.
      LINK_ALLOWED_TARGETS: "${or(i.localTarget, '127.0.0.1')}"
      LOCAL_HOST: "${or(i.localTarget, '127.0.0.1')}"
      # Host networking, so the default ":25540" would put an unauthenticated
      # status endpoint on your LAN. Nothing reads it in route-only mode.
      LINK_PORT: "127.0.0.1:25540"
    network_mode: host
`;
}

/**
 * nodeCompose is the full managed-node stack: warp joins the overlay, then the
 * node agent runs MC containers on this machine.
 *
 * Two things here are load-bearing and easy to get wrong by hand: the node gets
 * NO CLUSTER_SECRET (it fetches a scoped Redis credential over gRPC after
 * enrolling, which is what keeps a customer machine from holding fleet
 * credentials), and it spawns its own link sidecar, so nobody should run link
 * separately.
 */
export function nodeCompose(i: WarpDeployInput): string {
    return `# byon-node.yml  ->  docker compose -f byon-node.yml up -d
# Linux host or Docker Desktop. warp must be up before the node can reach Redis.
services:
  warp:
    image: ${REG}/dylaris-gateway-warp:latest
    restart: unless-stopped
    environment:
      API_KEY: "${i.apiKey}"
      ENROLL_URL: "${or(i.enrollUrl, '<core-url>')}"
      # Overlay CIDR(s) to route through the tunnel - NOT your home LAN.
      TUNNEL_SUBNETS: "${or(i.tunnelSubnets, '<overlay-cidr e.g. 10.20.0.0/16>')}"
      # Also serve the local proxy to containers: the link sidecar and every
      # Minecraft server sit on a Docker bridge and cannot reach loopback here.
      PROXY_BIND_DOCKER_BRIDGES: "true"
    network_mode: host
    cap_add: [NET_ADMIN]

  node:
    image: ${REG}/dylaris-platform-node:latest
    restart: unless-stopped
    depends_on: [warp]
    environment:
      NODE_EXTERNAL: "true"
      NODE_ID: "${or(i.nodeId, '<stable-id-for-this-machine>')}"
      # Single-use, first boot only.
      NODE_ENROLL_TOKEN: "${or(i.nodeEnrollToken, '<enroll-token-from-panel>')}"
${grpcTlsLines(i.grpcTlsFingerprint)}      # No CORE_GRPC_ADDR and no REDIS_ADDR: the node reaches both through
      # warp's local proxy, and warp refreshes the real addresses from Core on
      # its own. Nothing in this file has to change when the platform moves.
      # NO CLUSTER_SECRET and no static Redis password on purpose: the node
      # fetches a scoped Redis credential over gRPC once it has enrolled.
      # It also starts its own link sidecar - do not run link yourself.
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /dev:/dev:ro
      # Server files. Replace the named volume with a path of your own to keep
      # them somewhere you can see:
      #   Linux           - /srv/dylaris:/app/dylaris_data
      #   Docker Desktop  - C:\\dylaris:/app/dylaris_data
      - byon_data:/app/dylaris_data
    network_mode: host
    cap_add: [SYS_ADMIN]

volumes:
  byon_data:
`;
}

/** CLI steps that go with either compose file. */
export function deployCli(kind: 'route-only' | 'node'): string {
    const file = kind === 'route-only' ? 'route-only.yml' : 'byon-node.yml';
    return `# 1. Save the compose file above, then start it. Pull first: the
#    tunnel agent is what supplies the internal addresses, so a stale
#    cached image would leave the rest of the stack with nothing to talk to.
docker compose -f ${file} pull
docker compose -f ${file} up -d

# 2. Watch the tunnel come up (peer + handshake within ~15s):
docker compose -f ${file} logs -f warp

# 3. Verify the overlay actually carries traffic, not just that wg0 exists:
docker compose -f ${file} exec warp wg show
${kind === 'node'
            ? `
# 4. The node registers itself; it appears in the panel under Nodes within ~30s.
docker compose -f ${file} logs -f node`
            : `
# 4. The link registers its tunnel; then create the route(s) in the panel.
docker compose -f ${file} logs -f link`}`;
}

/**
 * A node running with host networking binds these on the customer's machine.
 * NODE_EXTERNAL is an advertised mode, not enforcement - the SFTP listener in
 * particular starts regardless - so the operator has to be told rather than
 * assume they are closed.
 */
export const EXTERNAL_NODE_PORTS = [
    { port: 25520, what: 'SFTP', note: 'starts even with NODE_EXTERNAL=true' },
    { port: 25521, what: 'Beam gRPC', note: 'overlay-only in practice' },
    { port: 25522, what: 'Migration pull', note: 'auto-move transport' },
    { port: 25523, what: 'Beam LAN fast-path', note: 'set BEAM_LAN_FASTPATH=false to drop it' },
    { port: 25570, what: 'Overlay proxy (Core)', note: 'bound by warp on loopback + your Docker bridges, never the LAN' },
    { port: 25571, what: 'Overlay proxy (Redis)', note: 'same; this is how containers reach the overlay' },
];
