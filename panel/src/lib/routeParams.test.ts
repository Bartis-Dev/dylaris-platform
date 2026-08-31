import { describe, it, expect } from 'vitest';
import { routeIdFrom } from './routeParams';
import { EXPORT_PARAM } from './exportParam';

// The panel is a static export: every dynamic route is prerendered once under
// EXPORT_PARAM, and Next then reports that placeholder as the param value for
// every id. Reading the address bar is the fix, and this pins the mapping.
//
// The bug it replaces was total and silent. Every server, modpack, module,
// ticket, proxied tab and share link rendered "not found", while the list
// pages, login, settings and the entire API were fine - so nothing failed
// except the thing a user does immediately after creating a server.
describe('routeIdFrom', () => {
    const cases: Array<[string, string, string]> = [
        ['/servers/1', 'servers', '1'],
        ['/servers/1/console', 'servers', '1'],
        ['/servers/12/config/scheduled', 'servers', '12'],
        ['/servers/1/t/5', 'servers', '1'],
        ['/servers/1/t/5', 't', '5'],
        ['/modpacks/7', 'modpacks', '7'],
        ['/modpacks/7/builds/9', 'modpacks', '7'],
        ['/modpacks/7/builds/9', 'builds', '9'],
        ['/modules/3', 'modules', '3'],
        ['/tickets/4', 'tickets', '4'],
        ['/c/sh4re-t0ken', 'c', 'sh4re-t0ken'],
        // A trailing slash and a missing collection are both "no id", which is
        // what every caller already handles.
        ['/servers', 'servers', ''],
        ['/servers/', 'servers', ''],
        ['/settings/nodes', 'servers', ''],
    ];
    for (const [path, collection, want] of cases) {
        it(`${path} + ${collection} -> ${want || '(none)'}`, () => {
            expect(routeIdFrom(path, collection)).toBe(want);
        });
    }

    // An exact segment match, so a route WORD that merely looks similar does not
    // answer: /servers/1/config/tabs must not read like the /t/ tab route.
    it('matches a whole segment, not a prefix', () => {
        expect(routeIdFrom('/servers/1/config/tabs', 't')).toBe('');
        expect(routeIdFrom('/servers/1/config/tabs', 'tabs')).toBe('');
    });

    // Landing on the prerendered URL itself yields no id rather than the literal
    // placeholder, which would otherwise be sent to the API as if it were one.
    it('never hands back the export placeholder', () => {
        expect(routeIdFrom(`/servers/${EXPORT_PARAM}`, 'servers')).toBe('');
    });

    // A share token can carry characters that are percent-encoded in the URL.
    it('decodes the segment', () => {
        expect(routeIdFrom('/c/a%2Fb', 'c')).toBe('a/b');
    });
});
