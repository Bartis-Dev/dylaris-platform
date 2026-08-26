import { describe, it, expect } from 'vitest';
import { saveState, type SavableForm } from './SettingsCard';

// The rule this pins: a settings card's Save is present in every state and
// commits in exactly one of them.
//
// The panel used to hide the control entirely until something was dirty, in a
// bar at the bottom of the viewport. Two things came out of that: the operator
// could not tell a saved page from an unsaveable one, and the button appeared
// under the cursor at the moment it became relevant. Both are why 'idle' is a
// rendered state here rather than nothing.

const form = (over: Partial<SavableForm> = {}): SavableForm => ({
    dirty: false,
    saving: false,
    save: async () => true,
    discard: () => {},
    ...over,
});

describe('saveState', () => {
    const cases: [string, SavableForm, string | undefined, ReturnType<typeof saveState>][] = [
        ['a clean form offers nothing to write', form(), undefined, 'idle'],
        ['an edited form is the one state that saves', form({ dirty: true }), undefined, 'ready'],
        ['a save in flight is not another save', form({ dirty: true, saving: true }), undefined, 'saving'],

        // A failed load renders the component's own defaults. Comparing those
        // against nothing is clean, so `dirty` is false - and reporting idle
        // there would say "nothing to save" about a form that CANNOT be saved.
        ['a failed load is blocked, not idle', form({ loadFailed: true }), undefined, 'blocked'],
        ['a failed load stays blocked once edited', form({ dirty: true, loadFailed: true }), undefined, 'blocked'],

        // Validation. The reason is the trigger, so an empty string must not
        // block - that is the shape a call site produces when it means "valid".
        ['an invalid form is refused', form({ dirty: true }), 'Retention must look like 3d', 'blocked'],
        ['a clean form is blocked while invalid too', form(), 'Retention must look like 3d', 'blocked'],
        ['an empty reason does not block', form({ dirty: true }), '', 'ready'],

        // saving wins over both: the request is already out with the value it
        // captured, and the answer to "is it valid" no longer changes that.
        ['an in-flight save outranks a block', form({ dirty: true, loadFailed: true, saving: true }), undefined, 'saving'],
    ];

    for (const [name, f, reason, expected] of cases) {
        it(name, () => {
            expect(saveState(f, reason)).toBe(expected);
        });
    }

    it('only ready ever commits', () => {
        const states = cases.map(([, f, reason]) => saveState(f, reason));
        const committing = states.filter(s => s === 'ready');
        expect(committing).toHaveLength(2);
    });
});
