import { readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const HERE = path.dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1'));

function pageFiles(dir: string): string[] {
    const out: string[] = [];
    for (const e of readdirSync(dir, { withFileTypes: true })) {
        const p = path.join(dir, e.name);
        if (e.isDirectory()) out.push(...pageFiles(p));
        else if (e.name === 'page.tsx') out.push(p);
    }
    return out;
}

// Every root a page can return, as (line, className).
//
// EVERY one, not the first: this check found three offenders while it read only
// the top-level return, and the custom-tab page turned out to return four roots
// - a loading skeleton, a pending skeleton, a proxied frame and a direct one -
// all four wrong, one of them seen. A page's early-return branches are the
// states a reader actually spends time in.
//
// The indent bound is what separates a component's own returns from the ones
// inside a .map() callback, which are list items whose height their parent
// gives them.
function pageRoots(source: string): { line: number; cls: string }[] {
    const out: { line: number; cls: string }[] = [];
    const re = /\n {4,16}return \(\s*\n\s*<[A-Za-z][\w.]*\s+className="([^"]*)"/g;
    for (const m of source.matchAll(re)) {
        out.push({ line: source.slice(0, m.index).split('\n').length + 1, cls: m[1] });
    }
    return out;
}

// ServerShell puts every server page inside `flex-1 overflow-y-auto p-6`. That
// box is a BLOCK, so `flex-1` on a page's root sizes nothing: the page is as
// tall as its content and the SHELL scrolls.
//
// For a page that just stacks cards that is fine and intended. For a page whose
// root says `overflow-hidden` it is not, and the two failures arrive together:
// the panes inside never reach their own overflow, so the whole window scrolls
// instead of the list, and whatever the page clips is clipped at a height
// nothing constrains - unreachable, with no scrollbar offering it.
//
// The rule is therefore about one root: a root that hides its overflow has to
// take a height. `h-full` is that height, because the shell's box has a
// definite one. files/page.tsx got this right first.
//
// This is a source check and it names the SITES rather than the file: it
// enumerates every page under servers/[id] and judges each root on its own,
// so a new page written from the wrong template fails here rather than being
// absent from a whole-file grep that was green all along.
describe('server page roots', () => {
    const files = pageFiles(HERE);

    it('finds the pages it is supposed to be checking', () => {
        expect(files.length).toBeGreaterThan(8);
    });

    it.each(files.map(f => [path.relative(HERE, f), f]))('%s', (rel, file) => {
        const offenders = pageRoots(readFileSync(file, 'utf8'))
            .filter(r => r.cls.includes('overflow-hidden') && !r.cls.includes('h-full'))
            .map(r => `${rel}:${r.line}  ${r.cls}`);
        expect(
            offenders,
            'these roots hide their overflow but take no height. ServerShell puts a page in a ' +
            'scrolling block, so the window scrolls instead of the panes inside, and anything ' +
            'the root clips has no scrollbar offering it - measured: an iframe meant to fill ' +
            'the tab rendered 150px tall in a 764px box',
        ).toEqual([]);
    });
});
