import { describe, it, expect } from 'vitest';
import {
    VANILLA_SCHEMA,
    groupedSchema,
    getSchemaForSoftware,
    GROUP_LABELS,
    type PropertyDef,
} from './server-properties-schema';

describe('groupedSchema', () => {
    it('groups every entry from VANILLA_SCHEMA and drops none', () => {
        const grouped = groupedSchema(VANILLA_SCHEMA);
        const total = Object.values(grouped).reduce((sum, defs) => sum + defs.length, 0);
        expect(total).toBe(VANILLA_SCHEMA.length);
    });

    it('every property lands in the bucket matching its own group field', () => {
        const grouped = groupedSchema(VANILLA_SCHEMA);
        for (const [group, defs] of Object.entries(grouped)) {
            for (const def of defs) {
                expect(def.group).toBe(group);
            }
        }
    });

    it('preserves the original relative order of properties within a group', () => {
        const grouped = groupedSchema(VANILLA_SCHEMA);
        for (const [group, defs] of Object.entries(grouped)) {
            const expectedOrder = VANILLA_SCHEMA.filter((d) => d.group === group).map((d) => d.key);
            expect(defs.map((d) => d.key)).toEqual(expectedOrder);
        }
    });

    it('groups a small custom schema correctly, including an absent group', () => {
        const custom: PropertyDef[] = [
            { key: 'a', type: 'toggle', default: true, group: 'misc', label: 'A' },
            { key: 'b', type: 'toggle', default: false, group: 'world', label: 'B' },
            { key: 'c', type: 'toggle', default: false, group: 'misc', label: 'C' },
        ];
        const grouped = groupedSchema(custom);
        expect(grouped.misc.map((d) => d.key)).toEqual(['a', 'c']);
        expect(grouped.world.map((d) => d.key)).toEqual(['b']);
        expect(grouped.network).toBeUndefined();
    });

    it('returns an empty object for an empty schema', () => {
        expect(groupedSchema([])).toEqual({});
    });
});

describe('getSchemaForSoftware', () => {
    it('returns the VANILLA_SCHEMA reference regardless of the software argument', () => {
        expect(getSchemaForSoftware('paper')).toBe(VANILLA_SCHEMA);
        expect(getSchemaForSoftware()).toBe(VANILLA_SCHEMA);
        expect(getSchemaForSoftware('spigot')).toBe(VANILLA_SCHEMA);
        expect(getSchemaForSoftware('fabric')).toBe(VANILLA_SCHEMA);
    });

    it('every schema entry has a unique key', () => {
        const keys = getSchemaForSoftware().map((d) => d.key);
        expect(new Set(keys).size).toBe(keys.length);
    });

    it('every schema entry group has a human label in GROUP_LABELS', () => {
        for (const def of getSchemaForSoftware()) {
            expect(GROUP_LABELS[def.group]).toBeTruthy();
        }
    });
});
