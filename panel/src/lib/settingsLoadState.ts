/**
 * What a settings screen should render, given how its load went.
 *
 * The three states are not obvious in the way they look on screen, which is why
 * they are decided here instead of in a chain of ternaries per screen:
 *
 *   loading  the request is out. A skeleton.
 *   failed   it came back with nothing usable. NOT the form: a form renders its
 *            own defaults, and defaults are indistinguishable from a real
 *            configuration - no limits, no domains, every toggle off. An
 *            operator reading that concludes their settings were lost.
 *   ready    values arrived. The form.
 *
 * "failed" outranks "loading" being false rather than being folded into it,
 * because a screen that lifts its skeleton on failure is exactly how the wrong
 * values get shown as if they were right.
 */
export type SettingsLoadState = 'loading' | 'failed' | 'ready';

export function settingsLoadState(loading: boolean, loadFailed: boolean): SettingsLoadState {
    if (loading) return 'loading';
    if (loadFailed) return 'failed';
    return 'ready';
}

/**
 * Whether anything typed into the form could actually be written.
 *
 * dirty is measured against a snapshot that only a SUCCESSFUL load sets, so
 * after a failure it stays false no matter what is typed - the save bar never
 * appears and nothing explains why. Callers use this to refuse the form
 * outright rather than presenting one that silently cannot be saved.
 */
export function settingsFormUsable(loading: boolean, loadFailed: boolean): boolean {
    return settingsLoadState(loading, loadFailed) === 'ready';
}
