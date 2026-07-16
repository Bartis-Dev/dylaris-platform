import { describe, it, expect } from 'vitest';
import { siblingRead, withImpliedReads, filterScopes, capIdsForScopes } from './caps';
import type { CatalogScope } from '@/lib/api/authzCatalog';

const cat: CatalogScope[] = [
    { scope: 'server', categories: [
        { category: 'Files', capabilities: [
            { id: 'files.read', label: 'Read files', verb: 'read' },
            { id: 'files.write', label: 'Write files', verb: 'write' },
        ] },
    ] },
    { scope: 'panel', categories: [
        { category: 'Users', capabilities: [
            { id: 'users.read', label: 'View users', verb: 'read' },
        ] },
    ] },
];

describe('siblingRead', () => {
    it('maps *.write to *.read', () => { expect(siblingRead('files.write')).toBe('files.read'); });
    it('returns null for a read cap', () => { expect(siblingRead('files.read')).toBeNull(); });
    it('returns null for an action verb', () => { expect(siblingRead('rcon.exec')).toBeNull(); });
});

describe('withImpliedReads', () => {
    it('adds the sibling read when present in the catalog', () => {
        const known = new Set(['files.read', 'files.write']);
        expect(withImpliedReads(['files.write'], known).sort()).toEqual(['files.read', 'files.write']);
    });
    it('does not invent a read that is not in the catalog', () => {
        const known = new Set(['config.write']);
        expect(withImpliedReads(['config.write'], known)).toEqual(['config.write']);
    });
    it('is idempotent and dedupes', () => {
        const known = new Set(['files.read', 'files.write']);
        expect(withImpliedReads(['files.write', 'files.read'], known).sort()).toEqual(['files.read', 'files.write']);
    });
});

describe('filterScopes', () => {
    it('keeps only requested scopes', () => {
        expect(filterScopes(cat, ['server']).map(s => s.scope)).toEqual(['server']);
    });
    it('supports multiple scopes', () => {
        expect(filterScopes(cat, ['server', 'panel']).map(s => s.scope)).toEqual(['server', 'panel']);
    });
});

describe('capIdsForScopes', () => {
    it('flattens cap ids under the requested scopes', () => {
        expect(capIdsForScopes(cat, ['server']).sort()).toEqual(['files.read', 'files.write']);
    });
    it('returns nothing for an unknown scope', () => {
        expect(capIdsForScopes(cat, ['owner'])).toEqual([]);
    });
});
