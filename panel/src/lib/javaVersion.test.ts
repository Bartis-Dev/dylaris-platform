import { describe, it, expect } from 'vitest';
import { recommendJavaForVersion, effectiveMcVersion, JAVA_25, JAVA_21, JAVA_17, JAVA_8, JAVA_IMAGES } from './javaVersion';

describe('recommendJavaForVersion', () => {
    // The bug this exists for: the old parser read parts[1] unconditionally, so
    // "26.2" was judged as minor=2, fell through every branch and returned null.
    // The picker lists 26.x first, so the newest selectable version got no
    // recommendation and installed onto Java 21, where Paper refuses to start:
    // "Minecraft 26.1 and newer requires running the server with Java 25 or
    // above." Verified live - the container crash-looped on exit 1.
    it.each([
        ['26.1', JAVA_25],
        ['26.2', JAVA_25],
        ['27.0', JAVA_25],
        // 26.0 predates the Java 25 requirement, which starts at 26.1.
        ['26.0', JAVA_21],
    ])('new scheme %s -> %s', (v, want) => {
        expect(recommendJavaForVersion(v)).toBe(want);
    });

    it.each([
        ['1.21', JAVA_21],
        ['1.21.4', JAVA_21],
        ['1.20.5', JAVA_21],
        ['1.20.4', JAVA_17],
        ['1.18', JAVA_17],
        ['1.17', JAVA_8],
        ['1.16.5', JAVA_8],
        ['1.8', JAVA_8],
    ])('old scheme %s -> %s', (v, want) => {
        expect(recommendJavaForVersion(v)).toBe(want);
    });

    it.each(['', 'latest', 'snapshot-23w45a', '1.7', '2.0', 'abc.def'])('rejects %s', (v) => {
        expect(recommendJavaForVersion(v)).toBeNull();
    });
});

describe('JAVA_IMAGES', () => {
    it('offers a Java 25 image, without which no 26.x server can boot', () => {
        expect(JAVA_IMAGES.some((j) => j.id === JAVA_25)).toBe(true);
    });

    it('has no duplicate ids', () => {
        expect(new Set(JAVA_IMAGES.map((j) => j.id)).size).toBe(JAVA_IMAGES.length);
    });

    it('exposes every named constant', () => {
        for (const id of [JAVA_25, JAVA_21, JAVA_17, JAVA_8]) {
            expect(JAVA_IMAGES.some((j) => j.id === id)).toBe(true);
        }
    });
});

describe('effectiveMcVersion', () => {
    it('prefers the build when it refines the major', () => {
        expect(effectiveMcVersion('1.20', '1.20.4')).toBe('1.20.4');
        expect(effectiveMcVersion('26.2', '26.2')).toBe('26.2');
    });

    it('falls back to the major for loader versions', () => {
        expect(effectiveMcVersion('1.20', '47.2.0')).toBe('1.20');
        expect(effectiveMcVersion('1.20', '')).toBe('1.20');
    });
});
