import { describe, it, expect } from 'vitest';
import { isUsername, isEmail, isServerName, isPackSlug, isSemver, isMcVersion, sanitizeServerName, clampInt } from './validation';

describe('isUsername (mirrors validate.Username)', () => {
    it('accepts valid handles', () => {
        for (const s of ['alice', 'a_b.c-d', 'abc', 'A1b2']) expect(isUsername(s)).toBe(true);
    });
    it('rejects colon/space/leading-symbol/short/long', () => {
        for (const s of ['a:b', 'a b', '_abc', '.abc', 'ab', '', 'a'.repeat(33)]) expect(isUsername(s)).toBe(false);
    });
});

describe('isServerName', () => {
    it('accepts names with spaces/underscores', () => {
        for (const s of ['My Server', 'srv_1', 'a-b+c']) expect(isServerName(s)).toBe(true);
    });
    it('rejects leading space, slash, colon, empty', () => {
        for (const s of [' x', 'a/b', 'a:b', '']) expect(isServerName(s)).toBe(false);
    });
});

describe('isPackSlug / isEmail / isSemver', () => {
    it('slug', () => { expect(isPackSlug('my-pack_1')).toBe(true); expect(isPackSlug('My Pack')).toBe(false); expect(isPackSlug('a/b')).toBe(false); });
    it('email', () => { expect(isEmail('a@b.co')).toBe(true); expect(isEmail('no-at')).toBe(false); });
    it('semver', () => { expect(isSemver('1.2.3')).toBe(true); expect(isSemver('1.2')).toBe(false); expect(isSemver('1.x.0')).toBe(false); });
});

describe('isMcVersion (mirrors validate.McVersion)', () => {
    it('accepts a bare major.minor or major.minor.patch', () => {
        expect(isMcVersion('1.21')).toBe(true);
        expect(isMcVersion('1.21.4')).toBe(true);
    });
    it('rejects empty, non-numeric, and 4-segment versions', () => {
        for (const s of ['', 'latest', '1', '1.20.4.5']) expect(isMcVersion(s)).toBe(false);
    });
});

describe('sanitizeServerName', () => {
    it('strips invalid chars, keeps spaces, drops leading symbols, caps at 50', () => {
        expect(sanitizeServerName('My Server!')).toBe('My Server');
        expect(sanitizeServerName('  ---abc')).toBe('abc');
        expect(sanitizeServerName('a/b*c')).toBe('abc');
        expect(sanitizeServerName('a'.repeat(60)).length).toBe(50);
    });
});

describe('clampInt', () => {
    it('clamps to range and floors, min on NaN', () => {
        expect(clampInt(5.9, 0, 10)).toBe(5);
        expect(clampInt(-3, 0, 10)).toBe(0);
        expect(clampInt(99, 0, 10)).toBe(10);
        expect(clampInt('abc', 256)).toBe(256);
        expect(clampInt(1024, 256)).toBe(1024);
    });
});
