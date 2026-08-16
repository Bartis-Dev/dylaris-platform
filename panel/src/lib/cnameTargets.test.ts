import { describe, it, expect } from 'vitest';
import { cnameTargetsFor } from './cnameTargets';

describe('cnameTargetsFor', () => {
    it('expands one label into one target per hoster domain', () => {
        expect(cnameTargetsFor('route', [
            { domain: 'eu.dylaris.com' },
            { domain: 'us-east.dylaris.com' },
        ])).toEqual(['route.eu.dylaris.com', 'route.us-east.dylaris.com']);
    });

    it('returns nothing without a label or without domains', () => {
        expect(cnameTargetsFor('', [{ domain: 'eu.dylaris.com' }])).toEqual([]);
        expect(cnameTargetsFor('   ', [{ domain: 'eu.dylaris.com' }])).toEqual([]);
        expect(cnameTargetsFor('route', [])).toEqual([]);
    });

    it('normalises case and whitespace on both sides', () => {
        expect(cnameTargetsFor('  Route  ', [{ domain: ' EU.Dylaris.com ' }]))
            .toEqual(['route.eu.dylaris.com']);
    });

    it('skips empty domains instead of producing "route."', () => {
        expect(cnameTargetsFor('route', [
            { domain: '' },
            { domain: '   ' },
            { domain: 'eu.dylaris.com' },
        ])).toEqual(['route.eu.dylaris.com']);
    });

    it('deduplicates targets, since two entries may differ only by validation mode', () => {
        expect(cnameTargetsFor('route', [
            { domain: 'eu.dylaris.com' },
            { domain: 'eu.dylaris.com' },
        ])).toEqual(['route.eu.dylaris.com']);
    });
});
