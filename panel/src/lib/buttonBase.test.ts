import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { classStringsMissingBase } from './buttonBase';

describe('classStringsMissingBase', () => {
    it('flags a modifier used on its own', () => {
        expect(classStringsMissingBase('<b className="btn-primary shrink-0" />')).toEqual(['btn-primary shrink-0']);
    });

    it('accepts the base plus a modifier', () => {
        expect(classStringsMissingBase('<b className="btn btn-primary" />')).toEqual([]);
    });

    // "btn-primary" contains "btn". A naive substring check passes every broken
    // case, which is exactly how this survived 442 call sites.
    it('does not read the modifier itself as the base class', () => {
        expect(classStringsMissingBase('<b className="btn-icon btn-sm" />')).toHaveLength(1);
    });

    it('reads template literals too', () => {
        expect(classStringsMissingBase('<b className={`btn-secondary ${x}`} />')).toHaveLength(1);
        expect(classStringsMissingBase('<b className={`btn btn-secondary ${x}`} />')).toEqual([]);
    });

    it('ignores class strings with no button in them', () => {
        expect(classStringsMissingBase('<b className="flex gap-2" />')).toEqual([]);
    });
});

// The sweep. Every .tsx under src, one failure line per offending class string,
// so the report names the sites rather than the files.
describe('every button in the panel carries the base class', () => {
    const files: string[] = [];
    (function walk(dir: string) {
        for (const e of readdirSync(dir)) {
            const p = join(dir, e);
            if (statSync(p).isDirectory()) walk(p);
            else if (p.endsWith('.tsx')) files.push(p);
        }
    })('src');

    it('finds no modifier used without it', () => {
        const offenders: string[] = [];
        for (const f of files) {
            for (const cls of classStringsMissingBase(readFileSync(f, 'utf8'))) {
                offenders.push(`${f}: className="${cls}"`);
            }
        }
        expect(offenders).toEqual([]);
    });
});
