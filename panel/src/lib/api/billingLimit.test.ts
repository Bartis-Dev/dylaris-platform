import { describe, it, expect } from 'vitest';
import { limitFromSetting, limitToSetting, LIMIT_UNLIMITED } from '@/lib/api/billing';

describe('operator-typed limits in the settings table', () => {
    it('reads an unset setting as no cap, never as a cap of none', () => {
        // The defect this pins: Core answered "0" for an unset R2 quota and the
        // panel sent it straight back, so opening the billing screen and pressing
        // Save stopped every tenant's backups at zero bytes.
        expect(limitFromSetting('')).toBeNull();
    });

    it('keeps unlimited and zero apart', () => {
        expect(limitFromSetting(LIMIT_UNLIMITED)).toBeNull();
        expect(limitFromSetting('0')).toBe(0);
    });

    it('reads a number as that cap', () => {
        expect(limitFromSetting('250')).toBe(250);
    });

    it('falls back to no cap for anything unparseable', () => {
        // Not reachable through the panel, so a value here was hand-edited or
        // written by an older build. No cap is the only answer that cannot
        // silently stop somebody.
        expect(limitFromSetting('nonsense')).toBeNull();
        expect(limitFromSetting('-5')).toBeNull();
    });

    it('writes null as the word, so unset stays distinguishable', () => {
        expect(limitToSetting(null)).toBe(LIMIT_UNLIMITED);
        expect(limitToSetting(0)).toBe('0');
        expect(limitToSetting(250)).toBe('250');
    });

    it('round-trips every state a control can hold', () => {
        for (const v of [null, 0, 1, 250]) {
            expect(limitFromSetting(limitToSetting(v))).toBe(v);
        }
    });
});
