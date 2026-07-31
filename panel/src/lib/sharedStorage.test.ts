import { describe, it, expect } from 'vitest';
import {
    peerLabel,
    sharedStorageMessage,
    sharedStorageSummary,
    type SharedStorageConflict,
} from './sharedStorage';

function conflict(p: Partial<SharedStorageConflict> = {}): SharedStorageConflict {
    return { path: '/storage', kind: 'peer', ...p };
}

describe('peerLabel', () => {
    it('prefers the hostname, which is what an operator can act on', () => {
        expect(peerLabel(conflict({ peerHost: 'worker-2', peerNode: 'abc123' }))).toBe('worker-2');
    });

    it('falls back to the node id', () => {
        expect(peerLabel(conflict({ peerNode: 'abc123' }))).toBe('abc123');
    });

    it('stays readable when the beacon carried neither', () => {
        expect(peerLabel(conflict())).toBe('another node');
        expect(peerLabel(conflict({ peerHost: '  ', peerNode: '' }))).toBe('another node');
    });
});

describe('sharedStorageMessage', () => {
    it('names the path and the peer', () => {
        const msg = sharedStorageMessage(conflict({ path: '/mnt/nas', peerHost: 'worker-2' }));
        expect(msg).toContain('/mnt/nas');
        expect(msg).toContain('worker-2');
    });

    // The identity case means node ids and secrets are already being overwritten.
    // Reading like the milder peer case would hide why a node's id keeps changing.
    it('keeps the identity case distinct from a peer conflict', () => {
        const peer = sharedStorageMessage(conflict({ kind: 'peer' }));
        const identity = sharedStorageMessage(conflict({ kind: 'identity' }));
        expect(identity).not.toBe(peer);
        expect(identity).toMatch(/identity/i);
    });

    it('never renders undefined into the text', () => {
        expect(sharedStorageMessage(conflict())).not.toContain('undefined');
        expect(sharedStorageMessage(conflict({ kind: 'identity' }))).not.toContain('undefined');
    });
});

describe('sharedStorageSummary', () => {
    it('counts distinct paths, not conflicts', () => {
        // Two peers on ONE path is still one broken mount.
        const summary = sharedStorageSummary([
            conflict({ path: '/storage', peerNode: 'a' }),
            conflict({ path: '/storage', peerNode: 'b' }),
        ]);
        expect(summary).toContain('1 storage path is');
    });

    it('pluralises several paths', () => {
        const summary = sharedStorageSummary([
            conflict({ path: '/storage' }),
            conflict({ path: '/mnt/nas' }),
        ]);
        expect(summary).toContain('2 storage paths are');
    });

    // Fixing one mount while another is still shared leaves the failure in
    // place, so the worst kind present decides the headline.
    it('reports the worst kind present', () => {
        const summary = sharedStorageSummary([
            conflict({ path: '/a', kind: 'peer' }),
            conflict({ path: '/b', kind: 'identity' }),
        ]);
        expect(summary).toMatch(/identity/i);
    });

    it('omits the identity wording when only peers are present', () => {
        expect(sharedStorageSummary([conflict()])).not.toMatch(/identity/i);
    });
});
