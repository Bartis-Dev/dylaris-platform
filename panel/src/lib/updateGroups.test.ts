import { describe, it, expect } from 'vitest';
import {
    groupInstances, compareVersions, serviceLabel, anythingOutdated,
    bellState, categories, formatDeadline, mergeReleases,
} from './updateGroups';
import type { UpdateInstance, Release, ReleaseEntry } from './api/updates';

const inst = (label: string, version: string, outdated = false): UpdateInstance =>
    ({ label, version: version || undefined, outdated });

describe('groupInstances', () => {
    // The case a single fleet-wide number could not express, and the reason node
    // versions travel per node.
    it('collapses instances by version and counts them against the total', () => {
        const groups = groupInstances([
            inst('node-a', '2026.08.28'),
            inst('node-b', '2026.08.28'),
            inst('node-c', '2026.08.20', true),
        ]);
        expect(groups).toHaveLength(2);
        // Outdated first: the reason to open this view is to find what needs doing.
        expect(groups[0]).toMatchObject({ version: '2026.08.20', count: 1, total: 3, outdated: true });
        expect(groups[1]).toMatchObject({ version: '2026.08.28', count: 2, total: 3, outdated: false });
    });

    it('reports a uniform fleet as one group', () => {
        const groups = groupInstances([inst('a', '2026.08.28'), inst('b', '2026.08.28')]);
        expect(groups).toHaveLength(1);
        expect(groups[0]).toMatchObject({ count: 2, total: 2 });
    });

    // "Not reporting" is its own state. It must never be rendered as behind: an
    // image built before release stamping reports nothing, and calling that
    // outdated would flag deployments nobody has touched.
    it('keeps unreported versions separate and not outdated', () => {
        const groups = groupInstances([inst('a', '2026.08.28'), inst('b', '')]);
        const unknown = groups.find(g => g.version === '');
        expect(unknown).toBeDefined();
        expect(unknown!.outdated).toBe(false);
        // ...and it sorts after anything outdated but before what is current, so
        // it reads as "look at this" without claiming a problem.
        expect(groups[groups.length - 1].version).toBe('2026.08.28');
    });

    it('carries the instance labels so the row can name them', () => {
        const groups = groupInstances([inst('node-fra-1', '2026.08.28'), inst('node-fra-2', '2026.08.28')]);
        expect(groups[0].labels).toEqual(['node-fra-1', 'node-fra-2']);
    });

    it('handles a component with no instances', () => {
        expect(groupInstances([])).toEqual([]);
    });
});

describe('compareVersions', () => {
    // The reason versions are compared part by part rather than as strings.
    it('orders the same-day counter numerically', () => {
        expect(compareVersions('2026.08.28.10', '2026.08.28.2')).toBe(1);
        expect(compareVersions('2026.08.28.2', '2026.08.28.10')).toBe(-1);
    });

    it('treats a bare date as older than the same date with a counter', () => {
        expect(compareVersions('2026.08.28.1', '2026.08.28')).toBe(1);
    });

    it('orders by date', () => {
        expect(compareVersions('2026.09.01', '2026.08.28')).toBe(1);
        expect(compareVersions('2025.12.31', '2026.01.01')).toBe(-1);
        expect(compareVersions('2026.08.28', '2026.08.28')).toBe(0);
    });

    it('sorts an unknown version lowest without crashing', () => {
        expect(compareVersions('', '2026.08.28')).toBe(-1);
        expect(compareVersions('2026.08.28', '')).toBe(1);
        expect(compareVersions('', '')).toBe(0);
    });
});

describe('bellState', () => {
    // The two states are separate on purpose. A release that touches nothing you
    // run is worth reading and not worth alarming you about, and collapsing them
    // is how a badge becomes something people learn to ignore.
    it('is attention when something you run is behind', () => {
        expect(bellState({ outdated: true, required: false, latest: '2026.08.28', seen: '2026.08.28' }))
            .toBe('attention');
    });

    it('is attention when a deadline applies, even with nothing else to say', () => {
        expect(bellState({ outdated: false, required: true, latest: '2026.08.28', seen: '2026.08.28' }))
            .toBe('attention');
    });

    it('is unseen for a new release that does not affect you', () => {
        expect(bellState({ outdated: false, required: false, latest: '2026.08.28', seen: '2026.08.20' }))
            .toBe('unseen');
    });

    it('is idle once acknowledged and nothing is behind', () => {
        expect(bellState({ outdated: false, required: false, latest: '2026.08.28', seen: '2026.08.28' }))
            .toBe('idle');
    });

    it('is idle when nothing has been published at all', () => {
        expect(bellState({ outdated: false, required: false })).toBe('idle');
    });
});

describe('anythingOutdated', () => {
    it('is true when any component is behind', () => {
        expect(anythingOutdated([
            { service: 'core', outdated: false, countable: true, instances: [] },
            { service: 'node', outdated: true, countable: true, instances: [] },
        ])).toBe(true);
    });
    it('is false for an empty or current fleet', () => {
        expect(anythingOutdated([])).toBe(false);
        expect(anythingOutdated([{ service: 'core', outdated: false, countable: true, instances: [] }])).toBe(false);
    });
});

describe('categories', () => {
    const empty: Release = { version: '2026.08.28', features: null, breaking: null, security: null, fixes: null };

    // All four, always. An absent Security heading reads as "nobody filled this
    // in", which is a very different statement from "no security fixes".
    it('returns all four even when every one is empty', () => {
        const cats = categories(empty);
        expect(cats.map(c => c.key)).toEqual(['breaking', 'security', 'features', 'fixes']);
        expect(cats.every(c => c.entries.length === 0)).toBe(true);
    });

    // Reader order, not file order: what forces action comes first.
    it('leads with breaking and security', () => {
        const cats = categories({ ...empty, features: [{ text: 'f' }], breaking: [{ text: 'b' }] });
        expect(cats[0].entries[0].text).toBe('b');
        expect(cats.find(c => c.key === 'features')!.entries[0].text).toBe('f');
    });
});

describe('serviceLabel', () => {
    it('names the known components and passes anything else through', () => {
        expect(serviceLabel('node')).toBe('Nodes');
        expect(serviceLabel('log-shipper')).toBe('Log shipper');
        expect(serviceLabel('something-new')).toBe('something-new');
    });
});

describe('formatDeadline', () => {
    it('returns the input unchanged when it is not a date', () => {
        expect(formatDeadline('not a date')).toBe('not a date');
    });
    it('renders a real timestamp as something other than the raw ISO string', () => {
        const out = formatDeadline('2026-09-05T14:00:00Z');
        expect(out).not.toBe('2026-09-05T14:00:00Z');
        expect(out).toContain('2026');
    });
});

describe('mergeReleases', () => {
    const rel = (version: string, over: Partial<Release> = {}): Release => ({
        version,
        features: null, breaking: null, security: null, fixes: null,
        ...over,
    });
    const entry = (text: string): ReleaseEntry => ({ text });

    it('returns null for nothing to show', () => {
        expect(mergeReleases([])).toBeNull();
    });

    it('renders a single release as a plain version, not a range', () => {
        const m = mergeReleases([rel('2026.08.30')])!;
        expect(m.range).toBe('2026.08.30');
        expect(m.count).toBe(1);
    });

    it('spans oldest to newest, the direction a person reads a range', () => {
        // The list arrives newest-first. Printing it in array order would read
        // "2026.08.30.4 - 2026.08.30.1", which is backwards to everyone.
        const m = mergeReleases([
            rel('2026.08.30.4'), rel('2026.08.30.3'), rel('2026.08.30.2'), rel('2026.08.30.1'),
        ])!;
        expect(m.range).toBe('2026.08.30.1 - 2026.08.30.4');
        expect(m.count).toBe(4);
    });

    it('pools each category across every release, newest entries first', () => {
        const m = mergeReleases([
            rel('2026.08.30.2', { breaking: [entry('newer break')], fixes: [entry('newer fix')] }),
            rel('2026.08.30.1', { breaking: [entry('older break')] }),
        ])!;
        const breaking = m.categories.find(c => c.key === 'breaking')!;
        expect(breaking.entries.map(e => e.text)).toEqual(['newer break', 'older break']);
        const fixes = m.categories.find(c => c.key === 'fixes')!;
        expect(fixes.entries.map(e => e.text)).toEqual(['newer fix']);
    });

    it('keeps all four categories so an empty one still reads as empty', () => {
        // "No security fixes this time" and "nobody filled this in" must not
        // look the same, which is why an empty category is kept rather than
        // dropped during the fold.
        const m = mergeReleases([rel('2026.08.30')])!;
        expect(m.categories.map(c => c.key)).toEqual(['breaking', 'security', 'features', 'fixes']);
    });

    it('carries the most urgent requirement, not the newest', () => {
        // A reader told about the later deadline would plan for a date the
        // earlier release has already passed.
        const m = mergeReleases([
            rel('2026.08.30.2', { required: { deadline: '2026-09-30', immediate: false } }),
            rel('2026.08.30.1', { required: { deadline: '2026-09-05', immediate: false } }),
        ])!;
        expect(m.required?.deadline).toBe('2026-09-05');
    });

    it('lets an immediate requirement outrank any date', () => {
        const m = mergeReleases([
            rel('2026.08.30.2', { required: { deadline: '2026-09-30', immediate: true } }),
            rel('2026.08.30.1', { required: { deadline: '2026-09-05', immediate: false } }),
        ])!;
        expect(m.required?.immediate).toBe(true);
    });
});
