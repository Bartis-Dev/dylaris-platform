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
 * because it fails later and somewhere else.
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
    /** core gRPC over the overlay, e.g. 10.20.0.4:25501. */
    coreGrpcAddr?: string;
    /** central Redis over the overlay, e.g. 10.20.0.5:6379. */
    redisAddr?: string;
    /** Stable id for the machine. */
    nodeId?: string;
    /** Route-only: the local host the link may dial. Host only, no port. */
    localTarget?: string;
};

const REG = 'ghcr.io/bartis-dev';

function or(value: string | undefined, placeholder: string): string {
    const v = (value ?? '').trim();
    return v === '' ? placeholder : v;
}

/**
 * routeOnlyCompose is warp + link: the customer runs the Minecraft server
 * themselves and gets a protected Dylaris address for it. No node, no swarm
 * join, no published ports - the machine only makes OUTBOUND connections.
 *
 * The link is what makes this route-only rather than plain overlay access, and
 * nothing deploys it implicitly: a managed node starts its own link, but here
 * the customer runs it. It fetches its tunnel token and a scoped Redis
 * credential from Core at boot, authenticated by the same warp key, so no
 * second secret has to be handed out and nothing secret lands on disk.
 */
export function routeOnlyCompose(i: WarpDeployInput): string {
    return `# route-only.yml  ->  docker compose -f route-only.yml up -d
# Linux only: the kit uses kernel WireGuard (host networking + NET_ADMIN).
services:
  warp:
    image: ${REG}/dylaris-gateway-warp:latest
    restart: unless-stopped
    environment:
      LEADER: "false"
      API_KEY: "${i.apiKey}"
      ENROLL_URL: "${or(i.enrollUrl, '<core-url>')}"
      # Overlay CIDR(s) where Redis and the edges live - NOT your home LAN.
      TUNNEL_SUBNETS: "${or(i.tunnelSubnets, '<overlay-cidr e.g. 10.20.0.0/16>')}"
    network_mode: host
    cap_add: [NET_ADMIN]

  link:
    image: ${REG}/dylaris-gateway-link:latest
    restart: unless-stopped
    depends_on: [warp]
    environment:
      # Both are the SAME warp key: the link exchanges it at boot for its own
      # derived token, so the token never travels with the kit.
      CORE_URL: "${or(i.enrollUrl, '<core-url>')}"
      LINK_BOOT_KEY: "${i.apiKey}"
      REDIS_ADDR: "${or(i.redisAddr, '<redis e.g. 10.20.0.5:6379>')}"
      # false unless your operator terminates TLS on Redis. Against a plain-TCP
      # Redis a TLS client fails the handshake and the link never registers.
      REDIS_USE_TLS: "false"
      # The LOCAL address the link may dial - your own server. Host only, NO
      # port: this is compared as an exact host string.
      LINK_ALLOWED_TARGETS: "${or(i.localTarget, '127.0.0.1')}"
      LOCAL_HOST: "${or(i.localTarget, '127.0.0.1')}"
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
      LEADER: "false"
      API_KEY: "${i.apiKey}"
      ENROLL_URL: "${or(i.enrollUrl, '<core-url>')}"
      # Overlay CIDR(s) where Redis and core live - NOT your home LAN.
      TUNNEL_SUBNETS: "${or(i.tunnelSubnets, '<overlay-cidr e.g. 10.20.0.0/16>')}"
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
      # Both reachable ONLY over the overlay warp provides. Never publish them.
      CORE_GRPC_ADDR: "${or(i.coreGrpcAddr, '<core-grpc e.g. 10.20.0.4:25501>')}"
      REDIS_ADDR: "${or(i.redisAddr, '<redis e.g. 10.20.0.5:6379>')}"
      # The node spawns its own link sidecar - do not run link yourself.
      NODE_MANAGES_LINK: "true"
      LINK_IMAGE: "${REG}/dylaris-gateway-link:latest"
      # NO CLUSTER_SECRET and no static Redis password on purpose: the node
      # fetches a scoped Redis credential over gRPC once it has enrolled.
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /dev:/dev:ro
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
    return `# 1. Save the compose file above, then start it:
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
];
