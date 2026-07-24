import { describe, it, expect } from 'vitest';
import { isRconDialError, friendlyRconError } from './rcon';

// Raw error strings a node dial failure can bubble up as (see
// node/rcon.go execRcon + node/grpc_rcon.go handleRconExec, which wrap
// net.DialTimeout / net.OpError verbatim into RconExecResp.Error). These must
// never reach the user unmapped - they leak the internal mc_<uuid> hostname
// and read as an unactionable crash.
const RAW_DIAL_ERRORS = [
    'dial mc_abc-123:25575: dial tcp 10.0.5.3:25575: connect: connection refused',
    'dial mc_abc-123:25575: dial tcp: lookup mc_abc-123 on 127.0.0.11:53: no such host',
    'dial mc_abc-123:25575: read exec resp: i/o timeout',
];

describe('isRconDialError', () => {
    it('recognizes every raw dial-failure shape the node can produce', () => {
        for (const err of RAW_DIAL_ERRORS) {
            expect(isRconDialError(err), `expected dial error: ${err}`).toBe(true);
        }
    });

    it('does not misclassify legitimate RCON errors as dial failures', () => {
        expect(isRconDialError('rcon auth failed (bad password)')).toBe(false);
        expect(isRconDialError('rcon not enabled for this server')).toBe(false);
        expect(isRconDialError('command too long')).toBe(false);
    });

    it('is false for empty/undefined input', () => {
        expect(isRconDialError(undefined)).toBe(false);
        expect(isRconDialError('')).toBe(false);
    });
});

describe('friendlyRconError', () => {
    it('replaces a raw dial error with one clear, actionable message', () => {
        for (const err of RAW_DIAL_ERRORS) {
            expect(friendlyRconError(err)).toBe('RCON not reachable yet - restart the server.');
        }
    });

    it('passes a non-dial RCON error through unchanged', () => {
        expect(friendlyRconError('rcon auth failed (bad password)')).toBe('rcon auth failed (bad password)');
    });

    it('falls back to the provided default when there is no error message', () => {
        expect(friendlyRconError(undefined, 'RCON unavailable')).toBe('RCON unavailable');
        expect(friendlyRconError('', 'RCON unavailable')).toBe('RCON unavailable');
    });
});
