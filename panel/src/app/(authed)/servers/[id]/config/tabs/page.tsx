"use client";

import React from 'react';
import { LayoutGrid } from 'lucide-react';

// Placeholder until Phase 13 ships the custom-tabs system (Minimap, BlueMap,
// arbitrary in-container HTTP UIs surfaced as panel tabs).
export default function ServerConfigTabsPage() {
    return (
        <div className="max-w-2xl">
            <div className="card p-6 flex items-start gap-4">
                <div className="w-10 h-10 rounded-md bg-(--accent-ghost) flex items-center justify-center shrink-0">
                    <LayoutGrid size={18} className="text-(--accent-light)" />
                </div>
                <div className="min-w-0">
                    <h2 className="text-base font-display font-semibold text-(--base-09) mb-1">Custom Tabs</h2>
                    <p className="text-sm text-(--base-07)">
                        Phase 13 will let you surface in-container HTTP UIs
                        (Minimap, BlueMap, custom plugin dashboards) as panel tabs,
                        optionally also reachable via a public route.
                    </p>
                </div>
            </div>
        </div>
    );
}
