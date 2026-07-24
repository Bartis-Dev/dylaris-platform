// Pure helpers for grouping CPU cores by their L3 cache domain (to surface AMD
// 3D V-Cache / X3D CCDs) and formatting per-core display values. Consumed by
// CpuPinningControl. Mirrors the cpuset.ts pattern: pure, unit-tested, no I/O.

import type { CpuCore } from './api/types';

export interface CacheGroup {
    cacheGroup: number; // the cores' cacheGroup index; -1 for the synthetic single group
    l3KB: number;       // representative L3 size for the group (0 when unknown)
    isVCache: boolean;  // true only for the max-L3 group when >1 distinct non-zero L3 exists
    cores: CpuCore[];
}

// Group cores by their L3 cache-domain index. Cores sharing a cacheGroup value form
// one group; groups are ordered by ascending cacheGroup (so -1, i.e. the synthetic
// "no cache data" group, sorts first and stands alone). A group is flagged isVCache
// only when it holds the maximum l3KB AND there is more than one distinct non-zero
// l3KB across all cores (a true hetero-cache part such as a dual-CCD 7950X3D). A
// uniform-cache part (single CCD, Intel, VM) collapses to one group with no flag.
export function groupCoresByCache(cores: CpuCore[]): CacheGroup[] {
    if (cores.length === 0) return [];

    const buckets = new Map<number, CpuCore[]>();
    for (const c of cores) {
        const list = buckets.get(c.cacheGroup);
        if (list) list.push(c);
        else buckets.set(c.cacheGroup, [c]);
    }

    const distinctL3 = new Set<number>();
    for (const c of cores) {
        if (c.l3KB > 0) distinctL3.add(c.l3KB);
    }
    const hasVCache = distinctL3.size > 1;
    let maxL3 = 0;
    if (hasVCache) {
        for (const v of distinctL3) if (v > maxL3) maxL3 = v;
    }

    return [...buckets.keys()]
        .sort((a, b) => a - b)
        .map(key => {
            const groupCores = buckets.get(key)!;
            const l3KB = groupCores.reduce((m, c) => Math.max(m, c.l3KB), 0);
            return {
                cacheGroup: key,
                l3KB,
                isVCache: hasVCache && l3KB === maxL3,
                cores: groupCores,
            };
        });
}

// Format an L3 size (KB) as a compact label: "96MB" / "32MB" / "512KB". A size
// that is a whole number of MB renders as MB, otherwise sub-MB renders as KB.
// Zero or negative renders as "".
export function formatL3(kb: number): string {
    if (kb <= 0) return '';
    if (kb >= 1024 && kb % 1024 === 0) return `${kb / 1024}MB`;
    if (kb >= 1024) return `${Math.round(kb / 1024)}MB`;
    return `${kb}KB`;
}

// Format a max clock (MHz) as one-decimal GHz ("4.8G"); "" when unknown (<= 0).
export function formatClock(mhz: number): string {
    if (mhz <= 0) return '';
    return `${(mhz / 1000).toFixed(1)}G`;
}
