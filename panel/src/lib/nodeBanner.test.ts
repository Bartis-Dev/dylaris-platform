import { describe, it, expect } from 'vitest';
import { nodeBannerState, type BannerNode } from './nodeBanner';
import { RECONNECTING_MS, UNREACHABLE_MS } from './connectivity';

const NOW = Date.parse('2026-09-01T12:00:00Z');
const ago = (ms: number) => new Date(NOW - ms).toISOString();

function node(over: Partial<BannerNode> = {}): BannerNode {
    return { name: 'home-box', status: 'offline', lastSeenAt: ago(UNREACHABLE_MS - 1_000), ...over };
}

describe('nodeBannerState', () => {
    it('says nothing while everything is connected', () => {
        expect(nodeBannerState([{ name: 'a', status: 'online' }], NOW)).toBeNull();
        expect(nodeBannerState([], NOW)).toBeNull();
    });

    // The decision that keeps this bar credible. A minute without a heartbeat is
    // every ordinary restart; a bar that appears whenever someone reboots their
    // own machine is a bar they stop reading.
    it('stays quiet while a node is only reconnecting', () => {
        const justRestarted = node({ lastSeenAt: ago(RECONNECTING_MS - 1_000) });
        expect(nodeBannerState([justRestarted], NOW)).toBeNull();
    });

    it('speaks once the outage is long enough to matter', () => {
        const got = nodeBannerState([node({ lastSeenAt: ago(RECONNECTING_MS + 1_000) })], NOW);
        expect(got?.tier).toBe('unreachable');
        expect(got?.text).toContain('home-box');
        expect(got?.text).toContain('stopped responding');
    });

    it('reports a node that has never connected', () => {
        const got = nodeBannerState([node({ lastSeenAt: null })], NOW);
        expect(got?.tier).toBe('down');
        // No "last seen" clause, because there is no such moment to name.
        expect(got?.text).not.toContain('last seen');
    });

    it('prefers the name the owner gave it', () => {
        const got = nodeBannerState([node({ name: 'n-7f3a', displayName: 'Basement' })], NOW);
        expect(got?.text).toContain('Basement');
        expect(got?.text).not.toContain('n-7f3a');
    });

    // The worst state sets the tone: one machine merely slow and one properly
    // gone is a "gone" situation, not a "slow" one.
    it('lets the worst machine set the tone', () => {
        const got = nodeBannerState([
            node({ name: 'slow', lastSeenAt: ago(RECONNECTING_MS + 1_000) }),
            node({ name: 'gone', lastSeenAt: ago(UNREACHABLE_MS * 10) }),
        ], NOW);
        expect(got?.tier).toBe('down');
        expect(got?.count).toBe(1);
        expect(got?.text).toContain('gone');
    });

    it('counts rather than lists when several are in the same state', () => {
        const got = nodeBannerState([
            node({ name: 'a', lastSeenAt: ago(UNREACHABLE_MS * 10) }),
            node({ name: 'b', lastSeenAt: ago(UNREACHABLE_MS * 10) }),
        ], NOW);
        expect(got?.count).toBe(2);
        expect(got?.text).toContain('2 of your machines');
    });

    // An online node alongside a dead one must not dilute the message, and must
    // not be counted into it.
    it('ignores the healthy ones', () => {
        const got = nodeBannerState([
            { name: 'fine', status: 'online' },
            node({ name: 'dead', lastSeenAt: ago(UNREACHABLE_MS * 10) }),
        ], NOW);
        expect(got?.count).toBe(1);
        expect(got?.text).toContain('dead');
    });
});
