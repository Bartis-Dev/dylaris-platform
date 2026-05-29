"use client";

import React from 'react';
import { Activity } from 'lucide-react';

// Placeholder until Phase 11 ships Spark integration. Kept here so the
// sub-tab strip has a valid route to point at — clicking it lands on a
// friendly "coming soon" screen instead of a 404.
export default function ServerConfigProfilingPage() {
    return (
        <div className="max-w-2xl">
            <div className="card p-6 flex items-start gap-4">
                <div className="w-10 h-10 rounded-md bg-(--accent-ghost) flex items-center justify-center shrink-0">
                    <Activity size={18} className="text-(--accent-light)" />
                </div>
                <div className="min-w-0">
                    <h2 className="text-base font-display font-semibold text-(--base-09) mb-1">Profiling</h2>
                    <p className="text-sm text-(--base-07)">
                        Spark profiler integration arrives in Phase 11 — enable per-server,
                        start/stop profiles from here, view results inline. For now this
                        sub-tab is a stub.
                    </p>
                </div>
            </div>
        </div>
    );
}
