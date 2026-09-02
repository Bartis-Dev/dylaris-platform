import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// FeaturesTab holds THREE independent save-on-demand cards, and each one is
// dirty-checked against its own snapshot ref:
//
//   dirty = snapshot.current !== null && JSON.stringify(now) !== JSON.stringify(snapshot.current)
//
// A snapshot the loader never fills stays null forever, so `dirty` is
// permanently false, the save bar never arms, and the card's save() returns
// early on its own `if (!prev) return false`. Nothing errors. The screen looks
// completely normal and the switches simply do not persist - which is exactly
// how it shipped: `platformSnapshot` was declared and read in three places and
// assigned in none of them that the loader reaches, so NO platform feature flag
// (tickets, modpacks, BYON, auto-move, long-term statistics, user API keys)
// could be saved at all.
//
// This tab predates `useSettingsForm`, which owns that lifecycle properly, and
// hand-rolls it once per card. Until it is migrated, the invariant is checked
// here: every snapshot ref this file declares must be seeded by the load
// effect. A fourth card added later is covered without touching this test.
//
// Source-level on purpose - the suite is logic-only with no jsdom, and the bug
// is a missing assignment rather than a wrong render.

const src = readFileSync(join(__dirname, 'FeaturesTab.tsx'), 'utf8');

// The mount-time loader: `useEffect(() => { ... }, []);`
function loadEffect(): string {
    const start = src.indexOf('useEffect(() => {');
    expect(start, 'FeaturesTab has no mount effect any more').toBeGreaterThan(-1);
    const end = src.indexOf('}, []);', start);
    expect(end, 'the mount effect is no longer dependency-free').toBeGreaterThan(start);
    return src.slice(start, end);
}

function snapshotRefs(): string[] {
    // `const platformSnapshot = useRef<FeatureFlagsAdminPayload | null>(null);`
    const names = [...src.matchAll(/const\s+(\w+)\s*=\s*useRef<[^>]*\|\s*null>\(null\)/g)]
        .map(m => m[1]);
    expect(names.length, 'no snapshot refs found - has this tab been migrated?').toBeGreaterThan(0);
    return names;
}

describe('every FeaturesTab card can actually be saved', () => {
    it('the loader seeds every snapshot ref it declares', () => {
        const effect = loadEffect();
        const unseeded = snapshotRefs().filter(name => !effect.includes(`${name}.current =`));
        expect(
            unseeded,
            `these snapshots are never filled on load, so their card is permanently ` +
            `not-dirty and silently unsavable: ${unseeded.join(', ')}`,
        ).toEqual([]);
    });

    it('each snapshot is read by a dirty check, so an unseeded one is not merely unused', () => {
        // Without this, deleting the dirty check would "fix" the test above.
        for (const name of snapshotRefs()) {
            expect(src, `${name} is declared but never dirty-checked`)
                .toMatch(new RegExp(`${name}\\.current\\s*!==\\s*null`));
        }
    });

    it('a failed load says so instead of leaving the card mutely inert', () => {
        // The inert card is correct (unconfirmed values must not be writable),
        // but on its own it reads as a broken page. The other two loaders in
        // this file already say it; the platform one did not.
        const effect = loadEffect();
        const platformLoad = effect.slice(effect.indexOf('getSystemFeaturesAdmin()'));
        expect(platformLoad).toMatch(/showToast\(/);
    });
});
