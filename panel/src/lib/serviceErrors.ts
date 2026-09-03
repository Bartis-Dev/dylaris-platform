// The service error streams (dylaris:errors:<service>:<instance>) that core,
// edge, link, hub, beam, node and warp write to.
//
// core is the newest and the one that was missing longest: it runs every
// periodic job - dunning, the backup scheduler, the retention sweeps - and
// reported their failures only to its own stdout, which does not survive the
// container's next deploy.
//
// These are not a nice-to-have. Some failures are reported HERE AND NOWHERE
// ELSE, because the component that can see them is not the component the
// operator would think to look at: a route whose target refuses the connection
// is a clean, successful proxy from the edge's point of view, and the only
// line naming the failure is the link's `failed to connect to <target>`. That
// exact case cost a debugging session while `/api/infrastructure/overview` was
// already carrying the answer in a field the view dropped on the floor.

export interface ServiceErrorEntry {
    ts: string;
    level: string;   // ERROR | WARN | INFO
    source: string;  // producer-side label, e.g. "stream", "splice-handler"
    message: string;
}

export interface FlatServiceError extends ServiceErrorEntry {
    service: string;
}

/**
 * Flattens the per-service map the API returns into one list, newest first.
 *
 * One list rather than a section per service: the question being asked is
 * "what is broken right now", and the answer is usually one component. Which
 * one it is rides along as a field.
 *
 * Entries with an unparseable timestamp sort last rather than being dropped -
 * a producer writing a malformed ts is itself worth seeing.
 */
export function flattenServiceErrors(
    byService: Record<string, ServiceErrorEntry[]> | null | undefined,
): FlatServiceError[] {
    if (!byService) return [];
    const out: FlatServiceError[] = [];
    for (const [service, entries] of Object.entries(byService)) {
        if (!Array.isArray(entries)) continue;
        for (const e of entries) {
            if (!e || typeof e.message !== 'string') continue;
            out.push({ ...e, service });
        }
    }
    return out.sort((a, b) => stamp(b) - stamp(a));
}

function stamp(e: ServiceErrorEntry): number {
    const t = Date.parse(e.ts);
    return Number.isNaN(t) ? -Infinity : t;
}

/**
 * How many entries deserve attention.
 *
 * INFO is carried in the same stream on purpose (a leader election, a
 * certificate coming into management) and it is worth reading, but it must not
 * drive a badge - a permanently non-zero count is a badge nobody looks at.
 */
export function attentionCount(errors: FlatServiceError[]): number {
    return errors.filter(isAttention).length;
}

export function isAttention(e: ServiceErrorEntry): boolean {
    const l = e.level?.toUpperCase();
    return l === 'ERROR' || l === 'WARN';
}
