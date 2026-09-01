import type { Entitlement, EntitlementResponse } from '@/lib/api/entitlement';

/**
 * Turning an entitlement response into the state the admin UI renders.
 *
 * One function because there were two, written out field by field - the load
 * and the after-a-grant refresh - and both listed the fields they knew about
 * when they were written. The per-kind deadlines were added later, so neither
 * carried them: the rows read grantByonExpiresAt, nothing ever set it, and
 * every granted tenant showed as "Not granted" while the grant itself was
 * working perfectly. The admin could see the effect of a grant nowhere.
 *
 * So it SPREADS rather than enumerating. A field added to the response reaches
 * the UI without anyone remembering to add it here, which is the property the
 * old shape lacked. Only the three that need a defined default are named.
 */
export function entitlementOf(r: EntitlementResponse): Entitlement {
    return {
        ...r,
        byon: !!r.byon,
        routeOnly: !!r.routeOnly,
        source: r.source || 'none',
    };
}

/**
 * How long is left on a grant, for the admin who is deciding whether to extend.
 *
 * A date alone answers "when" and not "how soon", and "how soon" is the
 * question being asked at that moment. Rounded UP: with eight hours left the
 * honest answer is "1 day", not "0".
 *
 * Returns null when there is nothing to say - no grant, or an unparseable
 * value, in which case the caller shows the plain state rather than a wrong
 * number.
 */
export function daysLeft(expiresAt: string | undefined, now: Date = new Date()): number | null {
    if (!expiresAt) return null;
    const end = new Date(expiresAt).getTime();
    if (!Number.isFinite(end)) return null;
    const ms = end - now.getTime();
    if (ms <= 0) return 0;
    return Math.ceil(ms / 86_400_000);
}

/** "3 days left", "1 day left", "expired". */
export function formatDaysLeft(expiresAt: string | undefined, now: Date = new Date()): string {
    const d = daysLeft(expiresAt, now);
    if (d === null) return '';
    if (d === 0) return 'expired';
    return `${d} day${d === 1 ? '' : 's'} left`;
}
