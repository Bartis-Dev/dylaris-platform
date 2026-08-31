import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { linesClaimingZeroIsUnlimited, ZERO_IS_UNLIMITED_ALLOWED } from './limitWording';

describe('linesClaimingZeroIsUnlimited', () => {
    it('catches the spellings the panel actually used', () => {
        expect(linesClaimingZeroIsUnlimited('0 = unlimited')).toHaveLength(1);
        expect(linesClaimingZeroIsUnlimited('A 0 means unlimited for this user.')).toHaveLength(1);
        expect(linesClaimingZeroIsUnlimited('Enter 0 for unlimited')).toHaveLength(1);
        expect(linesClaimingZeroIsUnlimited('0 = no limit. Example: 2.0 = 2 cores')).toHaveLength(1);
    });

    it('reports the line number, so the site is named and not just the file', () => {
        expect(linesClaimingZeroIsUnlimited('a\nb\n0 = unlimited')).toEqual(['3: 0 = unlimited']);
    });

    // The convention's own wording has a zero in it and must not trip this.
    it('leaves correct copy alone', () => {
        expect(linesClaimingZeroIsUnlimited('0 means none: they may hold zero of this.')).toEqual([]);
        expect(linesClaimingZeroIsUnlimited('Leave it empty for no limit; 0 means none.')).toEqual([]);
        expect(linesClaimingZeroIsUnlimited('min={0}')).toEqual([]);
    });
});

// The sweep. Every source file under src, one failure line per offending claim.
describe('nothing tells an operator that zero means unlimited', () => {
    const files: string[] = [];
    (function walk(dir: string) {
        for (const e of readdirSync(dir)) {
            const p = join(dir, e);
            if (statSync(p).isDirectory()) walk(p);
            // Tests only, not sources: a fixture quoting the wrong wording in
            // order to assert it is caught is not a claim made to an operator.
            else if (/\.tsx?$/.test(p) && !/\.test\.tsx?$/.test(p)) files.push(p);
        }
    })('src');

    it('finds the claim only where a Docker or throttle limit genuinely means it', () => {
        const offenders: string[] = [];
        for (const f of files) {
            const key = f.split('\\').join('/');
            if (key in ZERO_IS_UNLIMITED_ALLOWED) continue;
            for (const line of linesClaimingZeroIsUnlimited(readFileSync(f, 'utf8'))) {
                offenders.push(`${key}:${line}`);
            }
        }
        expect(offenders).toEqual([]);
    });

    // An allowlist entry for a file that no longer makes the claim is stale, and
    // a stale entry is a free pass waiting for the next edit to use it.
    it('has no allowlist entry that is no longer needed', () => {
        const unused = Object.keys(ZERO_IS_UNLIMITED_ALLOWED).filter(f => {
            if (f === 'src/lib/limitWording.ts') return false; // documents the rule itself
            try {
                return linesClaimingZeroIsUnlimited(readFileSync(f, 'utf8')).length === 0;
            } catch {
                return true; // the file is gone
            }
        });
        expect(unused).toEqual([]);
    });
});
