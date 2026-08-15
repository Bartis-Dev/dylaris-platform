import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { isDeliveryModeDisabled } from './ModpacksTab';
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
    canPresign: true, publicConfigured: true, publicReachable: true, privatePackCount: 0, notes: {}, ...over,
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

describe('ModpacksTab wires the delivery radios and hints to the real capability state', () => {
    const source = readFileSync(join(__dirname, 'ModpacksTab.tsx'), 'utf8');

    it('the presigned/public radios compute `disabled` via isDeliveryModeDisabled, not a hand-rolled condition', () => {
        expect(source).toMatch(/const disabled = isDeliveryModeDisabled\(mode, deliveryCaps\);/);
        expect(source).toMatch(/disabled=\{disabled\}/);
    });

    it('the presigned hint renders the backend-provided note, not a hardcoded string', () => {
        expect(source).toMatch(/deliveryCaps\?\.canPresign === false && deliveryCaps\.notes\.presigned/);
        expect(source).toMatch(/<span>\{deliveryCaps\.notes\.presigned\}<\/span>/);
    });

    it('the public hint fires on either not-configured or configured-but-unreachable', () => {
        expect(source).toMatch(/deliveryCaps\?\.publicConfigured === false \|\| deliveryCaps\?\.publicReachable === false/);
        expect(source).toMatch(/<span>\{deliveryCaps\.notes\.public\}<\/span>/);
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
