"use client";

import React from 'react';
import { LifeBuoy, Lock } from 'lucide-react';

// TicketsDisabledBanner is the standard "feature paused" curtain that every
// ticket page renders in place of its real content when featureFlags.tickets
// is false. Keeps wording + layout identical across /tickets, /tickets/new,
// /tickets/inbox and /tickets/[id] so a refresh during a flip looks
// intentional rather than broken.
export default function TicketsDisabledBanner() {
    return (
        <main className="flex-1 flex items-center justify-center p-6">
            <div className="card max-w-md w-full p-8 text-center border border-(--base-03)">
                <div className="w-12 h-12 rounded-md bg-(--base-03) flex items-center justify-center mx-auto mb-4 relative">
                    <LifeBuoy size={24} className="text-(--base-06)" />
                    <span className="absolute -bottom-1 -right-1 w-5 h-5 rounded-full bg-(--base-02) border border-(--base-04) flex items-center justify-center">
                        <Lock size={10} className="text-(--warning-light)" />
                    </span>
                </div>
                <h1 className="text-base font-display font-bold text-(--base-09)">Ticket system is disabled</h1>
                <p className="text-sm text-(--base-06) mt-2">
                    The ticket system is disabled by the platform admin. Reach out to your admin if you need support.
                </p>
            </div>
        </main>
    );
}
