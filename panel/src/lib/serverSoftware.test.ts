import { describe, it, expect } from 'vitest';
import { isPluginLoader } from './serverSoftware';

describe('isPluginLoader', () => {
    const plugins = ['paper', 'spigot', 'bukkit', 'purpur', 'velocity', 'waterfall', 'bungeecord'];
    for (const p of plugins) {
        it(`classifies "${p}" as a plugin loader`, () => {
            expect(isPluginLoader(p)).toBe(true);
        });
    }

    it('is case-insensitive', () => {
        expect(isPluginLoader('PAPER')).toBe(true);
        expect(isPluginLoader('Purpur')).toBe(true);
    });

    const nonPlugins = ['fabric', 'forge', 'neoforge', 'quilt', 'modpack', 'pack', 'upload', 'upload-zip', 'library', 'unknown-thing'];
    for (const n of nonPlugins) {
        it(`classifies "${n}" as non-plugin (warn-first)`, () => {
            expect(isPluginLoader(n)).toBe(false);
        });
    }

    it('treats empty / undefined / null as non-plugin', () => {
        expect(isPluginLoader('')).toBe(false);
        expect(isPluginLoader(undefined)).toBe(false);
        expect(isPluginLoader(null)).toBe(false);
    });
});
