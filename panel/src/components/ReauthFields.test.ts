import { describe, it, expect } from 'vitest';
import { reauthReady } from './ReauthFields';

// The rule this pins is the one that can drift away from Core: the second
// factor is asked for ONLY when the account has one.
//
// Both directions cost something real. Demanding a code from an account without
// 2FA blocks that user from the action completely - they have no code to give.
// Not demanding one from an account that has it leaves the second factor
// decorative on exactly the actions it exists for, since these are the ones
// that outlive a password change.
describe('reauthReady', () => {
    it('takes the password alone when the account has no second factor', () => {
        expect(reauthReady(false, 'pw', '')).toBe(true);
    });

    it('still needs the password when the account has no second factor', () => {
        expect(reauthReady(false, '', '')).toBe(false);
        expect(reauthReady(false, '', '123456')).toBe(false);
    });

    it('needs both when the account has a second factor', () => {
        expect(reauthReady(true, 'pw', '')).toBe(false);
        expect(reauthReady(true, '', '123456')).toBe(false);
        expect(reauthReady(true, 'pw', '123456')).toBe(true);
    });

    it('accepts a backup code, which is longer than a TOTP code', () => {
        expect(reauthReady(true, 'pw', 'abcdef0123456789')).toBe(true);
    });

    it('ignores spacing, the way the 2FA dialogs already do', () => {
        expect(reauthReady(true, 'pw', '123 456')).toBe(true);
        expect(reauthReady(true, 'pw', '  12  ')).toBe(false);
    });
});
