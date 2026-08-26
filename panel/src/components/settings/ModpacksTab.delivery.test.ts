import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { isDeliveryModeDisabled, deliveryDisabledReason } from './ModpacksTab';
import type { DeliveryCapabilities } from '@/lib/api/modpackSettings';

// This repo has no component-render test harness (no @testing-library/react —
// see the panel's react/react-dom pin: panel/package.json exact-pins
// "19.2.0" while the npm workspace root hoists a newer range-satisfied copy
// for other members, so any renderer pulled in only via a hoisted devDep
// loads a SECOND react instance and every hook call breaks; `npm dedupe`
// itself refuses with an ERESOLVE conflict against next@16's peer range).
// Fixing that is a workspace-wide dependency change, out of scope here. So,
// same as bootGate.test.ts/discardedResult.test.ts/setupStatusTimeout.test.ts
// elsewhere in this file's own directory tree: the disabled-state LOGIC is
// tested directly (exhaustively, table-driven) and the JSX wiring is pinned
// with a source-structure assertion instead of a live render.

const disabled = (over: Partial<DeliveryCapabilities>): DeliveryCapabilities => ({
    storageConfigured: true, canPresign: true, publicConfigured: true, publicReachable: true, privatePackCount: 0, notes: {}, ...over,
});

describe('isDeliveryModeDisabled', () => {
    it('unknown capabilities (null — not loaded yet, or probe failed) fails open: nothing is disabled', () => {
        expect(isDeliveryModeDisabled('core', null)).toBe(false);
        expect(isDeliveryModeDisabled('presigned', null)).toBe(false);
        expect(isDeliveryModeDisabled('public', null)).toBe(false);
    });

    it('core is never disabled regardless of capabilities', () => {
        expect(isDeliveryModeDisabled('core', disabled({ canPresign: false }))).toBe(false);
        expect(isDeliveryModeDisabled('core', disabled({ publicConfigured: false, publicReachable: false }))).toBe(false);
    });

    it('presigned is disabled exactly when canPresign is false', () => {
        expect(isDeliveryModeDisabled('presigned', disabled({ canPresign: false }))).toBe(true);
        expect(isDeliveryModeDisabled('presigned', disabled({ canPresign: true }))).toBe(false);
    });

    it('public is disabled when not configured, or configured but unreachable', () => {
        expect(isDeliveryModeDisabled('public', disabled({ publicConfigured: false }))).toBe(true);
        expect(isDeliveryModeDisabled('public', disabled({ publicConfigured: true, publicReachable: false }))).toBe(true);
        expect(isDeliveryModeDisabled('public', disabled({ publicConfigured: true, publicReachable: true }))).toBe(false);
    });

    it('public with publicReachable unknown (null — probe inconclusive) is NOT disabled on that basis alone', () => {
        expect(isDeliveryModeDisabled('public', disabled({ publicConfigured: true, publicReachable: null }))).toBe(false);
    });

    it('matches the brief: canPresign false greys out presigned only; caps allowing enables both', () => {
        const cannotPresign = disabled({ canPresign: false, publicConfigured: false, publicReachable: null });
        expect(isDeliveryModeDisabled('presigned', cannotPresign)).toBe(true);

        const allAllowed = disabled({ canPresign: true, publicConfigured: true, publicReachable: true });
        expect(isDeliveryModeDisabled('presigned', allAllowed)).toBe(false);
        expect(isDeliveryModeDisabled('public', allAllowed)).toBe(false);
    });
});

describe('deliveryDisabledReason', () => {
    it('is empty for a mode that is not disabled — nothing to explain', () => {
        expect(deliveryDisabledReason('core', disabled({ canPresign: false }))).toBe('');
        expect(deliveryDisabledReason('presigned', disabled({ canPresign: true }))).toBe('');
        expect(deliveryDisabledReason('public', null)).toBe('');
    });

    it('prefers the backend note over the generic fallback', () => {
        const caps = disabled({ canPresign: false, notes: { presigned: 'this backend cannot presign' } });
        expect(deliveryDisabledReason('presigned', caps)).toBe('this backend cannot presign');
    });

    it('still explains itself when the probe returned no note', () => {
        // The important property: a disabled option is NEVER shown without a
        // reason, which is the state people file bugs about.
        expect(deliveryDisabledReason('presigned', disabled({ canPresign: false }))).not.toBe('');
        expect(deliveryDisabledReason('public', disabled({ publicConfigured: false }))).not.toBe('');
    });

    it('explains an unreachable public base, not only an unconfigured one', () => {
        const caps = disabled({ publicConfigured: true, publicReachable: false, notes: { public: 'base URL did not answer' } });
        expect(deliveryDisabledReason('public', caps)).toBe('base URL did not answer');
    });
});

describe('ModpacksTab wires the delivery options and hints to the real capability state', () => {
    const source = readFileSync(join(__dirname, 'ModpacksTab.tsx'), 'utf8');

    it('the delivery options compute `disabled` via isDeliveryModeDisabled, not a hand-rolled condition', () => {
        expect(source).toMatch(/const disabled = isDeliveryModeDisabled\(opt\.value, deliveryCaps\);/);
        expect(source).toMatch(/disabled=\{disabled\}/);
    });

    it('the disabled reason comes from deliveryDisabledReason, not a hardcoded string per mode', () => {
        expect(source).toMatch(/const reason = disabled \? deliveryDisabledReason\(opt\.value, deliveryCaps\) : '';/);
        expect(source).toMatch(/<span>\{reason\}<\/span>/);
    });

    it('a disabled option surfaces its reason inline rather than only in a tooltip', () => {
        // title= alone would hide the reason from touch and keyboard users.
        expect(source).toMatch(/\{reason && \(/);
    });

    it('capabilities are probed on mount alongside the settings load, failing open on error', () => {
        expect(source).toMatch(/getModpackDeliveryCapabilities\(\)/);
        expect(source).toMatch(/\.catch\(\(\) => setDeliveryCaps\(null\)\)/);
    });

    it('warns that public mode exposes private/hidden packs, only when public is selected and such packs exist', () => {
        expect(source).toMatch(/settings\.solderDeliveryMode === 'public' && \(deliveryCaps\?\.privatePackCount \?\? 0\) > 0/);
        expect(source).toMatch(/Use presigned to keep private packs confidential/);
    });
});
