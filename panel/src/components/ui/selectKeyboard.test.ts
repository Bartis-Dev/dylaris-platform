import { describe, it, expect } from 'vitest';
import { selectKeyReducer, type SelectKeyState } from './selectKeyboard';

const closed: SelectKeyState = { open: false, highlight: -1 };

describe('selectKeyReducer - closed', () => {
    it('ArrowDown opens and highlights the selected index', () => {
        const r = selectKeyReducer(closed, 'ArrowDown', 3, 2);
        expect(r.state).toEqual({ open: true, highlight: 2 });
        expect(r.commit).toBeUndefined();
    });
    it('opens at 0 when nothing is selected', () => {
        expect(selectKeyReducer(closed, 'ArrowUp', 3, -1).state).toEqual({ open: true, highlight: 0 });
    });
    it('Enter opens', () => {
        expect(selectKeyReducer(closed, 'Enter', 3, 0).state.open).toBe(true);
    });
    it('Escape stays closed', () => {
        expect(selectKeyReducer(closed, 'Escape', 3, 0).state).toEqual(closed);
    });
});

describe('selectKeyReducer - open', () => {
    const open: SelectKeyState = { open: true, highlight: 1 };
    it('ArrowDown moves down and clamps at the end', () => {
        expect(selectKeyReducer(open, 'ArrowDown', 3, 1).state.highlight).toBe(2);
        expect(selectKeyReducer({ open: true, highlight: 2 }, 'ArrowDown', 3, 2).state.highlight).toBe(2);
    });
    it('ArrowUp moves up and clamps at 0', () => {
        expect(selectKeyReducer(open, 'ArrowUp', 3, 1).state.highlight).toBe(0);
        expect(selectKeyReducer({ open: true, highlight: 0 }, 'ArrowUp', 3, 0).state.highlight).toBe(0);
    });
    it('Enter commits the highlight and closes', () => {
        const r = selectKeyReducer(open, 'Enter', 3, 1);
        expect(r.commit).toBe(1);
        expect(r.state.open).toBe(false);
    });
    it('Escape closes without committing', () => {
        const r = selectKeyReducer(open, 'Escape', 3, 1);
        expect(r.commit).toBeUndefined();
        expect(r.state.open).toBe(false);
    });
    it('an unhandled key is a no-op', () => {
        expect(selectKeyReducer(open, 'x', 3, 1)).toEqual({ state: open });
    });
});
