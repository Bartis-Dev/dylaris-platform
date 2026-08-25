import { describe, it, expect, vi } from 'vitest';
import { aggregate, type UnsavedChangesRegistration } from './UnsavedChanges';

function reg(over: Partial<UnsavedChangesRegistration> = {}): UnsavedChangesRegistration {
    return { dirty: false, saving: false, save: () => {}, discard: () => {}, ...over };
}

describe('aggregate', () => {
    // Nothing registered is not the same as registered-and-clean: the save bar
    // and the navigation guard both treat null as "there is no form here".
    it('is null with no registrations', () => {
        expect(aggregate([])).toBeNull();
    });

    // The rule this decides: one dirty section on a page of several is enough
    // to raise the bar and to prompt on navigation. Before the map, the second
    // section to mount replaced the first, so the first section's edits could
    // leave without a prompt.
    it('is dirty when any section is dirty', () => {
        const agg = aggregate([reg(), reg({ dirty: true }), reg()]);
        expect(agg?.dirty).toBe(true);
    });

    it('is clean only when every section is clean', () => {
        expect(aggregate([reg(), reg()])?.dirty).toBe(false);
    });

    it('reports saving while any section is saving', () => {
        expect(aggregate([reg(), reg({ saving: true })])?.saving).toBe(true);
    });

    // Saves run one after another, not concurrently: two sections of one page
    // routinely write the same settings table, and two concurrent writes of it
    // resolve by network timing rather than by what the operator did last.
    it('saves dirty sections in sequence', async () => {
        const order: string[] = [];
        const slow = reg({
            dirty: true,
            save: async () => {
                order.push('a:start');
                await new Promise(r => setTimeout(r, 10));
                order.push('a:end');
            },
        });
        const fast = reg({
            dirty: true,
            save: () => {
                order.push('b');
            },
        });

        await aggregate([slow, fast])!.save();

        expect(order).toEqual(['a:start', 'a:end', 'b']);
    });

    // A clean section's save handler is entitled to assume it has something to
    // write, so it must not be called.
    it('skips clean sections when saving', async () => {
        const clean = vi.fn();
        await aggregate([reg({ save: clean }), reg({ dirty: true })])!.save();
        expect(clean).not.toHaveBeenCalled();
    });

    // Discard is the opposite: it resets every section, dirty or not, because
    // the button says Discard and leaving one section holding an edit after it
    // would be a form that looks reset and is not.
    it('discards every section', () => {
        const a = vi.fn();
        const b = vi.fn();
        aggregate([reg({ dirty: true, discard: a }), reg({ discard: b })])!.discard();
        expect(a).toHaveBeenCalledOnce();
        expect(b).toHaveBeenCalledOnce();
    });
});
