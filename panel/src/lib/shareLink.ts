// shareLinkExpired: null/absent means the link never expires, which is a
// different thing from "expired" and has to stay so - the column behind it is
// nullable for exactly that reason, and Core reads a NULL as "no deadline".
// An unparseable value is treated as no deadline too: a display helper must
// never be the thing that declares a working link dead.
export function shareLinkExpired(iso: string | null | undefined, now: number = Date.now()): boolean {
    if (!iso) return false;
    const t = new Date(iso).getTime();
    if (Number.isNaN(t)) return false;
    return t <= now;
}
