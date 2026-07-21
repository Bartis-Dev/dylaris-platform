import { describe, it, expect } from 'vitest';
import { isFatalBeamUploadError, cleanBeamGrpcMessage } from './beamUploadErrors';

describe('isFatalBeamUploadError', () => {
    it('flags deliberate node refusals as fatal (no retry)', () => {
        const fatal = [
            'rpc error: code = ResourceExhausted desc = daily upload quota reached: 5 of 10 bytes used',
            'rpc error: code = PermissionDenied desc = path not allowed',
            'rpc error: code = InvalidArgument desc = bad request',
            'rpc error: code = Unauthenticated desc = ticket expired',
        ];
        for (const m of fatal) {
            expect(isFatalBeamUploadError(m)).toBe(true);
        }
    });

    it('treats transient / transport errors as retryable', () => {
        const retryable = [
            'rpc error: code = Unavailable desc = connection reset',
            'rpc error: code = DeadlineExceeded desc = context deadline exceeded',
            'EOF',
            'Connection unstable',
            '',
        ];
        for (const m of retryable) {
            expect(isFatalBeamUploadError(m)).toBe(false);
        }
    });

    it('does not match a code name that is not the gRPC "code = X" token', () => {
        // A desc that merely mentions the word must not be misread as the code.
        expect(isFatalBeamUploadError('rpc error: code = Unavailable desc = ResourceExhausted upstream')).toBe(false);
    });
});

describe('cleanBeamGrpcMessage', () => {
    it('extracts just the desc from a gRPC status string', () => {
        expect(cleanBeamGrpcMessage('rpc error: code = ResourceExhausted desc = daily upload quota reached'))
            .toBe('daily upload quota reached');
    });

    it('returns the message unchanged when there is no desc marker', () => {
        expect(cleanBeamGrpcMessage('some plain error')).toBe('some plain error');
    });
});
