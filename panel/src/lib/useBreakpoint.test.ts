import { describe, it, expect } from 'vitest';
import { BREAKPOINTS, layoutForWidth } from './useBreakpoint';

describe('layoutForWidth', () => {
    it('is wide at and above the rail breakpoint', () => {
        expect(layoutForWidth(BREAKPOINTS.rail)).toBe('wide');
        expect(layoutForWidth(1920)).toBe('wide');
    });

    it('is narrow between compact and rail - the band where only the sidebar collapses', () => {
        expect(layoutForWidth(BREAKPOINTS.rail - 1)).toBe('narrow');
        expect(layoutForWidth(BREAKPOINTS.compact)).toBe('narrow');
    });

    it('is compact below the compact breakpoint', () => {
        expect(layoutForWidth(BREAKPOINTS.compact - 1)).toBe('compact');
        expect(layoutForWidth(BREAKPOINTS.floor)).toBe('compact');
    });

    it('stays compact below the floor rather than inventing a fourth band', () => {
        // Below the floor the page scrolls horizontally instead of scaling
        // further, so there is deliberately no layout below this one.
        expect(layoutForWidth(320)).toBe('compact');
        expect(layoutForWidth(0)).toBe('compact');
    });

    it('has boundaries in a sane order', () => {
        expect(BREAKPOINTS.floor).toBeLessThan(BREAKPOINTS.compact);
        expect(BREAKPOINTS.compact).toBeLessThan(BREAKPOINTS.rail);
    });
});
