import { describe, it, expect } from 'vitest';
import { parseZones } from './gatewayDns';

// The rule this decides: what a comma-separated field becomes.
//
// The empty-entry case is the one that matters. A trailing comma producing a
// zone named '' would be stored and then matched against every name and none,
// and the screen would show a zone list that looks right.
describe('parseZones', () => {
    it.each([
        ['example.com', ['example.com'], 'the plain case'],
        ['a.com, b.com', ['a.com', 'b.com'], 'spaces after the comma are normal typing'],
        ['a.com,,b.com', ['a.com', 'b.com'], 'a double comma is a typo, not an empty zone'],
        ['a.com,', ['a.com'], 'a trailing comma must not become a zone named ""'],
        ['  Example.COM  ', ['example.com'], 'DNS is case-insensitive, so the stored value is too'],
        ['', [], 'an empty field is no zones, not one empty zone'],
        ['   ,  , ', [], 'whitespace and commas alone are still no zones'],
    ])('parseZones(%j) -> %j (%s)', (input, expected) => {
        expect(parseZones(input as string)).toEqual(expected);
    });
});
