import { describe, it, expect } from 'vitest';
import { countThatFit } from './Navbar';

describe('countThatFit', () => {
    const TRIGGER = 60;

    it('keeps everything when it all fits', () => {
        expect(countThatFit([100, 100, 100], 400, TRIGGER)).toBe(3);
    });

    it('reserves room for the trigger while entries are still being collected', () => {
        // Four entries, 320px of room. The third would take the total to 300,
        // which fits on its own - but a fourth is still waiting, so the trigger
        // has to fit too and 300 + 60 does not. Two are shown, two collect.
        expect(countThatFit([100, 100, 100, 100], 320, TRIGGER)).toBe(2);
    });

    it('does not reserve for a trigger that will not be drawn', () => {
        // The last entry fits exactly, so nothing overflows and no trigger is
        // needed. Reserving unconditionally would hide an entry to make room
        // for a menu that would then have exactly one item in it.
        expect(countThatFit([100, 100], 200, TRIGGER)).toBe(2);
    });

    /**
     * The defect this function exists to prevent.
     *
     * The old measurement read the widths of the items it was CURRENTLY
     * rendering. Once an entry had been hidden its width was gone from the
     * calculation, so widening the window could never bring it back - a one-way
     * door where every resize could take entries away and none could return
     * them. The widths are now measured off a hidden row that always holds every
     * entry, so the same input width always yields the same answer.
     */
    it('is reversible: widening restores what narrowing removed', () => {
        const widths = [100, 100, 100, 100];
        const wide = countThatFit(widths, 500, TRIGGER);
        const narrow = countThatFit(widths, 220, TRIGGER);
        const wideAgain = countThatFit(widths, 500, TRIGGER);

        expect(narrow).toBeLessThan(wide);
        expect(wideAgain).toBe(wide);
    });

    it('collapses to none rather than overflowing when nothing fits', () => {
        expect(countThatFit([200, 200], 80, TRIGGER)).toBe(0);
    });

    it('treats an unmeasured strip as "show everything" rather than "hide everything"', () => {
        // clientWidth is 0 for one frame before layout. Reading that as "nothing
        // fits" would flash an empty nav on every mount.
        expect(countThatFit([100, 100], 0, TRIGGER)).toBe(2);
    });
});
