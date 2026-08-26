'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import {
    getBuildCompat,
    getServerCompat,
    type CompatMatrix,
    type CompatMode,
} from '@/lib/api/modcompat';
import { API_URL, getAuthHeader } from '@/lib/api/core';

// The state behind the availability check, shared by the modpack builder and a
// modded server. Only the fetch differs; everything around it (mode, target
// picker, the "do not clobber a newer answer with an older one" guard) is the
// same, so it lives here once.

export type CompatSource =
    | { kind: 'build'; packId: number; buildId: number }
    | { kind: 'server'; serverId: number };

interface GameVersionTag {
    version: string;
    version_type: string;
}

export function useCompat(source: CompatSource, current: string) {
    const [mode, setMode] = useState<CompatMode>('all-newer');
    const [specific, setSpecific] = useState('');
    const [data, setData] = useState<CompatMatrix | null>(null);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState('');
    const [versionOptions, setVersionOptions] = useState<string[]>([]);

    // A check can take seconds on a cold cache. Two clicks in a row would
    // otherwise let the first answer land after the second and overwrite it
    // with the older result.
    const ticket = useRef(0);

    const key = source.kind === 'build' ? `${source.packId}/${source.buildId}` : `${source.serverId}`;

    useEffect(() => {
        let cancelled = false;
        (async () => {
            try {
                const res = await fetch(`${API_URL}/modrinth/game-versions`, { headers: getAuthHeader() });
                if (!res.ok) return;
                const tags: GameVersionTag[] = await res.json();
                if (cancelled) return;
                // Releases only, and never the version we are already on.
                setVersionOptions(
                    tags.filter(t => t.version_type === 'release' && t.version !== current).map(t => t.version),
                );
            } catch { /* the picker degrades to empty; the other two modes still work */ }
        })();
        return () => { cancelled = true; };
    }, [current]);

    const refresh = useCallback(async () => {
        if (mode === 'specific' && !specific) {
            setError('Pick a Minecraft version first.');
            return;
        }
        const mine = ++ticket.current;
        setLoading(true);
        setError('');
        const res = source.kind === 'build'
            ? await getBuildCompat(source.packId, source.buildId, mode, specific)
            : await getServerCompat(source.serverId, mode, specific);
        if (mine !== ticket.current) return; // a newer check is already in flight
        setLoading(false);
        if (!res.success || !res.matrix) {
            setError(res.message || 'The availability check failed.');
            return;
        }
        setData(res.matrix);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [key, mode, specific]);

    // Switching mode invalidates the answer on screen: it was computed for a
    // different set of target versions, and leaving it up would read as a
    // result for the new mode.
    useEffect(() => { setData(null); setError(''); }, [mode, specific, key]);

    return { mode, setMode, specific, setSpecific, data, loading, error, versionOptions, refresh };
}
