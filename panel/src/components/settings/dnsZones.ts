// Zone discovery has FOUR outcomes and they lead to four different remedies.
// Merging "the call failed" into "no zones found" is the trap this exists to
// prevent: a transient network error reported as a permission problem sends an
// admin to widen a token that was already fine.
//
// Kept out of the component so the mapping is unit-testable - the panel has
// vitest only, no DOM test tooling.

export type DNSZoneState = 'ok' | 'empty' | 'unsupported' | 'error';

// isInZone mirrors Core's ResolveZone matching rule. The label boundary is the
// point: without it "evil-dylaris.com" reads as being inside "dylaris.com", and
// the panel would offer to manage a name in a zone it does not belong to.
export function isInZone(name: string, zone: string): boolean {
    const n = normalizeDNSName(name);
    const z = normalizeDNSName(zone);
    if (!n || !z) return false;
    return n === z || n.endsWith(`.${z}`);
}

// resolveZone picks the managed zone a name belongs to, longest match first so a
// delegated sub-zone beats its parent. Returns '' when no zone matches, which is
// the case the screen has to surface: the name will never be written.
export function resolveZone(name: string, zones: string[]): string {
    let best = '';
    for (const zone of zones) {
        if (!isInZone(name, zone)) continue;
        const z = normalizeDNSName(zone);
        if (z.length > best.length) best = z;
    }
    return best;
}

export function normalizeDNSName(s: string): string {
    return s.trim().toLowerCase().replace(/\.$/, '');
}

// originLabel names where a managed record came from. 'edge env' and 'relay' are
// spelled out because both are set outside the panel - an admin looking for why
// their selection is not winning needs to know which file to go edit.
export function originLabel(origin: string): string {
    switch (origin) {
        case 'panel':
            return 'panel';
        case 'relay':
            return 'relay';
        default:
            return 'edge env';
    }
}

export interface DNSZonesResponse {
    success: boolean;
    state: DNSZoneState;
    zones: string[];
    error?: string;
}

export interface ZoneHint {
    tone: 'info' | 'warn' | 'error';
    message: string;
    // Whether the admin should fall back to typing the zone by hand. True for
    // every state except a successful listing.
    manualEntry: boolean;
}

export function zoneHint(res: DNSZonesResponse): ZoneHint | null {
    switch (res.state) {
        case 'ok':
            return null; // the picker speaks for itself
        case 'unsupported':
            return {
                tone: 'info',
                message: 'This provider cannot list zones. Enter the domain manually.',
                manualEntry: true,
            };
        case 'empty':
            return {
                tone: 'warn',
                message:
                    'No zones are visible to this token. Widen its permissions, or enter the domain manually.',
                manualEntry: true,
            };
        case 'error':
            return {
                tone: 'error',
                // The raw provider error, deliberately: libdns does not normalise
                // errors, so only the original text distinguishes a 403 from a
                // timeout. Suppressing it would leave the admin guessing.
                message: res.error
                    ? `Could not list zones: ${res.error}`
                    : 'Could not list zones.',
                manualEntry: true,
            };
    }
}
