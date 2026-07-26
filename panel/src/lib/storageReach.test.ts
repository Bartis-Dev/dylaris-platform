import { describe, it, expect } from 'vitest';
import {
    initialReachState,
    reachReducer,
    statusLabel,
    statusRemedy,
    type ReachState,
    type ReachStatus,
} from './storageReach';

const start = (total = 3): ReachState => reachReducer(initialReachState, { type: 'start', total });

describe('reachReducer', () => {
    it('starts in verifying with a zeroed counter', () => {
        const s = start(3);
        expect(s.phase).toBe('verifying');
        expect(s.confirmed).toBe(0);
        expect(s.total).toBe(3);
    });

    it('advances the X/N counter on progress', () => {
        let s = start(3);
        s = reachReducer(s, {
            type: 'progress',
            progress: { confirmed: 2, total: 3, done: false, ok: false, results: [] },
        });
        expect(s.confirmed).toBe(2);
        expect(s.total).toBe(3);
        expect(s.phase).toBe('verifying');
    });

    it('switches to slow after 3 seconds but keeps the counter live', () => {
        let s = start(3);
        s = reachReducer(s, { type: 'progress', progress: { confirmed: 1, total: 3, done: false, ok: false, results: [] } });
        s = reachReducer(s, { type: 'tick', elapsedMs: 3200 });
        expect(s.phase).toBe('slow');
        expect(s.confirmed).toBe(1);
    });

    it('does not go slow before 3 seconds', () => {
        const s = reachReducer(start(3), { type: 'tick', elapsedMs: 2900 });
        expect(s.phase).toBe('verifying');
    });

    it('succeeds as soon as every core is confirmed, without waiting out the clock', () => {
        let s = start(3);
        s = reachReducer(s, { type: 'tick', elapsedMs: 4000 });
        expect(s.phase).toBe('slow');
        s = reachReducer(s, {
            type: 'progress',
            progress: { confirmed: 3, total: 3, done: true, ok: true, results: [] },
        });
        expect(s.phase).toBe('success');
        expect(s.confirmed).toBe(3);
    });

    it('does not succeed on a full counter that is not done', () => {
        // done=false means the coordinator is still collecting; treating that
        // as success would commit on an unfinished round.
        const s = reachReducer(start(3), {
            type: 'progress',
            progress: { confirmed: 3, total: 3, done: false, ok: false, results: [] },
        });
        expect(s.phase).toBe('verifying');
    });

    it('fails with the per-core results when the round returns not-ok', () => {
        let s = start(2);
        s = reachReducer(s, {
            type: 'failure',
            message: 'Only 1 of 2 Cores could prove they can read and write this storage.',
            progress: {
                confirmed: 1, total: 2, done: true, ok: false,
                results: [
                    { coreId: 'core-a', status: 'ok' },
                    { coreId: 'core-b', status: 'not-shared', missingPeers: ['core-a'] },
                ],
            },
        });
        expect(s.phase).toBe('failed');
        expect(s.message).toContain('1 of 2');
        expect(s.results).toHaveLength(2);
        expect(s.results[1].status).toBe('not-shared');
    });

    it('fails when the deadline passes without a verdict', () => {
        const s = reachReducer(start(2), { type: 'tick', elapsedMs: 15100 });
        expect(s.phase).toBe('failed');
        expect(s.message).not.toBe('');
    });

    it('retry returns to verifying and clears the previous failure', () => {
        let s = reachReducer(start(2), { type: 'failure', message: 'nope', progress: null });
        expect(s.phase).toBe('failed');
        s = reachReducer(s, { type: 'start', total: 2 });
        expect(s.phase).toBe('verifying');
        expect(s.message).toBe('');
        expect(s.results).toHaveLength(0);
    });

    it('ignores progress once the round has already finished', () => {
        // A late SSE event from the round that just succeeded must not drag
        // the UI back into verifying.
        let s = reachReducer(start(1), {
            type: 'progress',
            progress: { confirmed: 1, total: 1, done: true, ok: true, results: [] },
        });
        expect(s.phase).toBe('success');
        s = reachReducer(s, {
            type: 'progress',
            progress: { confirmed: 0, total: 1, done: false, ok: false, results: [] },
        });
        expect(s.phase).toBe('success');
    });

    it('ignores progress from a different round', () => {
        let s = reachReducer(initialReachState, { type: 'start', total: 2, roundId: 'R1' });
        s = reachReducer(s, {
            type: 'progress',
            roundId: 'R2',
            progress: { confirmed: 2, total: 2, done: true, ok: true, results: [] },
        });
        expect(s.phase).toBe('verifying');
        expect(s.confirmed).toBe(0);
    });
});

describe('status copy', () => {
    const statuses: ReachStatus[] = [
        'ok', 'offline', 'no-response', 'unreachable',
        'write-denied', 'not-shared', 'fingerprint-mismatch', 'cross-write-denied',
    ];

    it('has a label for every taxonomy value', () => {
        for (const s of statuses) {
            expect(statusLabel(s), `label for ${s}`).not.toBe('');
        }
    });

    it('has a remedy for every failing taxonomy value', () => {
        for (const s of statuses.filter(s => s !== 'ok')) {
            expect(statusRemedy(s), `remedy for ${s}`).not.toBe('');
        }
    });

    it('falls back rather than rendering a raw enum for an unknown status', () => {
        expect(statusLabel('something-new' as ReachStatus)).not.toBe('');
    });
});
