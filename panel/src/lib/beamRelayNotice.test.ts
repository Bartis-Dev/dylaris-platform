import { describe, it, expect } from 'vitest';
import { shouldWarnBeamRelayMissing, type BeamRelayInput } from './beamRelayNotice';

const i = (o: Partial<BeamRelayInput> = {}): BeamRelayInput =>
    ({ enabled: true, relayAddress: '', gatewayEnabled: true, ...o });

describe('shouldWarnBeamRelayMissing', () => {
    // The reported bug. Core sends manualOverride:"" plus a resolved
    // relayAddress; the old `manualOverride ?? relayAddress` never reached the
    // second field, so a working discovered relay warned forever.
    it('stays quiet when Core resolved an address by discovery', () => {
        expect(shouldWarnBeamRelayMissing(i({ relayAddress: 'beam.dylaris.com:25550' }))).toBe(false);
    });

    it('warns when there really is no address', () => {
        expect(shouldWarnBeamRelayMissing(i({ relayAddress: '' }))).toBe(true);
        expect(shouldWarnBeamRelayMissing(i({ relayAddress: null }))).toBe(true);
        expect(shouldWarnBeamRelayMissing(i({ relayAddress: undefined }))).toBe(true);
    });

    it('treats whitespace as no address', () => {
        expect(shouldWarnBeamRelayMissing(i({ relayAddress: '   ' }))).toBe(true);
    });

    // A relay only exists inside the gateway subsystem. Without routing there is
    // nothing to configure, so the warning describes a feature the install does
    // not run.
    it('never warns when the gateway is not routing', () => {
        expect(shouldWarnBeamRelayMissing(i({ gatewayEnabled: false }))).toBe(false);
        expect(shouldWarnBeamRelayMissing(i({ gatewayEnabled: false, enabled: false }))).toBe(false);
    });

    it('never warns when Beam itself is off', () => {
        expect(shouldWarnBeamRelayMissing(i({ enabled: false }))).toBe(false);
    });
});
