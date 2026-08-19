import { describe, it, expect } from 'vitest';
import { iconMap, TAB_ICON_NAMES } from './icons';

describe('TAB_ICON_NAMES', () => {
    // A tab stores its icon by NAME. A picker entry that iconMap cannot resolve
    // renders the Grid2x2 fallback, so the user picks one thing and gets
    // another - silently, because nothing errors.
    it('every offered name resolves in iconMap', () => {
        const unresolved = TAB_ICON_NAMES.filter(n => !iconMap[n]);
        expect(unresolved).toEqual([]);
    });

    it('offers no name twice', () => {
        expect(new Set(TAB_ICON_NAMES).size).toBe(TAB_ICON_NAMES.length);
    });

    // iconMap also holds Material-Symbol aliases pointing at the SAME
    // components. Offering both would put visually identical entries in the
    // grid under two names, which reads as a broken picker.
    it('offers no two names that render the same icon', () => {
        const byComponent = new Map<unknown, string[]>();
        for (const name of TAB_ICON_NAMES) {
            const c = iconMap[name];
            byComponent.set(c, [...(byComponent.get(c) ?? []), name]);
        }
        const shared = [...byComponent.values()].filter(names => names.length > 1);
        expect(shared).toEqual([]);
    });

    it('is large enough to be worth searching', () => {
        expect(TAB_ICON_NAMES.length).toBeGreaterThanOrEqual(100);
    });
});
