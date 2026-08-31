import { describe, it, expect, vi, afterEach } from 'vitest';
import { wailsAPIBase, type WailsAppBindings } from './wailsBeamAdapter';

// The Beam app hands its native side a session so the relay lookup is
// authenticated, and the Go side accepts an apiURL only when its origin is the
// panel the app was pointed at. That check cannot be argued with - it is what
// stops a compromised page aiming a credential-bearing client somewhere else.
//
// The value being handed over used to be window.location.origin + '/api', which
// inside the webview is always wails.localhost, because proxying the panel onto
// that origin is the entire design. So it named a host the check can never
// match, and the handoff was refused every time, silently: the failure is a
// caught promise and a console.warn.
//
// The test is on the VALUE rather than the outcome, because a refused handoff
// looks exactly like a successful one from the panel's side.

const app = (over: Partial<WailsAppBindings>) => over as unknown as WailsAppBindings;

afterEach(() => { vi.unstubAllGlobals(); });

describe('the API base handed to the Beam app', () => {
    it('is built from the panel the app itself was pointed at', async () => {
        expect(await wailsAPIBase(app({ GetPanelURL: async () => 'https://panel.example.com' })))
            .toBe('https://panel.example.com/api');
    });

    it('does not double the slash on a URL that has one', async () => {
        expect(await wailsAPIBase(app({ GetPanelURL: async () => 'https://panel.example.com///' })))
            .toBe('https://panel.example.com/api');
    });

    // The old expression stays reachable for an app build that predates the
    // binding. It is wrong for the origin check, but it is what today does, and
    // regressing an old app into a hard failure would be worse than leaving it.
    it('falls back when the app cannot say', async () => {
        vi.stubGlobal('window', { location: { origin: 'http://wails.localhost' } });
        expect(await wailsAPIBase(app({}))).toBe('http://wails.localhost/api');
    });

    it('falls back when the binding throws or answers empty', async () => {
        vi.stubGlobal('window', { location: { origin: 'http://wails.localhost' } });
        expect(await wailsAPIBase(app({ GetPanelURL: async () => { throw new Error('boom'); } })))
            .toBe('http://wails.localhost/api');
        expect(await wailsAPIBase(app({ GetPanelURL: async () => '   ' })))
            .toBe('http://wails.localhost/api');
    });
});
