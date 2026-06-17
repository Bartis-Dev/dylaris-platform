// Shared helpers for parsing and compacting Linux cpuset lists ("0-3,8").
// Used by CpuPinningControl (per-server pinning) and NodesTab (per-node pool).

// Parse a Linux cpuset list ("0-3,8") into a sorted, de-duplicated number[].
export function parseCpuset(spec: string): number[] {
    const out = new Set<number>();
    for (const part of spec.split(',')) {
        const t = part.trim();
        if (!t) continue;
        const dash = t.indexOf('-');
        if (dash > 0) {
            const lo = Number(t.slice(0, dash));
            const hi = Number(t.slice(dash + 1));
            if (Number.isInteger(lo) && Number.isInteger(hi) && lo <= hi) {
                for (let i = lo; i <= hi; i++) out.add(i);
            }
        } else {
            const n = Number(t);
            if (Number.isInteger(n)) out.add(n);
        }
    }
    return [...out].sort((a, b) => a - b);
}

// Compact a number[] into a Linux cpuset list ("0-3,8"). Inverse of parseCpuset.
export function compactCpuset(ids: number[]): string {
    const sorted = [...new Set(ids)].sort((a, b) => a - b);
    const ranges: string[] = [];
    let start = -1;
    let prev = -1;
    for (const id of sorted) {
        if (start === -1) {
            start = id;
            prev = id;
        } else if (id === prev + 1) {
            prev = id;
        } else {
            ranges.push(start === prev ? `${start}` : `${start}-${prev}`);
            start = id;
            prev = id;
        }
    }
    if (start !== -1) ranges.push(start === prev ? `${start}` : `${start}-${prev}`);
    return ranges.join(',');
}
