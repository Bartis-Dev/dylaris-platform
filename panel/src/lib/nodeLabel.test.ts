import { describe, it, expect } from 'vitest';
import { nodeLabel } from './nodeLabel';

describe('nodeLabel', () => {
    it('prefers the admin-editable display name', () => {
        expect(nodeLabel({ displayName: 'eu-node-00', name: 'uuid-here' })).toBe('eu-node-00');
    });

    it('falls back to the row name when no display name is set', () => {
        expect(nodeLabel({ name: 'legacy-hostname' })).toBe('legacy-hostname');
    });

    it('treats a whitespace-only display name as unset', () => {
        expect(nodeLabel({ displayName: '   ', name: 'legacy-hostname' })).toBe('legacy-hostname');
    });

    it('falls back to the id when neither is set', () => {
        expect(nodeLabel({ id: 7 })).toBe('Node 7');
    });

    // The regression this file exists for: the Infrastructure view rendered
    // `node.token || node.name`, which was the hostname only while token still
    // WAS the hostname. Under the pairing hardening the token is a Core-minted
    // UUID, and every node showed up as one.
    it('never uses the token as a label', () => {
        const node = { displayName: 'eu-node-00', name: 'x', token: '183940c8-f75c-4511-bb2f-b416539bfbc9' };
        expect(nodeLabel(node)).not.toContain('183940c8');
    });
});
