"use client";

import React from 'react';
import { Network } from 'lucide-react';

// Placeholder for BungeeCord/Velocity config editing. Only reachable when
// serverType=='proxy' — the sub-tab strip in the parent layout hides it
// otherwise. Editor lands in a later phase (config.yml is YAML, needs a
// careful per-mode editor and isn't on the P8 critical path).
export default function ServerConfigProxyPage() {
    return (
        <div className="max-w-2xl">
            <div className="card p-6 flex items-start gap-4">
                <div className="w-10 h-10 rounded-md bg-(--accent-ghost) flex items-center justify-center shrink-0">
                    <Network size={18} className="text-(--accent-light)" />
                </div>
                <div className="min-w-0">
                    <h2 className="text-base font-display font-semibold text-(--base-09) mb-1">Proxy Configuration</h2>
                    <p className="text-sm text-(--base-07)">
                        BungeeCord / Velocity config editor lands in a later phase.
                        For now use the Files tab to edit <code className="font-mono text-xs">config.yml</code> directly.
                    </p>
                </div>
            </div>
        </div>
    );
}
