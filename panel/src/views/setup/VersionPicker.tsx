"use client";

import React, { useState } from 'react';
import { RefreshCw, Info } from 'lucide-react';

const SOFTWARE_META: Record<string, { desc: string; url: string }> = {
    paper:      { desc: 'High-performance Spigot fork with major optimizations.',    url: 'https://papermc.io' },
    vanilla:    { desc: 'The official, unmodified Minecraft server.',                url: 'https://www.minecraft.net/download/server' },
    fabric:     { desc: 'Lightweight, modern modding toolchain.',                    url: 'https://fabricmc.net' },
    forge:      { desc: 'The long-established large-mod platform.',                  url: 'https://files.minecraftforge.net' },
    neoforge:   { desc: 'Community-driven fork of Forge.',                           url: 'https://neoforged.net' },
    velocity:   { desc: 'Modern, high-performance Minecraft proxy.',                 url: 'https://papermc.io/software/velocity' },
    waterfall:  { desc: 'Legacy BungeeCord fork (succeeded by Velocity).',           url: 'https://papermc.io' },
    bungeecord: { desc: 'The classic Minecraft proxy.',                              url: 'https://www.spigotmc.org/wiki/bungeecord/' },
};

export interface VersionEntry {
    major: string;
    build: string;
}

interface VersionPickerProps {
    software: string;
    onSoftwareChange: (s: string) => void;
    softwareList?: string[];
    allVersions: VersionEntry[];
    selectedMajor: string;
    onMajorChange: (m: string) => void;
    selectedBuild: string;
    onBuildChange: (b: string) => void;
    loading: boolean;
}

/**
 * Compare two dotted-numeric version strings semantically (e.g. "1.20.2", "48.10.1").
 * Returns a negative number if `a` should sort before `b` (i.e. a > b for descending order).
 * Non-numeric or missing segments are treated as 0, sorting them last / lowest.
 */
export function compareVersionsDesc(a: string, b: string): number {
    const aParts = a.split('.').map(s => { const n = parseInt(s, 10); return isNaN(n) ? 0 : n; });
    const bParts = b.split('.').map(s => { const n = parseInt(s, 10); return isNaN(n) ? 0 : n; });
    const len = Math.max(aParts.length, bParts.length);
    for (let i = 0; i < len; i++) {
        const diff = (bParts[i] ?? 0) - (aParts[i] ?? 0);
        if (diff !== 0) return diff;
    }
    return 0;
}

export default function VersionPicker({
    software, onSoftwareChange, softwareList,
    allVersions, selectedMajor, onMajorChange, selectedBuild, onBuildChange,
    loading,
}: VersionPickerProps) {
    const [hoveredInfo, setHoveredInfo] = useState<string | null>(null);

    const isProxySoftware = ['velocity', 'waterfall', 'bungeecord'].includes(software);
    const availableSoftware = softwareList || (isProxySoftware ? ['velocity', 'waterfall', 'bungeecord'] : ['paper', 'vanilla', 'fabric', 'forge', 'neoforge']);

    const majorVersions = [...new Set(allVersions.map(v => v.major))].sort(compareVersionsDesc);
    const buildsForMajor = selectedMajor
        ? allVersions.filter(v => v.major === selectedMajor).map(v => v.build).sort(compareVersionsDesc)
        : [];

    const spinner = (
        <div className="h-full flex items-center justify-center min-h-[120px]">
            <RefreshCw size={20} className="animate-spin text-(--base-07)" />
        </div>
    );

    return (
        <div className="grid grid-cols-3 gap-3 flex-1 min-h-0">
            {/* Software column */}
            <div className="flex flex-col gap-[5px]">
                <label className="input-label">Software</label>
                <div className="flex-1 border border-(--base-03) rounded-md overflow-y-auto bg-(--base-03) p-1 space-y-0.5">
                    {availableSoftware.map(s => {
                        const meta = SOFTWARE_META[s];
                        return (
                            <button key={s} type="button" onClick={() => onSoftwareChange(s)}
                                className={`w-full p-2 rounded-md text-left capitalize text-sm flex items-center justify-between ${
                                    software === s ? 'bg-(--accent) text-white font-medium' : 'hover:bg-(--base-04) text-(--base-07)'
                                }`}>
                                <span>{s}</span>
                                {meta && (
                                    <span className="relative shrink-0 ml-1">
                                        <span
                                            role="button"
                                            aria-label={`Info: ${s}`}
                                            onMouseEnter={() => setHoveredInfo(s)}
                                            onMouseLeave={() => setHoveredInfo(null)}
                                            onClick={e => {
                                                e.stopPropagation();
                                                window.open(meta.url, '_blank', 'noopener,noreferrer');
                                            }}
                                            className={`inline-flex items-center justify-center rounded p-0.5 transition-colors cursor-pointer ${
                                                software === s
                                                    ? 'text-white/60 hover:text-white'
                                                    : 'text-(--base-05) hover:text-(--base-08)'
                                            }`}
                                        >
                                            <Info size={13} />
                                        </span>
                                        {hoveredInfo === s && (
                                            <span className="absolute bottom-full right-0 mb-1.5 z-50 w-48 rounded-md bg-(--base-02) border border-(--base-03) px-2.5 py-1.5 text-[11px] text-(--base-07) leading-snug shadow-lg pointer-events-none whitespace-normal">
                                                {meta.desc}
                                            </span>
                                        )}
                                    </span>
                                )}
                            </button>
                        );
                    })}
                </div>
            </div>

            {/* Major Version column */}
            <div className="flex flex-col gap-[5px]">
                <label className="input-label">Version</label>
                <div className="flex-1 border border-(--base-03) rounded-md overflow-y-auto bg-(--base-03) p-1 space-y-0.5">
                    {loading ? spinner : majorVersions.map(m => (
                        <button key={m} type="button" onClick={() => {
                            onMajorChange(m);
                            const builds = allVersions.filter(v => v.major === m).map(v => v.build).sort(compareVersionsDesc);
                            if (builds.length > 0 && !builds.includes(selectedBuild)) {
                                onBuildChange(builds[0]);
                            }
                        }}
                            className={`w-full p-1.5 text-xs rounded-md text-left font-mono ${
                                selectedMajor === m ? 'bg-(--accent) text-white font-medium' : 'hover:bg-(--base-04) text-(--base-07)'
                            }`}>
                            {m}
                        </button>
                    ))}
                </div>
            </div>

            {/* Build column */}
            <div className="flex flex-col gap-[5px]">
                <label className="input-label">Build</label>
                <div className="flex-1 border border-(--base-03) rounded-md overflow-y-auto bg-(--base-03) p-1 space-y-0.5">
                    {loading ? spinner : buildsForMajor.map(b => (
                        <button key={b} type="button" onClick={() => onBuildChange(b)}
                            className={`w-full p-1 text-xs rounded-md text-left font-mono ${
                                selectedBuild === b ? 'bg-(--accent) text-white font-medium' : 'hover:bg-(--base-04) text-(--base-07)'
                            }`}>
                            {b}
                        </button>
                    ))}
                    {!loading && buildsForMajor.length === 0 && selectedMajor && (
                        <p className="text-xs text-(--base-06) text-center py-4 font-mono">No builds</p>
                    )}
                    {!loading && !selectedMajor && (
                        <p className="text-xs text-(--base-06) text-center py-4 font-mono">Select a version</p>
                    )}
                </div>
            </div>
        </div>
    );
}
