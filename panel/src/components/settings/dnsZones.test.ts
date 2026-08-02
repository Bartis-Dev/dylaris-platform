import { describe, it, expect } from 'vitest';
import {
    zoneHint,
    isInZone,
    resolveZone,
    normalizeDNSName,
    originLabel,
    addZoneTo,
    removeZoneFrom,
    type DNSZonesResponse,
} from './dnsZones';

function res(partial: Partial<DNSZonesResponse>): DNSZonesResponse {
    return { success: true, state: 'ok', zones: [], ...partial };
}

describe('zoneHint', () => {
    it('shows nothing when zones were listed', () => {
        expect(zoneHint(res({ state: 'ok', zones: ['example.com'] }))).toBeNull();
    });

    it('tells the admin to type the domain when the provider cannot list zones', () => {
        const hint = zoneHint(res({ state: 'unsupported' }));
        expect(hint?.tone).toBe('info');
        expect(hint?.manualEntry).toBe(true);
        // Not a misconfiguration on the admin's part, so it must not read as one.
        expect(hint?.message).not.toMatch(/permission/i);
    });

    it('points an empty listing at token permissions', () => {
        const hint = zoneHint(res({ state: 'empty' }));
        expect(hint?.tone).toBe('warn');
        expect(hint?.message).toMatch(/permission/i);
        expect(hint?.manualEntry).toBe(true);
    });

    // The trap this mapping exists to prevent: a failed call reported as "no
    // zones found" sends the admin to widen a token that was already fine.
    it('keeps a failed call distinct from an empty listing', () => {
        const failed = zoneHint(res({ state: 'error', error: 'dial tcp: i/o timeout' }));
        const empty = zoneHint(res({ state: 'empty' }));
        expect(failed?.tone).toBe('error');
        expect(failed?.message).not.toBe(empty?.message);
        expect(failed?.message).not.toMatch(/widen/i);
    });

    it('surfaces the raw provider error verbatim', () => {
        // libdns does not normalise errors, so only the original text tells a 403
        // apart from a timeout.
        const hint = zoneHint(res({ state: 'error', error: 'HTTP 403: Invalid API token' }));
        expect(hint?.message).toContain('HTTP 403: Invalid API token');
    });

    it('still reads sensibly when the error state carries no message', () => {
        const hint = zoneHint(res({ state: 'error' }));
        expect(hint?.tone).toBe('error');
        expect(hint?.message).toBeTruthy();
        expect(hint?.message).not.toContain('undefined');
    });

    it('offers manual entry in every non-ok state', () => {
        for (const state of ['unsupported', 'empty', 'error'] as const) {
            expect(zoneHint(res({ state }))?.manualEntry).toBe(true);
        }
    });
});

describe('normalizeDNSName', () => {
    it('lowercases, trims and drops the root dot', () => {
        expect(normalizeDNSName('  Example.COM.  ')).toBe('example.com');
    });

    it('leaves an already-normal name alone', () => {
        expect(normalizeDNSName('*.eu.example.com')).toBe('*.eu.example.com');
    });
});

// Mirrors Core's ResolveZone. If the two ever disagree, the panel would accept a
// name the server then rejects, or offer a zone the reconciler would not use.
describe('isInZone', () => {
    it('matches a subdomain of the zone', () => {
        expect(isInZone('*.eu.dylaris.com', 'dylaris.com')).toBe(true);
    });

    it('matches the zone apex itself', () => {
        expect(isInZone('dylaris.com', 'dylaris.com')).toBe(true);
    });

    it('normalises both sides', () => {
        expect(isInZone('*.EU.Dylaris.com.', 'dylaris.com')).toBe(true);
    });

    // The label boundary. Without it the panel would offer to manage a name in a
    // zone it does not belong to.
    it('rejects a lookalike domain', () => {
        expect(isInZone('evil-dylaris.com', 'dylaris.com')).toBe(false);
        expect(isInZone('*.eu.notdylaris.com', 'dylaris.com')).toBe(false);
    });

    it('rejects empty input', () => {
        expect(isInZone('', 'dylaris.com')).toBe(false);
        expect(isInZone('*.eu.dylaris.com', '')).toBe(false);
    });
});

describe('originLabel', () => {
    it('names each source', () => {
        expect(originLabel('panel')).toBe('panel');
        expect(originLabel('relay')).toBe('relay');
        expect(originLabel('edge')).toBe('edge env');
    });

    // An unknown origin must still read as something set outside the panel,
    // never as a panel selection - that would hide the exact case this label
    // exists to expose.
    it('falls back to the outside-the-panel wording', () => {
        expect(originLabel('something-new')).toBe('edge env');
        expect(originLabel('')).toBe('edge env');
    });
});

describe('resolveZone', () => {
    const zones = ['dylaris.com', 'eu.dylaris.com', 'example.org'];

    it('picks the longest matching zone', () => {
        expect(resolveZone('*.eu.dylaris.com', zones)).toBe('eu.dylaris.com');
        expect(resolveZone('*.us.dylaris.com', zones)).toBe('dylaris.com');
    });

    it('returns empty when nothing matches', () => {
        expect(resolveZone('*.eu.unmanaged.net', zones)).toBe('');
    });

    it('returns empty with no zones configured', () => {
        expect(resolveZone('*.eu.dylaris.com', [])).toBe('');
    });
});

describe('addZoneTo', () => {
    it('normalises and keeps the list sorted', () => {
        expect(addZoneTo(['b.com'], '  A.com.  ')).toEqual(['a.com', 'b.com']);
    });

    it('ignores a duplicate', () => {
        const zones = ['a.com'];
        expect(addZoneTo(zones, 'A.COM')).toBe(zones);
    });

    it('ignores an empty entry', () => {
        const zones = ['a.com'];
        expect(addZoneTo(zones, '   ')).toBe(zones);
    });
});

describe('removeZoneFrom', () => {
    it('removes the zone', () => {
        const out = removeZoneFrom(['a.com', 'b.net'], {}, 'a.com');
        expect(out.zones).toEqual(['b.net']);
    });

    it('drops per-region names that lose their zone', () => {
        const out = removeZoneFrom(
            ['a.com', 'b.net'],
            { eu: ['*.eu.a.com', '*.eu.b.net'] },
            'a.com',
        );
        expect(out.regionNames.eu).toEqual(['*.eu.b.net']);
    });

    it('drops a region entirely when nothing of it survives', () => {
        const out = removeZoneFrom(['a.com'], { eu: ['*.eu.a.com'] }, 'a.com');
        expect(out.regionNames.eu).toBeUndefined();
        expect(Object.keys(out.regionNames)).toHaveLength(0);
    });

    // The case a check against the removed zone alone gets wrong: with both a
    // parent and a child zone managed, a name inside the child also matches the
    // parent, so removing the parent would delete a name the child still covers.
    it('keeps a name that a remaining nested zone still covers', () => {
        const out = removeZoneFrom(
            ['example.com', 'eu.example.com'],
            { eu: ['*.eu.example.com'] },
            'example.com',
        );
        expect(out.zones).toEqual(['eu.example.com']);
        expect(out.regionNames.eu).toEqual(['*.eu.example.com']);
    });

    it('is case- and dot-insensitive about which zone to remove', () => {
        const out = removeZoneFrom(['a.com', 'b.net'], {}, 'A.com.');
        expect(out.zones).toEqual(['b.net']);
    });

    it('leaves other regions untouched', () => {
        const out = removeZoneFrom(
            ['a.com', 'b.net'],
            { eu: ['*.eu.a.com'], us: ['*.us.b.net'] },
            'a.com',
        );
        expect(out.regionNames.us).toEqual(['*.us.b.net']);
        expect(out.regionNames.eu).toBeUndefined();
    });

    it('is a no-op for a zone that is not in the list', () => {
        const out = removeZoneFrom(['a.com'], { eu: ['*.eu.a.com'] }, 'nope.com');
        expect(out.zones).toEqual(['a.com']);
        expect(out.regionNames.eu).toEqual(['*.eu.a.com']);
    });
});
