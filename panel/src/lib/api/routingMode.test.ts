import { describe, it, expect } from 'vitest';
import { isGatewayRouting, RoutingMode } from './types';

// The panel used to decide "is the gateway on" from a separate stored feature
// flag while Core decided it from routing_mode. The two could disagree in both
// directions: a routes UI whose every write 503s, or a live gateway with its
// whole UI hidden. These cases pin the panel to Core's rule
// (core/handlers/gateway_gate.go: mode == "gateway" || mode == "both").
describe('isGatewayRouting', () => {
    const cases: Array<{ mode: RoutingMode; want: boolean }> = [
        { mode: 'ip_port', want: false },
        { mode: 'gateway', want: true },
        { mode: 'both', want: true },
    ];

    for (const { mode, want } of cases) {
        it(`${mode} -> ${want}`, () => {
            expect(isGatewayRouting(mode)).toBe(want);
        });
    }
});
