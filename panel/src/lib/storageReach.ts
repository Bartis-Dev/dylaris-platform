// Shared-storage reachability: the round state machine and the operator-facing
// copy for every taxonomy value.
//
// Kept as a pure module (no React, no fetch) because the panel's vitest setup
// runs in `environment: 'node'` and includes only `src/**/*.test.ts` - the same
// reason selectKeyboard.ts exists next to Select.tsx.

/** Mirrors the Go storagereach.Status wire values exactly. */
export type ReachStatus =
    | 'ok'
    | 'offline'
    | 'no-response'
    | 'unreachable'
    | 'write-denied'
    | 'not-shared'
    | 'fingerprint-mismatch'
    | 'cross-write-denied';

export interface CoreReachResult {
    coreId: string;
    status: ReachStatus;
    missingPeers?: string[];
    deniedPeers?: string[];
    detail?: string;
}

export interface RoundProgress {
    confirmed: number;
    total: number;
    done: boolean;
    ok: boolean;
    results: CoreReachResult[];
}

export type ReachPhase = 'idle' | 'verifying' | 'slow' | 'success' | 'failed';

export interface ReachState {
    phase: ReachPhase;
    roundId: string | null;
    confirmed: number;
    total: number;
    results: CoreReachResult[];
    message: string;
}

export type ReachEvent =
    | { type: 'start'; total: number; roundId?: string }
    | { type: 'progress'; progress: RoundProgress; roundId?: string }
    | { type: 'tick'; elapsedMs: number }
    | { type: 'failure'; message: string; progress: RoundProgress | null };

/** Matches the Core-side round cap; the UI must not outlive the round. */
export const ROUND_DEADLINE_MS = 15_000;
/** After this, the copy acknowledges the wait instead of leaving a bare spinner. */
export const SLOW_AFTER_MS = 3_000;

export const initialReachState: ReachState = {
    phase: 'idle',
    roundId: null,
    confirmed: 0,
    total: 0,
    results: [],
    message: '',
};

const TIMEOUT_MESSAGE =
    'The Cores did not all confirm access within 15 seconds. The settings were not saved.';

function settled(phase: ReachPhase): boolean {
    return phase === 'success' || phase === 'failed';
}

export function reachReducer(state: ReachState, event: ReachEvent): ReachState {
    switch (event.type) {
        case 'start':
            return {
                phase: 'verifying',
                roundId: event.roundId ?? null,
                confirmed: 0,
                total: event.total,
                results: [],
                message: '',
            };

        case 'progress': {
            // A late event from a round that already settled must not drag the
            // UI back into verifying, and an event from a DIFFERENT round is
            // not about this one at all.
            if (settled(state.phase)) return state;
            if (state.roundId && event.roundId && event.roundId !== state.roundId) return state;

            const p = event.progress;
            const next: ReachState = {
                ...state,
                roundId: state.roundId ?? event.roundId ?? null,
                confirmed: p.confirmed,
                total: p.total || state.total,
                results: p.results ?? [],
            };
            // Success the moment every Core is confirmed AND the round is
            // finished: waiting out the clock would add up to 15s of nothing.
            if (p.done && p.ok) {
                return { ...next, phase: 'success', message: '' };
            }
            if (p.done && !p.ok) {
                return { ...next, phase: 'failed', message: TIMEOUT_MESSAGE };
            }
            return next;
        }

        case 'tick': {
            if (settled(state.phase)) return state;
            if (event.elapsedMs >= ROUND_DEADLINE_MS) {
                return { ...state, phase: 'failed', message: TIMEOUT_MESSAGE };
            }
            if (event.elapsedMs >= SLOW_AFTER_MS && state.phase === 'verifying') {
                return { ...state, phase: 'slow' };
            }
            return state;
        }

        case 'failure':
            return {
                ...state,
                phase: 'failed',
                message: event.message,
                confirmed: event.progress?.confirmed ?? state.confirmed,
                total: event.progress?.total ?? state.total,
                results: event.progress?.results ?? state.results,
            };

        default:
            return state;
    }
}

const LABELS: Record<string, string> = {
    'ok': 'Confirmed',
    'offline': 'Offline',
    'no-response': 'No response',
    'unreachable': 'Storage unreachable',
    'write-denied': 'Write denied',
    'not-shared': 'Storage not shared',
    'fingerprint-mismatch': 'Different backend',
    'cross-write-denied': 'Cross-write denied',
};

const REMEDIES: Record<string, string> = {
    'offline': 'This Core is not running. It will verify its own access the next time it starts.',
    'no-response': 'This Core is online but did not answer in time. Retry; if it keeps failing, check its logs.',
    'unreachable': 'This Core cannot reach the storage at all. Check that the mount exists on that host, or that it can reach the S3 endpoint.',
    'write-denied': 'This Core can read the storage but cannot write to it. Check for a read-only mount or missing write permission.',
    'not-shared': 'This Core cannot see files written by the others, so the storage is not actually shared. Use S3, or mount the same filesystem at this path on every host.',
    'fingerprint-mismatch': 'This Core is pointed at a different backend than the rest of the deployment. Restart it so it picks up the current settings.',
    'cross-write-denied': 'This Core cannot write into files owned by the others. Check the share user mapping (NFS root_squash / uid mapping).',
};

/**
 * A status with no copy would render as a raw enum value, which is exactly the
 * kind of silent, unexplained failure this feature exists to remove - so both
 * lookups fall back rather than returning empty.
 */
export function statusLabel(status: ReachStatus): string {
    return LABELS[status] ?? 'Unknown state';
}

export function statusRemedy(status: ReachStatus): string {
    if (status === 'ok') return '';
    return REMEDIES[status] ?? 'This Core could not confirm access to the shared storage.';
}
