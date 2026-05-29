"use client";

import React from 'react';
import { Cloud } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';

// CoreRegionChip displays the region of whichever Core this session is
// currently talking to. Auto-hides when:
//   - the coreInfo hasn't loaded yet
//   - the platform has only the 'default' region (single-region UX)
// Hover shows the Core ID for support — useful when DNS-georouting bounces
// between two Cores in the same region and you want to know which exact
// instance you hit.
export default function CoreRegionChip() {
    const { coreInfo, regions } = useAppData();
    if (!coreInfo) return null;
    if (regions.length <= 1 && coreInfo.region === 'default') return null;

    const entry = regions.find(r => r.id === coreInfo.region);
    const label = entry?.displayName || coreInfo.region;
    const color = entry?.color;

    return (
        <span
            title={`Core: ${coreInfo.coreId}`}
            className="hidden md:inline-flex items-center gap-1.5 px-2 py-1 rounded-md text-[10px] font-mono uppercase tracking-[0.08em] text-(--base-07) border border-(--base-03) bg-(--base-02) mr-2"
        >
            {color
                ? <span className="inline-block w-1.5 h-1.5 rounded-full" style={{ background: color }} />
                : <Cloud size={11} className="text-(--base-06)" />}
            <span className="truncate max-w-[100px]">{label}</span>
            <span className="text-(--base-06) hidden lg:inline">core</span>
        </span>
    );
}
