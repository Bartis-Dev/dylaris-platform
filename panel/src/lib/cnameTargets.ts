/**
 * The custom-domain CNAME setting is a single LABEL (e.g. "route"), not a full
 * domain. It is combined with every hoster base domain, so one setting produces
 * one target per region: route.eu.example.com, route.us-east.example.com, ...
 *
 * A user bringing their own domain picks whichever target matches the region
 * they want, and that choice is what decides which edge group answers their
 * players.
 */
export function cnameTargetsFor(label: string, hosterDomains: { domain: string }[]): string[] {
    const l = label.trim().toLowerCase();
    if (l === '') return [];
    const out: string[] = [];
    const seen = new Set<string>();
    for (const h of hosterDomains) {
        const base = (h?.domain ?? '').trim().toLowerCase();
        if (base === '') continue;
        const fqdn = `${l}.${base}`;
        // Two hoster entries can differ only by validation mode, which would
        // otherwise show the same target twice.
        if (seen.has(fqdn)) continue;
        seen.add(fqdn);
        out.push(fqdn);
    }
    return out;
}
