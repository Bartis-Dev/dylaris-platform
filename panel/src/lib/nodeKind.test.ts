import { describe, expect, it } from 'vitest';
import { nodeKind, isKind, hasExternalTag, type NodeKindFields } from './nodeKind';

// These mirror Core. The reason to test them at all is that both sides keep
// working when they disagree - a customer's machine simply appears under the
// operator's own heading, with nothing failing anywhere.
describe('nodeKind', () => {
    const cases: { name: string; node: NodeKindFields; want: string }[] = [
        { name: 'no owner, no tags', node: {}, want: 'platform' },
        { name: 'no owner, unrelated tags', node: { tags: 'eu,ssd' }, want: 'platform' },
        { name: 'external tag alone', node: { tags: 'external' }, want: 'external' },
        { name: 'external tag among others', node: { tags: 'eu, external ,ssd' }, want: 'external' },
        { name: 'owner set', node: { ownerId: 'u-1' }, want: 'byon' },
        // The one that actually matters: ownership wins over the tag. Core's
        // external predicate requires OwnerID == nil, so a tagged tenant node
        // must never surface under "your own machines".
        { name: 'owner set AND external tag', node: { ownerId: 'u-1', tags: 'external' }, want: 'byon' },
        // An owner column that is present but empty is not an owner. Core reads
        // a nil pointer; JSON with omitempty can still deliver "".
        { name: 'empty owner string', node: { ownerId: '' }, want: 'platform' },
        { name: 'null owner', node: { ownerId: null, tags: null }, want: 'platform' },
    ];

    for (const c of cases) {
        it(c.name + ' -> ' + c.want, () => {
            expect(nodeKind(c.node)).toBe(c.want);
        });
    }

    it('does not match a tag that merely contains "external"', () => {
        // "external-backup" is a different tag. Substring matching here would
        // quietly move machines between tabs the day somebody adds one.
        expect(hasExternalTag({ tags: 'external-backup' })).toBe(false);
        expect(hasExternalTag({ tags: 'not-external' })).toBe(false);
    });

    it('partitions a fleet with no node in two tabs and none left out', () => {
        const fleet: NodeKindFields[] = [
            {}, { tags: 'external' }, { ownerId: 'u-1' }, { ownerId: 'u-2', tags: 'external' }, { tags: 'eu' },
        ];
        const platform = isKind(fleet, 'platform');
        const external = isKind(fleet, 'external');
        const byon = isKind(fleet, 'byon');
        expect(platform.length + external.length + byon.length).toBe(fleet.length);
        // Every node in exactly one bucket: the counts above already prove no
        // duplicates, given each bucket is a filter over the same list.
        expect(platform).toHaveLength(2);
        expect(external).toHaveLength(1);
        expect(byon).toHaveLength(2);
    });
});
