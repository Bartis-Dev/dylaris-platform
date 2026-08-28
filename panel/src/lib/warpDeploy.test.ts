import { describe, it, expect } from 'vitest';
import { routeOnlyCompose, nodeCompose, deployCli, nodeIdFromLabel, defaultLocalTarget, EXTERNAL_NODE_PORTS } from './warpDeploy';

const base = { apiKey: 'KEY123', enrollUrl: 'https://api.example.com' };

describe('routeOnlyCompose', () => {
    it('embeds the key and enroll url', () => {
        const out = routeOnlyCompose(base);
        expect(out).toContain('API_KEY: "KEY123"');
        expect(out).toContain('ENROLL_URL: "https://api.example.com"');
    });

    // The link is what makes this route-only rather than plain overlay access.
    it('includes the link container', () => {
        const out = routeOnlyCompose(base);
        expect(out).toContain('dylaris-gateway-link');
        expect(out).toContain('depends_on: [warp]');
    });

    // One key does both jobs: the link exchanges it for its own derived token,
    // so no second secret is handed out.
    it('reuses the warp key as LINK_BOOT_KEY', () => {
        expect(routeOnlyCompose(base)).toContain('LINK_BOOT_KEY: "KEY123"');
    });

    // Against a plain-TCP Redis a TLS client fails the handshake and the link
    // never registers - the exact trap the repo example still carries. With the
    // local proxy it is doubly required: a TLS client would verify the
    // certificate against 127.0.0.1. The path is inside WireGuard either way.
    it('defaults REDIS_USE_TLS to false', () => {
        expect(routeOnlyCompose(base)).toContain('REDIS_USE_TLS: "false"');
    });

    // Host networking means anything the link binds lands on the customer's
    // machine, and /health has no auth. Nothing reads it in this mode.
    it('keeps the link management server off the LAN', () => {
        expect(routeOnlyCompose(base)).toContain('LINK_PORT: "127.0.0.1:25540"');
    });

    // Route-only runs the link with host networking, so warp's loopback
    // listener is already in its namespace and no bridge binding is needed.
    it('points the link at the local proxy and binds no bridges', () => {
        const out = routeOnlyCompose(base);
        expect(out).toContain('REDIS_ADDR: "127.0.0.1:25571"');
        expect(out).not.toContain('PROXY_BIND_DOCKER_BRIDGES');
    });

    // LINK_ALLOWED_TARGETS is compared as an exact host string; a port never matches.
    it('uses a bare host for the local target', () => {
        const out = routeOnlyCompose({ ...base, localTarget: '192.168.1.50' });
        expect(out).toContain('LINK_ALLOWED_TARGETS: "192.168.1.50"');
        expect(out).not.toContain('192.168.1.50:');
        expect(routeOnlyCompose(base)).toContain('LINK_ALLOWED_TARGETS: "127.0.0.1"');
    });

    it('leaves an obvious placeholder when the overlay CIDR is unknown', () => {
        expect(routeOnlyCompose(base)).toContain('<overlay-cidr');
        expect(routeOnlyCompose({ ...base, tunnelSubnets: '10.20.0.0/16' }))
            .toContain('TUNNEL_SUBNETS: "10.20.0.0/16"');
    });

    it('treats a whitespace-only value as unset rather than emitting an empty string', () => {
        expect(routeOnlyCompose({ ...base, tunnelSubnets: '   ' })).toContain('<overlay-cidr');
    });
});

describe('nodeCompose', () => {
    it('never emits a CLUSTER_SECRET — a customer machine must not hold fleet credentials', () => {
        expect(nodeCompose(base)).not.toContain('CLUSTER_SECRET:');
    });

    it('starts warp before the node, since the node reaches Redis over the overlay', () => {
        const out = nodeCompose(base);
        expect(out.indexOf('warp:')).toBeLessThan(out.indexOf('node:'));
        expect(out).toContain('depends_on: [warp]');
    });

    // Both are image defaults now (an external node manages its own Link with
    // the built-in image), so carrying them here is noise the reader has to
    // evaluate. Keep them out - if they come back, the reason has to be new.
    it('omits settings that equal the image default', () => {
        const out = nodeCompose(base);
        expect(out).not.toContain('NODE_MANAGES_LINK');
        expect(out).not.toContain('LINK_IMAGE');
        expect(out).not.toContain('LEADER:');
    });

    // Named volumes live where Docker decides; someone running servers on their
    // own box wants to know where the files are.
    it('shows how to bind the data directory to a real path', () => {
        const out = nodeCompose(base);
        expect(out).toContain('/app/dylaris_data');
        expect(out).toContain('/srv/dylaris:/app/dylaris_data');
        expect(out).toContain('C:\\dylaris:/app/dylaris_data');
    });

    it('fills in every operator value when known', () => {
        const out = nodeCompose({
            ...base,
            tunnelSubnets: '10.20.0.0/16',
            nodeEnrollToken: 'TOK',
            nodeId: 'home-desktop',
        });
        expect(out).toContain('NODE_ENROLL_TOKEN: "TOK"');
        expect(out).toContain('NODE_ID: "home-desktop"');
        expect(out).not.toContain('<');
    });

    // The whole point of the local proxy: the file carries no overlay address,
    // so a platform that moves does not send every customer back to the panel.
    it('carries no overlay address at all', () => {
        const out = nodeCompose({ ...base, tunnelSubnets: '10.20.0.0/16', nodeEnrollToken: 'TOK', nodeId: 'n' });
        // The names still appear in the comment that explains their absence,
        // so assert on the assignment, which is what a reader would have to edit.
        expect(out).not.toContain('CORE_GRPC_ADDR: "');
        expect(out).not.toContain('REDIS_ADDR: "');
    });

    // The link sidecar and every MC server run on a Docker bridge, where
    // 127.0.0.1 is their own loopback and not the host warp listens on.
    it('lets warp serve the proxy to containers too', () => {
        expect(nodeCompose(base)).toContain('PROXY_BIND_DOCKER_BRIDGES: "true"');
    });

    // Core returns a fingerprint only while its gRPC channel is TLS, so its
    // presence is the signal. Emitting the pin against a plaintext Core would
    // make every BYON node fail its handshake instead of connecting.
    it('pins the Core gRPC certificate when there is a fingerprint', () => {
        const withPin = nodeCompose({ ...base, grpcTlsFingerprint: 'ab12cd34' });
        expect(withPin).toContain('GRPC_TLS_ENABLED: "true"');
        expect(withPin).toContain('GRPC_TLS_FINGERPRINT: "ab12cd34"');
    });

    // The node defaults GRPC_TLS_ENABLED to true, and a BYON machine holds no
    // CLUSTER_SECRET to derive a pin from - so a snippet that simply omits the
    // line hands the customer a container that exits at boot with "no
    // certificate pin available". Silence stopped being a safe way to say
    // "plaintext" the moment the default flipped; the opt-out has to be written.
    it('says plaintext out loud rather than leaning on the old default', () => {
        const without = nodeCompose(base);
        expect(without).toContain('GRPC_TLS_ENABLED: "false"');
        expect(without).not.toContain('GRPC_TLS_FINGERPRINT');
    });

    // A machine that runs our node also runs warp's proxy, and the operator is
    // told what binds rather than left to find two unexplained ports.
    it('lists the proxy ports among what the machine binds', () => {
        const ports = EXTERNAL_NODE_PORTS.map(p => p.port);
        expect(ports).toContain(25570);
        expect(ports).toContain(25571);
    });
});

describe('nodeIdFromLabel', () => {
    // NODE_ID lands in Redis keys, the mesh identity and every container's
    // environment, so the free-text location name cannot go in verbatim.
    it('reduces a typed location name to a safe id', () => {
        expect(nodeIdFromLabel('My Home PC')).toBe('my-home-pc');
        expect(nodeIdFromLabel('  Rack #3 / EU  ')).toBe('rack-3-eu');
        expect(nodeIdFromLabel('home-desktop')).toBe('home-desktop');
    });

    // undefined leaves the snippet's placeholder in place; an empty string
    // would render as NODE_ID: "" and read like a deliberate setting.
    it('returns undefined when nothing usable is left', () => {
        expect(nodeIdFromLabel('')).toBeUndefined();
        expect(nodeIdFromLabel('   ')).toBeUndefined();
        expect(nodeIdFromLabel('###')).toBeUndefined();
        expect(nodeIdFromLabel(undefined)).toBeUndefined();
    });

    // The slice that caps the length must not leave a trailing separator.
    it('never ends in a separator after truncation', () => {
        const out = nodeIdFromLabel('a'.repeat(39) + ' tail');
        expect(out).not.toMatch(/-$/);
    });
});

describe('deployCli', () => {
    it('names the matching compose file', () => {
        expect(deployCli('route-only')).toContain('route-only.yml');
        expect(deployCli('node')).toContain('byon-node.yml');
    });

    it('tails the container that matters for each variant', () => {
        expect(deployCli('node')).toContain('logs -f node');
        expect(deployCli('route-only')).not.toContain('logs -f node');
        expect(deployCli('route-only')).toContain('logs -f link');
    });

    // The first step is the one people get stuck on, and it is the only one
    // that differs: a Linux reader has a shell, a Windows reader has Explorer
    // and a Notepad that appends .txt behind their back.
    it('shows how to create the file on each platform', () => {
        const linux = deployCli('route-only', 'linux');
        expect(linux).toContain('nano route-only.yml');
        expect(linux).not.toContain('notepad');

        const win = deployCli('route-only', 'windows');
        expect(win).toContain('notepad route-only.yml');
        // A lone backslash is not an escape in a template literal, it is
        // dropped - which would render the path as $HOMEdylaris.
        expect(win).toContain('mkdir "$HOME\\dylaris"');
        expect(win).toContain('.txt');
        expect(win).not.toContain('nano ');
    });

    it('defaults to the Linux steps', () => {
        expect(deployCli('node')).toBe(deployCli('node', 'linux'));
    });

    // Both platforms start the stack the same way, and both readers may be on
    // Portainer instead of a shell.
    it('starts the stack identically on both platforms', () => {
        for (const p of ['linux', 'windows'] as const) {
            const out = deployCli('route-only', p);
            expect(out).toContain('docker compose -f route-only.yml pull');
            expect(out).toContain('docker compose -f route-only.yml up -d');
            expect(out).toContain('Portainer');
        }
    });
});

describe('EXTERNAL_NODE_PORTS', () => {
    // NODE_EXTERNAL is an advertised mode, not enforcement: SFTP binds anyway.
    // Users need to be told, so this list must keep saying so.
    it('warns that SFTP binds despite NODE_EXTERNAL', () => {
        const sftp = EXTERNAL_NODE_PORTS.find(p => p.port === 25520);
        expect(sftp?.note).toContain('NODE_EXTERNAL');
    });

    // The note tells the operator to set an env var. A var that is absent from
    // the compose file's environment: block cannot be set from .env at all, so
    // the advice only works while the snippet actually writes the line out.
    it('only names an env var the generated compose forwards', () => {
        const fastpath = EXTERNAL_NODE_PORTS.find(p => p.port === 25523);
        expect(fastpath?.note).toContain('BEAM_LAN_FASTPATH=false');
        expect(nodeCompose(base)).toContain('BEAM_LAN_FASTPATH: "true"');
    });
});

// Docker Desktop's `network_mode: host` joins the WSL2 VM, not Windows. A
// snippet that kept 127.0.0.1 there would point the link at the VM's loopback
// and never reach the server the customer actually runs - measured: a
// host-networked port is not reachable from Windows, host.docker.internal is.
describe('routeOnlyCompose on Docker Desktop', () => {
    it('targets host.docker.internal instead of loopback', () => {
        const out = routeOnlyCompose({ ...base, platform: 'windows' });
        expect(out).toContain('LOCAL_HOST: "host.docker.internal"');
        expect(out).toContain('LINK_ALLOWED_TARGETS: "host.docker.internal"');
        expect(out).not.toContain('LOCAL_HOST: "127.0.0.1"');
    });

    it('says why, so the reader is not left guessing', () => {
        const out = routeOnlyCompose({ ...base, platform: 'windows' });
        expect(out).toContain('WSL2 VM');
        expect(out).not.toContain('Linux only');
    });

    // warp and the link share the VM's namespace, so they still find each other
    // on loopback - only the customer's own server sits outside it.
    it('keeps warp\'s local proxy on loopback', () => {
        const out = routeOnlyCompose({ ...base, platform: 'windows' });
        expect(out).toContain('REDIS_ADDR: "127.0.0.1:25571"');
        expect(out).toContain('LINK_PORT: "127.0.0.1:25540"');
    });

    it('still lets an explicit target win', () => {
        const out = routeOnlyCompose({ ...base, platform: 'windows', localTarget: '192.168.1.50' });
        expect(out).toContain('LOCAL_HOST: "192.168.1.50"');
    });

    it('defaults to the linux target when no platform is given', () => {
        expect(defaultLocalTarget(undefined)).toBe('127.0.0.1');
        expect(defaultLocalTarget('linux')).toBe('127.0.0.1');
        expect(defaultLocalTarget('windows')).toBe('host.docker.internal');
        expect(routeOnlyCompose(base)).toContain('LOCAL_HOST: "127.0.0.1"');
    });
});
