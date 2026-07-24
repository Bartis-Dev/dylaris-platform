import { describe, it, expect } from 'vitest';
import {
    modeLabel, modeHelp, isProxyScope, fullAccessCaps,
    describeGrantAccess, canEditInMode, FULL_ACCESS_PRESET_ID,
} from './accessMode';
import type { Server } from '@/lib/api';
import type { Grant } from '@/lib/api/grants';
import type { Preset } from '@/lib/api/authzCatalog';

const servers = [
    { id: 1, uuid: 'a', name: 'Proxy', nodeId: 1, ownerId: 'o', port: 1, memory: 1, status: 'online', serverType: 'proxy' },
    { id: 2, uuid: 'b', name: 'Game', nodeId: 1, ownerId: 'o', port: 1, memory: 1, status: 'online', serverType: 'game' },
    { id: 3, uuid: 'c', name: 'Untyped', nodeId: 1, ownerId: 'o', port: 1, memory: 1, status: 'online' },
] as unknown as Server[];

const presets: Preset[] = [
    { id: 'viewer', label: 'Viewer', description: '', capabilities: ['files.read'] },
    { id: 'admin', label: 'Server admin', description: '', capabilities: ['files.read', 'files.write', 'power.start'] },
];
const FULL = ['files.read', 'files.write', 'power.start'];

function grant(over: Partial<Grant>): Grant {
    return {
        username: 'friend', serverId: null, serverName: '', serverRoleId: null,
        serverRoleName: '', grantCaps: [], denyCaps: [], inherit: false, accountWide: true,
        ...over,
    };
}

describe('modeLabel / modeHelp', () => {
    it('labels off/simple/advanced as Off/Full-only/Admin-roles', () => {
        expect(modeLabel('off')).toBe('Off');
        expect(modeLabel('simple')).toBe('Full-only');
        expect(modeLabel('advanced')).toBe('Admin-roles');
    });
    it('returns non-empty help for every mode', () => {
        for (const m of ['off', 'simple', 'advanced'] as const) {
            expect(modeHelp(m).length).toBeGreaterThan(0);
        }
    });
});

describe('isProxyScope', () => {
    it('true for a proxy server id', () => { expect(isProxyScope(servers, 1)).toBe(true); });
    it('false for a game server id', () => { expect(isProxyScope(servers, 2)).toBe(false); });
    it('false for an untyped server (missing serverType)', () => { expect(isProxyScope(servers, 3)).toBe(false); });
    it('false for account-wide (null)', () => { expect(isProxyScope(servers, null)).toBe(false); });
    it('false for an unknown id', () => { expect(isProxyScope(servers, 99)).toBe(false); });
});

describe('fullAccessCaps', () => {
    it('returns the admin preset caps', () => { expect(fullAccessCaps(presets)).toEqual(FULL); });
    it('returns [] when no admin preset is present', () => {
        expect(fullAccessCaps([{ id: 'viewer', label: 'V', description: '', capabilities: ['x'] }])).toEqual([]);
    });
    it('uses the FULL_ACCESS_PRESET_ID constant', () => { expect(FULL_ACCESS_PRESET_ID).toBe('admin'); });
});

describe('describeGrantAccess', () => {
    it('shows the role name when a role is set', () => {
        expect(describeGrantAccess(grant({ serverRoleName: 'Moderator' }), FULL)).toBe('Moderator');
    });
    it('shows Full access for exactly the full caps, no denies, no role', () => {
        expect(describeGrantAccess(grant({ grantCaps: [...FULL] }), FULL)).toBe('Full access');
    });
    it('shows Full access regardless of cap ordering', () => {
        expect(describeGrantAccess(grant({ grantCaps: ['power.start', 'files.write', 'files.read'] }), FULL)).toBe('Full access');
    });
    it('shows Custom for a partial cap set', () => {
        expect(describeGrantAccess(grant({ grantCaps: ['files.read'] }), FULL)).toBe('Custom');
    });
    it('shows Custom for a partial set padded with duplicate caps to the full length', () => {
        expect(describeGrantAccess(grant({ grantCaps: ['files.read', 'files.read', 'files.write'] }), FULL)).toBe('Custom');
    });
    it('shows Custom when a deny is present even if grant caps match', () => {
        expect(describeGrantAccess(grant({ grantCaps: [...FULL], denyCaps: ['files.write'] }), FULL)).toBe('Custom');
    });
    it('shows Custom when full caps are unknown (empty)', () => {
        expect(describeGrantAccess(grant({ grantCaps: [...FULL] }), [])).toBe('Custom');
    });
});

describe('canEditInMode', () => {
    it('advanced mode can edit any grant', () => {
        expect(canEditInMode(grant({ grantCaps: ['files.read'] }), 'advanced', FULL)).toBe(true);
    });
    it('simple mode can edit a full-access grant', () => {
        expect(canEditInMode(grant({ grantCaps: [...FULL] }), 'simple', FULL)).toBe(true);
    });
    it('simple mode cannot edit a partial/legacy grant', () => {
        expect(canEditInMode(grant({ grantCaps: ['files.read'] }), 'simple', FULL)).toBe(false);
    });
    it('off mode never allows editing', () => {
        expect(canEditInMode(grant({ grantCaps: [...FULL] }), 'off', FULL)).toBe(false);
    });
});
