import { describe, it, expect } from 'vitest';
import {
    fleetHealthySummary,
    initialReachState,
    parseStorageReachEvent,
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
        const s = reachReducer(start(2), { type: 'tick', elapsedMs: 20100 });
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

    it('ignores a failure that arrives after the round already succeeded', () => {
        // Simulates the in-flight request's catch handler firing late, after
        // a `progress` event already resolved the round as a success.
        let s = reachReducer(initialReachState, { type: 'start', total: 1, roundId: 'R1' });
        s = reachReducer(s, {
            type: 'progress',
            roundId: 'R1',
            progress: { confirmed: 1, total: 1, done: true, ok: true, results: [] },
        });
        expect(s.phase).toBe('success');
        s = reachReducer(s, { type: 'failure', roundId: 'R1', message: 'stale rejection', progress: null });
        expect(s.phase).toBe('success');
        expect(s.message).toBe('');
    });

    it('ignores a failure naming an older round after Retry started a new one', () => {
        // Concrete Retry bug: `start` resets to a fresh round while the
        // PREVIOUS round's in-flight request is still pending; its eventual
        // rejection carries the OLD round id and must not flip the new,
        // already-verifying round to failed.
        let s = reachReducer(initialReachState, { type: 'start', total: 2, roundId: 'R1' });
        s = reachReducer(s, { type: 'start', total: 2, roundId: 'R2' });
        expect(s.phase).toBe('verifying');
        s = reachReducer(s, { type: 'failure', roundId: 'R1', message: 'stale from R1', progress: null });
        expect(s.phase).toBe('verifying');
        expect(s.message).toBe('');
    });
});

describe('conclusive progress failure vs real timeout', () => {
    it('does not use the timeout message for a fast, conclusive failure', () => {
        let s = reachReducer(initialReachState, { type: 'start', total: 2, roundId: 'R1' });
        s = reachReducer(s, {
            type: 'progress',
            roundId: 'R1',
            progress: {
                confirmed: 1, total: 2, done: true, ok: false,
                results: [
                    { coreId: 'core-a', status: 'ok' },
                    { coreId: 'core-b', status: 'not-shared', missingPeers: ['core-a'] },
                ],
            },
        });
        expect(s.phase).toBe('failed');
        expect(s.message).not.toContain('20 seconds');
        expect(s.message).toContain('1 of 2');
        expect(s.message).toContain(statusLabel('not-shared'));
    });

    it('names each distinct failing status when more than one Core fails differently', () => {
        let s = reachReducer(initialReachState, { type: 'start', total: 3, roundId: 'R1' });
        s = reachReducer(s, {
            type: 'progress',
            roundId: 'R1',
            progress: {
                confirmed: 1, total: 3, done: true, ok: false,
                results: [
                    { coreId: 'core-a', status: 'ok' },
                    { coreId: 'core-b', status: 'not-shared' },
                    { coreId: 'core-c', status: 'write-denied' },
                ],
            },
        });
        expect(s.phase).toBe('failed');
        expect(s.message).not.toContain('20 seconds');
        expect(s.message).toContain('2 of 3');
    });

    it('still uses the timeout message when the deadline actually passes without a verdict', () => {
        const s = reachReducer(start(2), { type: 'tick', elapsedMs: 20100 });
        expect(s.phase).toBe('failed');
        expect(s.message).toContain('20 seconds');
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

    it('returns an empty remedy for ok on purpose, since there is nothing to remedy on success', () => {
        expect(statusRemedy('ok')).toBe('');
    });

    it('falls back rather than rendering a raw enum for an unknown status', () => {
        expect(statusLabel('something-new' as ReachStatus)).not.toBe('');
    });

    it('falls back rather than rendering blank for an unknown status remedy', () => {
        expect(statusRemedy('something-new' as ReachStatus)).not.toBe('');
    });
});

describe('parseStorageReachEvent', () => {
    it('parses a real round progress payload into a progress event', () => {
        const event = parseStorageReachEvent({
            round: 'R1', confirmed: 1, total: 2, done: false, ok: false,
            results: [{ coreId: 'core-a', status: 'ok' }],
        });
        expect(event).toEqual({
            type: 'progress',
            roundId: 'R1',
            progress: {
                confirmed: 1, total: 2, done: false, ok: false,
                results: [{ coreId: 'core-a', status: 'ok' }],
            },
        });
    });

    it('ignores the periodic self-check publish, which carries no counter', () => {
        // The self-check publishes on the same SSE channel with no confirmed/
        // total fields at all - that absence is the only thing that tells it
        // apart from a real round progress payload.
        expect(parseStorageReachEvent({ status: 'ok' })).toBeNull();
    });

    it('ignores a payload missing just `total`', () => {
        expect(parseStorageReachEvent({ round: 'R1', confirmed: 1 })).toBeNull();
    });

    it('ignores a payload missing just `confirmed`', () => {
        expect(parseStorageReachEvent({ round: 'R1', total: 2 })).toBeNull();
    });

    it('ignores a malformed payload without crashing', () => {
        expect(parseStorageReachEvent(null)).toBeNull();
        expect(parseStorageReachEvent(undefined)).toBeNull();
        expect(parseStorageReachEvent('not an object')).toBeNull();
        expect(parseStorageReachEvent(42)).toBeNull();
        expect(parseStorageReachEvent({ confirmed: 'one', total: 2 })).toBeNull();
    });

    it('defaults results to an empty array when absent or malformed', () => {
        const event = parseStorageReachEvent({ confirmed: 0, total: 3 });
        expect(event?.type).toBe('progress');
        expect(event && event.type === 'progress' && event.progress.results).toEqual([]);
    });

    it('passes through the round id for a payload naming a different round, unfiltered', () => {
        // parseStorageReachEvent has no notion of "the current round" - it only
        // extracts what the payload carries. Rejecting a stale/foreign round is
        // the reducer's job (its own roundId guard), not this function's.
        const event = parseStorageReachEvent({ round: 'R2', confirmed: 2, total: 2, done: true, ok: true, results: [] });
        expect(event).toEqual({
            type: 'progress',
            roundId: 'R2',
            progress: { confirmed: 2, total: 2, done: true, ok: true, results: [] },
        });
    });
});

describe('fleetHealthySummary', () => {
    it('uses singular wording for exactly one Core', () => {
        expect(fleetHealthySummary(1)).toBe('This Core can reach the shared storage.');
    });

    it('uses plural wording with the count for more than one Core', () => {
        expect(fleetHealthySummary(3)).toBe('All 3 Cores can reach the shared storage.');
    });

    it('does not special-case zero, so a not-yet-populated online list reads as "All 0"', () => {
        // There is no meaningful zero-Core healthy state in practice (the panel
        // itself is served by an online Core), so this just pins the actual
        // behavior rather than inventing a state nothing produces.
        expect(fleetHealthySummary(0)).toBe('All 0 Cores can reach the shared storage.');
    });
});
