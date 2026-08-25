import { describe, it, expect } from 'vitest';
import { place } from './HelpTip';

const pop = { width: 320, height: 160 };
const wide = { width: 1600, height: 900 };

describe('place', () => {
    it('sits to the right of the trigger when there is room', () => {
        const p = place({ left: 400, right: 416, top: 300 }, pop, wide);
        expect(p.left).toBe(424); // right edge + 8px gap
    });

    // The failing case nobody develops in: a trigger near the right edge, which
    // is exactly where a help icon sits when it follows a full-width label.
    it('flips to the left when the right side does not fit', () => {
        const p = place({ left: 1500, right: 1516, top: 300 }, pop, wide);
        expect(p.left).toBe(1172); // 1500 - 8 - 320
    });

    // Flipping is not enough on a narrow window: both sides can fail, and the
    // popover must still land on screen rather than at a negative offset.
    it('never renders off the left edge', () => {
        const narrow = { width: 400, height: 900 };
        const p = place({ left: 380, right: 396, top: 300 }, pop, narrow);
        expect(p.left).toBeGreaterThanOrEqual(8);
        expect(p.left + pop.width).toBeLessThanOrEqual(narrow.width);
    });

    it('never renders off the bottom edge', () => {
        const p = place({ left: 400, right: 416, top: 880 }, pop, wide);
        expect(p.top + pop.height).toBeLessThanOrEqual(wide.height - 8);
    });

    it('never renders above the top edge', () => {
        const p = place({ left: 400, right: 416, top: 2 }, pop, wide);
        expect(p.top).toBeGreaterThanOrEqual(8);
    });
});
