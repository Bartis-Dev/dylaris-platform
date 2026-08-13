export type ConnTier = 'ok' | 'reconnecting' | 'unreachable' | 'down';

// Escalation thresholds (hardcoded; env/settings-configurability is a later
// follow-up). A node flips offline in Core ~15-20s after its last heartbeat.
export const RECONNECTING_MS = 60_000;
export const UNREACHABLE_MS = 300_000;

// nodeStatus: only the explicit 'offline' escalates; 'online', unknown, or an
// absent field all mean "trust server.status" (today's behaviour), which is the
// rollout-safe default while the Core field lands / on a cached payload.
export function nodeConnectivity(
  nodeStatus: string | null | undefined,
  nodeLastSeenAt: string | null | undefined,
  now: number,
): { tier: ConnTier; ageMs: number | null } {
  if (nodeStatus !== 'offline') return { tier: 'ok', ageMs: null };
  if (!nodeLastSeenAt) return { tier: 'down', ageMs: null };
  const ageMs = now - Date.parse(nodeLastSeenAt);
  if (ageMs < RECONNECTING_MS) return { tier: 'reconnecting', ageMs };
  if (ageMs < UNREACHABLE_MS) return { tier: 'unreachable', ageMs };
  return { tier: 'down', ageMs };
}

// Dot background per non-ok tier (Tailwind class). 'ok' is empty: the caller
// keeps rendering the server-status dot unchanged.
export const CONN_TONE: Record<ConnTier, string> = {
  ok: '',
  reconnecting: 'bg-(--warning) animate-pulse',
  unreachable: 'bg-(--warning)',
  down: 'bg-(--error)',
};

// Pick the dot bg-class: for 'ok' keep the caller's server-status class, else
// override with the connectivity tone. Extracted so the render sites stay thin
// and the selection is unit-tested.
export function dotFor(tier: ConnTier, okClass: string): string {
  return tier === 'ok' ? okClass : CONN_TONE[tier];
}

// Human label for a non-ok tier. Warp is folded in deliberately: a BYON node's
// "warp down" and "node process down" are indistinguishable from Core.
export function connLabel(
  tier: ConnTier,
  nodeLastSeenAt: string | null | undefined,
  now: number,
): string {
  if (tier === 'ok') return '';
  if (tier === 'reconnecting') return 'Reconnecting...';
  const secs = nodeLastSeenAt ? Math.floor((now - Date.parse(nodeLastSeenAt)) / 1000) : null;
  const seen = secs === null ? '' : ` - last seen ${secs < 60 ? `${secs}s` : secs < 3600 ? `${Math.floor(secs / 60)}m` : `${Math.floor(secs / 3600)}h`} ago`;
  if (tier === 'unreachable') return `Node not responding (node or its warp tunnel)${seen}`;
  return `Node offline${seen}`;
}
