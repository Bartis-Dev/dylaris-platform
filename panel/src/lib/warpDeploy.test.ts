import { describe, it, expect } from 'vitest';
import { warpOnlyCompose, nodeCompose, deployCli, EXTERNAL_NODE_PORTS } from './warpDeploy';

const base = { apiKey: 'KEY123', enrollUrl: 'https://api.example.com' };

describe('warpOnlyCompose', () => {
    it('embeds the key and enroll url', () => {
        const out = warpOnlyCompose(base);
        expect(out).toContain('API_KEY: "KEY123"');
        expect(out).toContain('ENROLL_URL: "https://api.example.com"');
    });

    it('leaves an obvious placeholder when the overlay CIDR is unknown', () => {
        expect(warpOnlyCompose(base)).toContain('<overlay-cidr');
        expect(warpOnlyCompose({ ...base, tunnelSubnets: '10.20.0.0/16' }))
            .toContain('TUNNEL_SUBNETS: "10.20.0.0/16"');
    });

    it('treats a whitespace-only value as unset rather than emitting an empty string', () => {
        expect(warpOnlyCompose({ ...base, tunnelSubnets: '   ' })).toContain('<overlay-cidr');
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

    it('sets NODE_MANAGES_LINK so nobody runs a second link sidecar by hand', () => {
        expect(nodeCompose(base)).toContain('NODE_MANAGES_LINK: "true"');
    });

    it('fills in every operator value when known', () => {
        const out = nodeCompose({
            ...base,
            tunnelSubnets: '10.20.0.0/16',
            nodeEnrollToken: 'TOK',
            coreGrpcAddr: '10.20.0.4:25501',
            redisAddr: '10.20.0.5:6379',
            nodeId: 'home-desktop',
        });
        expect(out).toContain('NODE_ENROLL_TOKEN: "TOK"');
        expect(out).toContain('CORE_GRPC_ADDR: "10.20.0.4:25501"');
        expect(out).toContain('REDIS_ADDR: "10.20.0.5:6379"');
        expect(out).toContain('NODE_ID: "home-desktop"');
        expect(out).not.toContain('<');
    });
});

describe('deployCli', () => {
    it('names the matching compose file', () => {
        expect(deployCli('warp')).toContain('warp.yml');
        expect(deployCli('node')).toContain('byon-node.yml');
    });

    it('only mentions node logs for the node variant', () => {
        expect(deployCli('node')).toContain('logs -f node');
        expect(deployCli('warp')).not.toContain('logs -f node');
    });
});

describe('EXTERNAL_NODE_PORTS', () => {
    // NODE_EXTERNAL is an advertised mode, not enforcement: SFTP binds anyway.
    // Users need to be told, so this list must keep saying so.
    it('warns that SFTP binds despite NODE_EXTERNAL', () => {
        const sftp = EXTERNAL_NODE_PORTS.find(p => p.port === 25520);
        expect(sftp?.note).toContain('NODE_EXTERNAL');
    });
});
