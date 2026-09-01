import { nodeConnectivity, type ConnTier } from '@/lib/connectivity';
import { timeAgo } from '@/lib/time';

/**
 * Telling a tenant their own machine has stopped talking to us.
 *
 * A BYON tenant is the one reader who can DO something about a node being
 * offline, and the one reader who never looked: the state lived on the
 * infrastructure page, which nobody opens while their servers are running
 * fine. By the time they noticed, the servers had been unreachable for hours.
 *
 * So it is a banner in the layout, on the same footing as billing and storage:
 * it costs nothing while everything is connected and it finds the reader
 * wherever they happen to be.
 */

/** The little a banner needs to know about a machine. */
export interface BannerNode {
    name: string;
    displayName?: string;
    status?: string;
    lastSeenAt?: string | null;
}

export interface NodeBannerState {
    /** The worst tier among the tenant's machines. */
    tier: Exclude<ConnTier, 'ok' | 'reconnecting'>;
    /** How many machines are in that state. */
    count: number;
    /** What to say. One machine is named; several are counted. */
    text: string;
}

function label(n: BannerNode): string {
    return (n.displayName || '').trim() || n.name;
}

/**
 * What to show, or null for "say nothing".
 *
 * 'reconnecting' is deliberately NOT surfaced. It starts one minute after the
 * last heartbeat, which every ordinary restart crosses, and a red bar that
 * appears whenever someone reboots their own machine is a bar people learn to
 * ignore. The banner waits for 'unreachable', where the outage has lasted long
 * enough to be worth interrupting somebody over.
 *
 * A node that is merely 'offline' in Core with no heartbeat ever recorded reads
 * as 'down' and is reported: a machine that has never connected is exactly the
 * one whose owner is waiting for something to happen.
 */
export function nodeBannerState(nodes: BannerNode[], now: number): NodeBannerState | null {
    const bad = nodes
        .map(n => ({ n, tier: nodeConnectivity(n.status, n.lastSeenAt, now).tier }))
        .filter(x => x.tier === 'unreachable' || x.tier === 'down');
    if (bad.length === 0) return null;

    // The worst one sets the tone, because that is the one with the problem.
    const down = bad.filter(x => x.tier === 'down');
    const worst = down.length > 0 ? down : bad;
    const tier = down.length > 0 ? ('down' as const) : ('unreachable' as const);

    if (worst.length === 1) {
        const { n } = worst[0];
        const seen = n.lastSeenAt ? `, last seen ${timeAgo(n.lastSeenAt)}` : '';
        const what = tier === 'down'
            ? `${label(n)} is offline${seen}.`
            : `${label(n)} has stopped responding${seen}.`;
        return { tier, count: 1, text: `${what} Servers on it are unreachable until it comes back.` };
    }
    return {
        tier,
        count: worst.length,
        text: `${worst.length} of your machines are ${tier === 'down' ? 'offline' : 'not responding'}. Servers on them are unreachable until they come back.`,
    };
}
