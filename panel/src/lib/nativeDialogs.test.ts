import { describe, it, expect } from 'vitest';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';

// The panel must not ask through the browser's native dialogs. Two separate
// commits removed them (alert() in 4b3289b, confirm() in 17e0a6f) and the second
// one MISSED FOUR call sites while its message claimed all fifteen were done -
// the count came from a grep run by hand. A hand-run grep is exactly what this
// replaces: the "did we get all of them" question is now answered by the suite
// rather than by whoever is writing the commit message.
//
// Why they must go, beyond looking like OS chrome on a dark panel: a browser that
// has suppressed further dialogs for the page - Chrome offers that after a few -
// makes confirm() return false and alert() a no-op WITHOUT asking. The action then
// silently does not happen and the user is told nothing. One of the four missed
// sites was "Reset pairing" on a node, another three gated every modpack-aware mod
// install.
const SRC = join(__dirname, '..');

// ConfirmDialog is the replacement and names what it replaces in its own doc
// comment; this file quotes the calls it is banning.
const EXEMPT = ['components/ui/ConfirmDialog.tsx', 'lib/nativeDialogs.test.ts'];

function walk(dir: string, out: string[] = []): string[] {
    for (const entry of readdirSync(dir)) {
        if (entry === 'node_modules') continue;
        const full = join(dir, entry);
        if (statSync(full).isDirectory()) walk(full, out);
        else if (/\.(ts|tsx)$/.test(entry)) out.push(full);
    }
    return out;
}

// Matches a real call, not the word inside a comment or a string.
const NATIVE_CALL = /(?<![\w.])(?:window\s*\.\s*)?(confirm|alert|prompt)\s*\(/g;

describe('no native browser dialogs', () => {
    const files = walk(SRC);

    it('finds source files to scan', () => {
        expect(files.length).toBeGreaterThan(50);
    });

    it.each(files.map((f) => relative(SRC, f).replace(/\\/g, '/')))('%s', (rel) => {
        if (EXEMPT.includes(rel)) return;
        const src = readFileSync(join(SRC, rel), 'utf8');
        const hits: string[] = [];
        for (const line of src.split('\n')) {
            const code = line.replace(/\/\/.*$/, '').replace(/\/\*.*?\*\//g, '');
            // Strip string and template literals so prose mentioning the word
            // inside a message is not a hit.
            const bare = code
                .replace(/'(?:[^'\\]|\\.)*'/g, "''")
                .replace(/"(?:[^"\\]|\\.)*"/g, '""')
                .replace(/`(?:[^`\\]|\\.)*`/g, '``');
            NATIVE_CALL.lastIndex = 0;
            let m: RegExpExecArray | null;
            while ((m = NATIVE_CALL.exec(bare))) {
                // A local named confirm/alert would be a false positive; none exist
                // today, and one would still be worth renaming.
                hits.push(m[1]);
            }
        }
        expect(hits, `use confirmDialog() / showToast() instead of ${hits.join(', ')}()`).toEqual([]);
    });
});
