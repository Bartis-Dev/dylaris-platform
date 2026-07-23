import { describe, it, expect } from 'vitest';
import { logLineClass } from './consoleLog';

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
