// Pure keyboard-navigation logic for the branded Select. Extracted so the
// behavior is unit-testable without a DOM (the panel has no DOM test tooling).

export interface SelectKeyState {
    open: boolean;
    highlight: number; // index into the option list, -1 when none
}

function clamp(n: number, count: number): number {
    if (count <= 0) return -1;
    return Math.max(0, Math.min(n, count - 1));
}

// Reduce a keydown into the next state, optionally committing an option index.
// selectedIndex is the currently-selected option (or -1) used to seed the
// highlight when the menu opens.
export function selectKeyReducer(
    state: SelectKeyState,
    key: string,
    count: number,
    selectedIndex: number,
): { state: SelectKeyState; commit?: number } {
    if (!state.open) {
        if (key === 'ArrowDown' || key === 'ArrowUp' || key === 'Enter' || key === ' ') {
            return { state: { open: true, highlight: selectedIndex >= 0 ? selectedIndex : 0 } };
        }
        return { state };
    }
    switch (key) {
        case 'ArrowDown':
            return { state: { open: true, highlight: clamp(state.highlight + 1, count) } };
        case 'ArrowUp':
            return { state: { open: true, highlight: clamp(state.highlight - 1, count) } };
        case 'Enter':
        case ' ':
            return { state: { open: false, highlight: state.highlight }, commit: state.highlight };
        case 'Escape':
        case 'Tab':
            return { state: { open: false, highlight: state.highlight } };
        default:
            return { state };
    }
}
