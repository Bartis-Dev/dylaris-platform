"use client";

import React, {
    createContext,
    useCallback,
    useContext,
    useEffect,
    useMemo,
    useRef,
    useState,
} from 'react';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type UploadStatus =
    | 'running'
    | 'retrying'
    | 'cancelling'
    | 'cancelled'
    | 'done'
    | 'error';

export interface UploadJob {
    id: string;
    filename: string;
    size: number;
    serverUuid: string;
    serverName?: string;
    path: string;
    status: UploadStatus;
    sentBytes: number;
    // Sliding-window samples used to compute the speed shown in the UI.
    // Without a window the speed would either look like "average since
    // start" (way too smooth) or "instant" (way too jittery). A 4-second
    // window strikes a balance.
    samples: { at: number; sent: number }[];
    startedAt: number;
    finishedAt?: number;
    errorMessage?: string;
    cancel?: () => void;
}

interface UploadManagerCtx {
    jobs: UploadJob[];
    activeCount: number;
    hasActive: boolean;
    aggregateProgress: number; // 0..1 across all running jobs
    addJob: (job: Omit<UploadJob, 'samples' | 'startedAt' | 'sentBytes' | 'status'> & { status?: UploadStatus }) => void;
    updateJob: (id: string, patch: Partial<UploadJob>) => void;
    recordProgress: (id: string, sentBytes: number) => void;
    finishJob: (id: string, status: 'done' | 'error' | 'cancelled', message?: string) => void;
    removeJob: (id: string) => void;
    clearFinished: () => void;
    requestCancel: (id: string) => void;
    // Returns true when at least one job for the given server is running.
    // Components use this to disable destructive actions on that target.
    isServerLocked: (serverUuid: string) => boolean;
}

const SAMPLE_WINDOW_MS = 4000;
const MAX_SAMPLES = 32;

const Ctx = createContext<UploadManagerCtx | null>(null);

// ---------------------------------------------------------------------------
// Provider
// ---------------------------------------------------------------------------

export function UploadManagerProvider({ children }: { children: React.ReactNode }) {
    const [jobs, setJobs] = useState<UploadJob[]>([]);
    const jobsRef = useRef<UploadJob[]>([]);
    jobsRef.current = jobs;

    const addJob: UploadManagerCtx['addJob'] = useCallback(job => {
        setJobs(prev => {
            const next: UploadJob = {
                ...job,
                sentBytes: 0,
                samples: [],
                startedAt: Date.now(),
                status: job.status ?? 'running',
            };
            // De-dupe by id (last wins) so a re-run of the same uploadID
            // doesn't accumulate ghosts.
            return [...prev.filter(j => j.id !== next.id), next];
        });
    }, []);

    const updateJob: UploadManagerCtx['updateJob'] = useCallback((id, patch) => {
        setJobs(prev => prev.map(j => (j.id === id ? { ...j, ...patch } : j)));
    }, []);

    const recordProgress: UploadManagerCtx['recordProgress'] = useCallback((id, sentBytes) => {
        const now = Date.now();
        setJobs(prev => prev.map(j => {
            if (j.id !== id) return j;
            // Keep the last MAX_SAMPLES inside the rolling SAMPLE_WINDOW_MS
            // and append the fresh one.
            const cutoff = now - SAMPLE_WINDOW_MS;
            const trimmed = j.samples.filter(s => s.at >= cutoff);
            const samples = [...trimmed, { at: now, sent: sentBytes }];
            while (samples.length > MAX_SAMPLES) samples.shift();
            return { ...j, sentBytes, samples };
        }));
    }, []);

    const finishJob: UploadManagerCtx['finishJob'] = useCallback((id, status, message) => {
        setJobs(prev => prev.map(j => {
            if (j.id !== id) return j;
            return {
                ...j,
                status,
                finishedAt: Date.now(),
                errorMessage: status === 'error' ? (message ?? j.errorMessage) : j.errorMessage,
                // Snap sentBytes to size on success so the UI shows 100%
                // even if the last progress callback rounded down.
                sentBytes: status === 'done' ? j.size : j.sentBytes,
            };
        }));
    }, []);

    const removeJob: UploadManagerCtx['removeJob'] = useCallback(id => {
        setJobs(prev => prev.filter(j => j.id !== id));
    }, []);

    const clearFinished: UploadManagerCtx['clearFinished'] = useCallback(() => {
        setJobs(prev => prev.filter(j => j.status === 'running' || j.status === 'cancelling'));
    }, []);

    const requestCancel: UploadManagerCtx['requestCancel'] = useCallback(id => {
        const job = jobsRef.current.find(j => j.id === id);
        if (!job) return;
        if (job.status !== 'running' && job.status !== 'retrying') return;
        // Flip to cancelling immediately so the UI reflects the intent
        // even before the Go side acknowledges.
        setJobs(prev => prev.map(j => (j.id === id ? { ...j, status: 'cancelling' as UploadStatus } : j)));
        try {
            job.cancel?.();
        } catch {
            /* best-effort */
        }
    }, []);

    // Auto-clear finished jobs after a short grace so the navbar widget
    // doesn't accumulate completed entries forever. Errors stick around
    // longer so the user has time to read the message.
    useEffect(() => {
        if (jobs.length === 0) return;
        const now = Date.now();
        const expiresOldestAt = jobs
            .filter(j => j.status === 'done' || j.status === 'cancelled' || j.status === 'error')
            .map(j => (j.finishedAt ?? now) + (j.status === 'error' ? 15000 : 5000))
            .sort((a, b) => a - b)[0];
        if (!expiresOldestAt) return;
        const ms = Math.max(0, expiresOldestAt - now);
        const t = setTimeout(() => {
            setJobs(prev => prev.filter(j => {
                if (j.status === 'running' || j.status === 'cancelling') return true;
                const ttl = j.status === 'error' ? 15000 : 5000;
                return now + ms < (j.finishedAt ?? now) + ttl;
            }));
        }, ms + 50);
        return () => clearTimeout(t);
    }, [jobs]);

    // "Active" counts retrying jobs too — they're still in flight from the
    // user's perspective, just paused mid-chunk while we re-handshake.
    const isLive = (s: UploadStatus) => s === 'running' || s === 'retrying' || s === 'cancelling';
    const activeCount = jobs.filter(j => isLive(j.status)).length;

    const aggregateProgress = useMemo(() => {
        const running = jobs.filter(j => isLive(j.status));
        if (running.length === 0) return 0;
        const sentTotal = running.reduce((s, j) => s + j.sentBytes, 0);
        const sizeTotal = running.reduce((s, j) => s + Math.max(j.size, 1), 0);
        return Math.max(0, Math.min(1, sentTotal / sizeTotal));
    }, [jobs]);

    const isServerLocked = useCallback((serverUuid: string) => {
        return jobs.some(j => j.serverUuid === serverUuid && isLive(j.status));
    }, [jobs]);

    const value: UploadManagerCtx = {
        jobs,
        activeCount,
        hasActive: activeCount > 0,
        aggregateProgress,
        addJob,
        updateJob,
        recordProgress,
        finishJob,
        removeJob,
        clearFinished,
        requestCancel,
        isServerLocked,
    };

    return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

// ---------------------------------------------------------------------------
// Hooks
// ---------------------------------------------------------------------------

export function useUploadManager(): UploadManagerCtx {
    const v = useContext(Ctx);
    if (!v) {
        // The provider is mounted in (authed)/layout.tsx so any authed page
        // sees a real context. Returning a no-op is safer than crashing if
        // a stray render hits before mount.
        return noopManager;
    }
    return v;
}

export function useServerUploadLock(serverUuid: string | undefined): boolean {
    const mgr = useUploadManager();
    if (!serverUuid) return false;
    return mgr.isServerLocked(serverUuid);
}

// jobSpeedBps reports the rolling-window speed of a job in bytes/sec.
// Computed lazily by readers rather than stored on the job so we don't
// pay a re-render on every chunk just to refresh a derived number.
export function jobSpeedBps(job: UploadJob): number {
    if (job.status !== 'running' && job.status !== 'retrying' && job.status !== 'cancelling') return 0;
    if (job.samples.length < 2) return 0;
    const first = job.samples[0];
    const last = job.samples[job.samples.length - 1];
    const dt = (last.at - first.at) / 1000;
    if (dt <= 0) return 0;
    return Math.max(0, (last.sent - first.sent) / dt);
}

export function jobEtaSeconds(job: UploadJob): number | null {
    const bps = jobSpeedBps(job);
    if (bps <= 0) return null;
    const remaining = Math.max(0, job.size - job.sentBytes);
    if (remaining === 0) return 0;
    return Math.round(remaining / bps);
}

const noopManager: UploadManagerCtx = {
    jobs: [],
    activeCount: 0,
    hasActive: false,
    aggregateProgress: 0,
    addJob: () => { /* noop */ },
    updateJob: () => { /* noop */ },
    recordProgress: () => { /* noop */ },
    finishJob: () => { /* noop */ },
    removeJob: () => { /* noop */ },
    clearFinished: () => { /* noop */ },
    requestCancel: () => { /* noop */ },
    isServerLocked: () => false,
};

// ---------------------------------------------------------------------------
// Window bridge
// ---------------------------------------------------------------------------
//
// The Wails-side upload adapter runs in a context where it can't import the
// React provider directly (it's a plain ts module with no React state). We
// expose a tiny window-attached bridge so the adapter can register/update/
// finish jobs without coupling.

declare global {
    interface Window {
        __dylarisUploadBridge?: {
            addJob: UploadManagerCtx['addJob'];
            recordProgress: UploadManagerCtx['recordProgress'];
            finishJob: UploadManagerCtx['finishJob'];
            updateJob: UploadManagerCtx['updateJob'];
            isServerLocked: UploadManagerCtx['isServerLocked'];
        };
    }
}

export function UploadManagerBridge() {
    const mgr = useUploadManager();
    useEffect(() => {
        if (typeof window === 'undefined') return;
        window.__dylarisUploadBridge = {
            addJob: mgr.addJob,
            recordProgress: mgr.recordProgress,
            finishJob: mgr.finishJob,
            updateJob: mgr.updateJob,
            isServerLocked: mgr.isServerLocked,
        };
        return () => {
            delete window.__dylarisUploadBridge;
        };
    }, [mgr]);
    return null;
}

// isServerUploadLocked is the non-React mirror of useServerUploadLock —
// callable from adapters that need to refuse write ops while an upload
// is still streaming bytes into the same server's directory.
export function isServerUploadLocked(serverUuid: string): boolean {
    if (typeof window === 'undefined') return false;
    return window.__dylarisUploadBridge?.isServerLocked(serverUuid) ?? false;
}

export function reportUploadStart(input: Parameters<UploadManagerCtx['addJob']>[0]): void {
    if (typeof window === 'undefined') return;
    window.__dylarisUploadBridge?.addJob(input);
}

export function reportUploadProgress(id: string, sentBytes: number): void {
    if (typeof window === 'undefined') return;
    window.__dylarisUploadBridge?.recordProgress(id, sentBytes);
}

export function reportUploadFinish(id: string, status: 'done' | 'error' | 'cancelled', message?: string): void {
    if (typeof window === 'undefined') return;
    window.__dylarisUploadBridge?.finishJob(id, status, message);
}

export function reportUploadUpdate(id: string, patch: Partial<UploadJob>): void {
    if (typeof window === 'undefined') return;
    window.__dylarisUploadBridge?.updateJob(id, patch);
}
