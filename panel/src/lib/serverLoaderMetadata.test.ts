import { describe, it, expect } from 'vitest';
import { isImportedServer, isKnownLoader, LOADER_OPTIONS } from './serverLoaderMetadata';

describe('isKnownLoader', () => {
    it('accepts every declared LOADER_OPTIONS entry', () => {
        for (const l of LOADER_OPTIONS) expect(isKnownLoader(l)).toBe(true);
    });

    it('is case-insensitive', () => {
        expect(isKnownLoader('Paper')).toBe(true);
        expect(isKnownLoader('NEOFORGE')).toBe(true);
    });

    it('rejects install-source-only types that are not Modrinth loader tags', () => {
        expect(isKnownLoader('vanilla')).toBe(false);
        expect(isKnownLoader('upload')).toBe(false);
        expect(isKnownLoader('upload-zip')).toBe(false);
        expect(isKnownLoader('library')).toBe(false);
        expect(isKnownLoader('modpack')).toBe(false);
    });

    it('rejects empty/unknown values', () => {
        expect(isKnownLoader('')).toBe(false);
        expect(isKnownLoader('bogus')).toBe(false);
    });
});

describe('isImportedServer', () => {
    it('flags a server with a blank MinecraftVersion even if installerType looks like a loader', () => {
        expect(isImportedServer({ installerType: 'paper', minecraftVersion: '' })).toBe(true);
        expect(isImportedServer({ installerType: 'paper' })).toBe(true);
    });

    it('flags a server with a non-loader installerType even if a version is present', () => {
        expect(isImportedServer({ installerType: 'upload', minecraftVersion: '1.20.4' })).toBe(true);
    });

    it('does not flag a server with both a known loader and a version', () => {
        expect(isImportedServer({ installerType: 'fabric', minecraftVersion: '1.21' })).toBe(false);
    });

    it('is case-insensitive on installerType and tolerates whitespace-only version', () => {
        expect(isImportedServer({ installerType: 'PAPER', minecraftVersion: '1.20.4' })).toBe(false);
        expect(isImportedServer({ installerType: 'paper', minecraftVersion: '   ' })).toBe(true);
    });
});
