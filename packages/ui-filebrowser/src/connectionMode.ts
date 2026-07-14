// Pure, React-free metadata for the Beam connection-mode badge. Kept out of
// FileBrowser.tsx so it is unit-testable under the node-env vitest without a DOM or
// React renderer. FileBrowser imports it for the toolbar badge; the panel imports it
// (deep path) for the vitest test.

export type BeamConnectionMode = 'lan-fastpath' | 'relay' | 'direct';

export interface BeamConnectionModeMeta {
  label: string;
  description: string;
}

const META: Record<BeamConnectionMode, BeamConnectionModeMeta> = {
  'lan-fastpath': {
    label: 'LAN fast-path',
    description:
      'Connected directly to the node over your local network (pinned-TLS). Fastest transfers. Only available when this device is on the same LAN as the node and the node publishes its beam port (25523).',
  },
  relay: {
    label: 'Relay',
    description:
      'Connected through the gateway relay. The node IP stays hidden and transfers are routed over the overlay. Used when a gateway is present and no LAN fast-path is reachable.',
  },
  direct: {
    label: 'Direct',
    description:
      "Connected directly to the node's public address (pinned-TLS). Used when no gateway relay is configured and no LAN fast-path is reachable.",
  },
};

const FALLBACK: BeamConnectionModeMeta = {
  label: 'Connected',
  description: 'A beam file-transfer connection is active.',
};

// beamConnectionModeMeta returns the badge label + tooltip description for a beam
// connection mode. An unrecognized value yields a safe generic fallback rather than
// throwing, so an unexpected string from the Go side can never crash the toolbar.
export function beamConnectionModeMeta(mode: BeamConnectionMode): BeamConnectionModeMeta {
  return META[mode] ?? FALLBACK;
}
