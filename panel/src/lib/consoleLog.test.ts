import { describe, it, expect } from 'vitest';
import { logLineClass, detectLevel, levelClass, computeLineLevels } from './consoleLog';

describe('detectLevel', () => {
    it('returns null for continuation-shaped lines (no level token)', () => {
        expect(detectLevel('\tat com.foo.Bar(Bar.java:1)')).toBeNull();
        expect(detectLevel('java.nio.file.NoSuchFileException: /x')).toBeNull();
    });
    it('returns the matching level for real log lines', () => {
        expect(detectLevel('[12:00:00] [Server thread/ERROR]: boom')).toBe('error');
        expect(detectLevel('[12:00:00] [Server thread/WARN]: heads up')).toBe('warn');
        expect(detectLevel('[Server/DEBUG]: detail')).toBe('debug');
    });
    it('returns null for INFO (no dedicated regex token, falls back to default elsewhere)', () => {
        expect(detectLevel('[12:00:00] [Server thread/INFO]: Done')).toBeNull();
    });
});

describe('levelClass', () => {
    it('maps each level to its design-token class', () => {
        expect(levelClass('error')).toBe('text-(--error-light)');
        expect(levelClass('warn')).toBe('text-(--warning-light)');
        expect(levelClass('debug')).toBe('text-(--base-06)');
        expect(levelClass('info')).toBe('text-(--base-09)');
    });
});

describe('computeLineLevels', () => {
    it('inherits the previous line level for stack-trace continuation lines', () => {
        const lines = [
            '[12:00:00] [Server/ERROR]: boom',
            'java.nio.file.NoSuchFileException: /x',
            '\tat com.foo.Bar',
            'Caused by: java.lang.NullPointerException',
            '\t... 3 more',
            '[12:00:01] [Server/INFO]: ok',
        ];
        expect(computeLineLevels(lines)).toEqual(['error', 'error', 'error', 'error', 'error', 'info']);
    });
    it('does not bleed a level into a fresh non-timestamped record (proxy consoles)', () => {
        // A plain line that is not an indented / stack-frame / exception /
        // Caused-by shape is its own default-level record, even without a
        // [HH:MM:SS] prefix - guards against proxy console formats.
        const lines = [
            '[12:00:00] [Server/ERROR]: boom',
            'Player connected: Steve',
        ];
        expect(computeLineLevels(lines)).toEqual(['error', 'info']);
    });
});

describe('logLineClass', () => {
    it('colours ERROR/SEVERE/FATAL as error', () => {
        expect(logLineClass('[12:00:00] [Server thread/ERROR]: boom')).toBe('text-(--error-light)');
        expect(logLineClass('[Server/SEVERE]: bad')).toBe('text-(--error-light)');
        expect(logLineClass('FATAL: crash')).toBe('text-(--error-light)');
    });
    it('colours WARN/WARNING as warning', () => {
        expect(logLineClass('[12:00:00] [Server thread/WARN]: heads up')).toBe('text-(--warning-light)');
        expect(logLineClass('WARNING something')).toBe('text-(--warning-light)');
    });
    it('colours DEBUG/TRACE as muted', () => {
        expect(logLineClass('[Server/DEBUG]: detail')).toBe('text-(--base-06)');
        expect(logLineClass('TRACE step')).toBe('text-(--base-06)');
    });
    it('leaves INFO and unmatched lines at the default colour', () => {
        expect(logLineClass('[12:00:00] [Server thread/INFO]: Done')).toBe('text-(--base-09)');
        expect(logLineClass('just some output')).toBe('text-(--base-09)');
        expect(logLineClass('')).toBe('text-(--base-09)');
    });
    it('is case-insensitive on the level token', () => {
        expect(logLineClass('[server thread/error]: x')).toBe('text-(--error-light)');
    });
});
