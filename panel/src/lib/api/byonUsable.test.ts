import { describe, it, expect } from 'vitest';
import { isByonUsable, isGatewayRouting, RoutingMode } from './types';

// The rule this file exists for: feature_byon_enabled is the operator's INTENT,
// not a statement that BYON can work. A tenant node forces gateway routing on
// its own side (NODE_EXTERNAL), so with routing on ip_port there is nothing for
// it to join.
//
// This matters most for the build that never has a gateway at all: the gateway
// is not part of the open-core stack, so on a self-host install the raw flag
// could switch on tenant enrolment screens for machines that can never connect,
// plus the Usage, Billing and Plans settings sitting behind them.
describe('isByonUsable', () => {
    const cases: Array<{ flag: boolean; mode: RoutingMode; want: boolean; why: string }> = [
        { flag: true, mode: 'gateway', want: true, why: 'flag on and the gateway routes: the real hosted case' },
        { flag: true, mode: 'both', want: true, why: 'both still routes through the gateway' },
        {
            flag: true, mode: 'ip_port', want: false,
            why: 'the self-host case: an operator switched the flag on with no gateway, and a tenant node has nothing to join',
        },
        { flag: false, mode: 'gateway', want: false, why: 'gateway up but the operator has not turned BYON on' },
        { flag: false, mode: 'ip_port', want: false, why: 'neither' },
    ];

    for (const c of cases) {
        it(`${c.flag ? 'flag on' : 'flag off'} + ${c.mode} -> ${c.want} (${c.why})`, () => {
            expect(isByonUsable(c.flag, c.mode)).toBe(c.want);
        });
    }

    // Reading the raw flag is exactly the bug: on ip_port the two disagree, and
    // the flag is the one that would have shown the UI.
    it('differs from the raw flag precisely where the gateway is off', () => {
        expect(isByonUsable(true, 'ip_port')).toBe(false);
        expect(true && isGatewayRouting('ip_port')).toBe(false);
        // and agrees everywhere the gateway is on, so the gate adds no surprise
        for (const mode of ['gateway', 'both'] as RoutingMode[]) {
            expect(isByonUsable(true, mode)).toBe(true);
        }
    });
});
