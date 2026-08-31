"use client";

import { MonitorCog } from 'lucide-react';
import { isWails } from '@/lib/adapters';

/**
 * Setting up a node on the computer the user is sitting at.
 *
 * The entry point belongs here, with the rest of the infrastructure, but the
 * act cannot happen here: writing a compose file and starting a container needs
 * the Wails bindings, and those are deliberately unreachable from the proxied
 * panel. The shell token that gates them is spliced only into Beam's own
 * /__beam/ page, so a panel that had been tampered with cannot start privileged
 * containers on the machine of everyone viewing it.
 *
 * So this card is a door, not a form: it hands off to the Beam screen that owns
 * the privileged half. #deploy is what lands there directly instead of on the
 * settings page.
 *
 * Hidden outside Beam, because the whole premise is that the app is running ON
 * the candidate machine - which is the one thing a browser cannot know about
 * itself. Read at render rather than in an effect: window.go is injected before
 * the first render in Wails, the same assumption BeamDownloadButton documents.
 */
export default function RunNodeHereCard() {
    if (!isWails()) return null;

    return (
        <div className="rounded-md border border-(--base-03) bg-(--base-02) px-3 py-3">
            <div className="flex items-start justify-between gap-3">
                <div className="flex items-start gap-2.5 min-w-0">
                    <MonitorCog size={16} className="mt-0.5 shrink-0 text-(--base-06)" aria-hidden="true" />
                    <div className="min-w-0">
                        <div className="text-sm text-(--base-09)">Run a node on this machine</div>
                        <p className="text-xs text-(--base-06) mt-1">
                            Beam checks Docker on this computer and writes a ready-to-run
                            compose file, so you do not have to copy the steps by hand.
                        </p>
                    </div>
                </div>
                <button
                    type="button"
                    onClick={() => { window.location.href = '/__beam/#deploy'; }}
                    className="shrink-0 px-3 py-1.5 rounded-md text-sm font-medium border border-(--base-04)
                               text-(--base-08) transition-colors hover:bg-(--base-04)/50 hover:text-(--base-09)
                               focus-visible:outline-none focus-visible:ring-(--focus-ring)"
                >
                    Set up
                </button>
            </div>
        </div>
    );
}
