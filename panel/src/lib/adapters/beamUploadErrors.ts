// Pure helpers for classifying Beam upload errors. Kept import-free so they can
// be unit-tested without loading the whole Wails transport adapter.

// gRPC status codes that mean the Node deliberately refused this upload — a
// limit (ResourceExhausted), a rejected path or auth (PermissionDenied /
// Unauthenticated), or a malformed request (InvalidArgument). These are
// permanent for this upload, so the chunk retry loop must fail fast with the
// real reason instead of treating them as a transient network blip.
const FATAL_BEAM_UPLOAD_CODES = ['ResourceExhausted', 'PermissionDenied', 'Unauthenticated', 'InvalidArgument'];

export function isFatalBeamUploadError(message: string): boolean {
    return FATAL_BEAM_UPLOAD_CODES.some(code => message.includes('code = ' + code));
}

// gRPC errors surface as "rpc error: code = X desc = <reason>". Show the caller
// just the reason; the code prefix is noise.
export function cleanBeamGrpcMessage(message: string): string {
    const marker = 'desc = ';
    const i = message.indexOf(marker);
    return i >= 0 ? message.slice(i + marker.length).trim() : message;
}
