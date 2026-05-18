"use client";

import React from 'react';
import { RefreshCw } from 'lucide-react';

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

export default function VersionPicker({
    software, onSoftwareChange, softwareList,
    allVersions, selectedMajor, onMajorChange, selectedBuild, onBuildChange,
    loading,
}: VersionPickerProps) {
    const isProxySoftware = ['velocity', 'waterfall', 'bungeecord'].includes(software);
    const availableSoftware = softwareList || (isProxySoftware ? ['velocity', 'waterfall', 'bungeecord'] : ['paper', 'vanilla', 'fabric', 'forge', 'neoforge']);

    const majorVersions = [...new Set(allVersions.map(v => v.major))];
    const buildsForMajor = selectedMajor
        ? allVersions.filter(v => v.major === selectedMajor).map(v => v.build)
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
                    {availableSoftware.map(s => (
                        <button key={s} type="button" onClick={() => onSoftwareChange(s)}
                            className={`w-full p-2 rounded-md text-left capitalize text-sm ${
                                software === s ? 'bg-(--accent) text-white font-medium' : 'hover:bg-(--base-04) text-(--base-07)'
                            }`}>
                            {s}
                        </button>
                    ))}
                </div>
            </div>

            {/* Major Version column */}
            <div className="flex flex-col gap-[5px]">
                <label className="input-label">Version</label>
                <div className="flex-1 border border-(--base-03) rounded-md overflow-y-auto bg-(--base-03) p-1 space-y-0.5">
                    {loading ? spinner : majorVersions.map(m => (
                        <button key={m} type="button" onClick={() => {
                            onMajorChange(m);
                            const builds = allVersions.filter(v => v.major === m).map(v => v.build);
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
