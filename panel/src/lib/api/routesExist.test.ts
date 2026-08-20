import { describe, expect, it } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';

/**
 * Every API path the panel calls must be a route Core actually serves.
 *
 * A mistyped or renamed path is invisible to everything else here: TypeScript
 * sees a string, the unit tests mock fetch, and the build succeeds. It surfaces
 * as a 404 in front of a user. The route table in ../../API.md is generated from
 * Core's router and held to it by two Go tests, so it can be trusted as the
 * other half of this comparison.
 *
 * The scan is deliberately conservative: it only reads shapes it recognises and
 * ignores everything else. A missed call site costs nothing, a false failure
 * would cost the next person their afternoon.
 */

const PANEL_ROOT = path.resolve(__dirname, '../../..');
const SRC = path.join(PANEL_ROOT, 'src');
const API_DOC = path.join(PANEL_ROOT, '..', 'API.md');

/** Markdown cells, honouring the \| escape the generator writes into Notes. */
function splitRow(line: string): string[] {
    const cells: string[] = [];
    let cur = '';
    const s = line.trim().replace(/^\|/, '').replace(/\|$/, '');
    for (let i = 0; i < s.length; i++) {
        if (s[i] === '\\' && s[i + 1] === '|') {
            cur += '|';
            i++;
        } else if (s[i] === '|') {
            cells.push(cur.trim());
            cur = '';
        } else {
            cur += s[i];
        }
    }
    cells.push(cur.trim());
    return cells;
}

/**
 * A route template as comparable segments. `{id:[0-9]+}` becomes a
 * single-segment wildcard; `{rest:.*}` and `{domain:.+}` match the rest of the
 * path, so they become a tail wildcard.
 */
function routeSegments(template: string): string[] {
    return template
        .slice('/api/'.length)
        .split('/')
        .map((seg) => {
            if (!seg.startsWith('{')) return seg;
            return seg.includes('.*') || seg.includes('.+') ? '**' : '*';
        });
}

function loadRoutes(): string[][] {
    const doc = readFileSync(API_DOC, 'utf8');
    const out: string[][] = [];
    for (const line of doc.split('\n')) {
        if (!line.startsWith('| ') || line.startsWith('| ---')) continue;
        const cells = splitRow(line);
        if (cells.length !== 7) continue;
        const p = cells[1].replace(/`/g, '');
        if (p.startsWith('/api/')) out.push(routeSegments(p));
    }
    return out;
}

function sourceFiles(dir: string): string[] {
    const out: string[] = [];
    for (const entry of readdirSync(dir)) {
        const full = path.join(dir, entry);
        if (statSync(full).isDirectory()) {
            out.push(...sourceFiles(full));
        } else if (/\.tsx?$/.test(entry) && !entry.includes('.test.')) {
            out.push(full);
        }
    }
    return out;
}

/**
 * Reads a template path starting right after `${API_URL}`, keeping balanced
 * `${...}` groups intact so a nested expression does not end the scan early.
 */
function scanTemplatePath(src: string, from: number): string {
    let out = '';
    let depth = 0;
    for (let i = from; i < src.length; i++) {
        const ch = src[i];
        if (depth === 0 && '`\'" ,);\n'.includes(ch)) break;
        if (src.startsWith('${', i)) {
            depth++;
            out += '${';
            i++;
            continue;
        }
        if (depth > 0 && ch === '{') depth++;
        if (depth > 0 && ch === '}') depth--;
        out += ch;
    }
    return out;
}

const EXPR = /\$\{[^{}]*(?:\{[^{}]*\}[^{}]*)*\}/g;

/**
 * Every string literal in src, read with a scanner rather than a regex: a
 * template literal may contain another one inside `${...}`, and a regex that
 * stops at the first closing backtick truncates the path mid-expression.
 */
function stringLiterals(src: string): string[] {
    const out: string[] = [];
    for (let i = 0; i < src.length; i++) {
        const quote = src[i];
        if (quote !== '`' && quote !== "'" && quote !== '"') continue;
        let body = '';
        let depth = 0;
        let j = i + 1;
        for (; j < src.length; j++) {
            if (src[j] === '\\') {
                body += src[j] + (src[j + 1] ?? '');
                j++;
                continue;
            }
            if (quote === '`' && src.startsWith('${', j)) {
                depth++;
                body += '${';
                j++;
                continue;
            }
            if (depth > 0 && src[j] === '{') depth++;
            if (depth > 0 && src[j] === '}') {
                depth--;
                body += '}';
                continue;
            }
            if (depth === 0 && src[j] === quote) break;
            if (depth === 0 && quote !== '`' && src[j] === '\n') break;
            body += src[j];
        }
        out.push(body);
        i = j;
    }
    return out;
}

/** A call path reduced to comparable segments, with `${x}` as a wildcard. */
function callSegments(raw: string): string[] {
    const withoutQuery = raw.replace(EXPR, '*').split('?')[0];
    return withoutQuery.split('/').filter((s) => s !== '');
}

function collectCalls(families: Set<string>): Map<string, Set<string>> {
    const calls = new Map<string, Set<string>>();
    const add = (p: string, file: string, requireFamily: boolean) => {
        if (!p.startsWith('/')) return;
        // A bare literal in the API layer might be a navigation target rather
        // than an endpoint ('/login' after a 401). Requiring the first segment
        // to name a real API family keeps those out. The cost is that a typo in
        // the FIRST segment goes unnoticed; the failure this test is built for
        // is a path renamed in Go and missed here, which keeps its family.
        if (requireFamily && !families.has(p.split('/')[1] ?? '')) return;
        if (!calls.has(p)) calls.set(p, new Set());
        calls.get(p)!.add(path.relative(PANEL_ROOT, file));
    };

    for (const file of sourceFiles(SRC)) {
        const src = readFileSync(file, 'utf8');

        // `${API_URL}/servers/${id}/...` - used all over the panel. This one is
        // unambiguous, so it is taken whatever the first segment says.
        for (let i = src.indexOf('${API_URL}'); i !== -1; i = src.indexOf('${API_URL}', i + 1)) {
            add(scanTemplatePath(src, i + '${API_URL}'.length), file, false);
        }

        // The API client layer passes bare paths to its own helpers (fetchAPI,
        // and per-module get/send wrappers that prepend API_URL). Restricting
        // this to src/lib/api keeps the app's navigation paths out of it.
        if (file.includes(path.join('lib', 'api'))) {
            for (const lit of stringLiterals(src)) {
                if (lit.startsWith('/')) add(lit, file, true);
            }
        }
    }
    return calls;
}

function matches(call: string[], route: string[]): boolean {
    const tail = route.indexOf('**');
    if (tail !== -1) {
        if (call.length < tail) return false;
        return route
            .slice(0, tail)
            .every((seg, i) => seg === call[i] || seg === '*' || call[i] === '*');
    }
    if (call.length !== route.length) return false;
    return route.every((seg, i) => seg === call[i] || seg === '*' || call[i] === '*');
}

describe('panel API calls', () => {
    const routes = loadRoutes();

    it('has a route table to compare against', () => {
        // Guards the whole test: a moved or unreadable API.md would otherwise
        // make every call "unmatched", or match nothing and pass vacuously.
        expect(routes.length).toBeGreaterThan(400);
    });

    it('only calls paths that Core actually serves', () => {
        const families = new Set(routes.map((r) => r[0]).filter((s) => !s.startsWith('*')));
        const calls = collectCalls(families);
        expect(calls.size).toBeGreaterThan(100);

        const unmatched: string[] = [];
        for (const [raw, files] of calls) {
            const segs = callSegments(raw);
            if (segs.length === 0) continue;

            // A trailing wildcard can be a query string rather than another
            // path segment, so a path is fine if either reading resolves.
            const last = segs[segs.length - 1];
            const alternatives = [segs];
            if (last.endsWith('*') && last !== '*') {
                alternatives.push([...segs.slice(0, -1), last.slice(0, -1)]);
            }

            if (!alternatives.some((a) => routes.some((r) => matches(a, r)))) {
                unmatched.push(`${raw}  (${[...files].sort().join(', ')})`);
            }
        }

        expect(
            unmatched.sort(),
            'these paths reach no route in API.md - a typo here is a 404 in front of a user',
        ).toEqual([]);
    });
});
