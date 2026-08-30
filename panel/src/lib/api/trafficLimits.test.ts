import { describe, it, expect } from 'vitest';
import { modeOf, writeFor } from './trafficLimits';

// The three states an operator means. A nullable number can only express two of
// them, which is why the wire carries a mode: "the scope above decides" and
// "decided here, no limit" are both an absent number, and only one of them
// stops the lookup.
describe('writeFor', () => {
    it('an unset row defers to the scope above', () => {
        expect(writeFor(false, null)).toEqual({ mode: 'default' });
        // Even with a number still sitting in the input: not being set is what
        // decides, or a value left over from a previous edit would silently be
        // written by unticking the row.
        expect(writeFor(false, 500)).toEqual({ mode: 'default' });
    });

    it('a set row with no number is an explicit "no limit"', () => {
        expect(writeFor(true, null)).toEqual({ mode: 'unlimited' });
    });

    it('a set row with a number is that cap', () => {
        expect(writeFor(true, 1000)).toEqual({ mode: 'custom', gb: 1000 });
    });

    it('zero is a cap, not an absence', () => {
        // The case the whole convention exists for. A region where nothing is
        // included, or where extra traffic is not for sale, has to be
        // expressible - and it must not collapse into "unlimited", which is
        // what happened four times on this platform before limits were nullable.
        expect(writeFor(true, 0)).toEqual({ mode: 'custom', gb: 0 });
    });
});

describe('modeOf', () => {
    it('reads a stored null as unlimited, never as default', () => {
        // A row that EXISTS has answered. Rendering it as "default" would
        // suggest the scope above still decides, and an operator would then be
        // surprised that changing the global value changed nothing.
        expect(modeOf(null)).toBe('unlimited');
    });

    it('reads a number as a custom cap, including zero', () => {
        expect(modeOf(0)).toBe('custom');
        expect(modeOf(2000)).toBe('custom');
    });
});
