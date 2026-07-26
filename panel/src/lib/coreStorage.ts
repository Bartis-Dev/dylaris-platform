// Pure, framework-free logic for the Core file storage settings form so it can
// be unit-tested without React. The panel mirrors the backend validator: a path
// backend needs an absolute, operator-confirmed path; an s3 backend needs a
// bucket, access key and a secret (fresh input OR one already stored server-side).

export interface CoreStorageConfig {
  backend: 'path' | 's3';
  path: string;
  pathConfirmed: boolean;
  s3Endpoint: string;
  s3Bucket: string;
  s3Region: string;
  s3AccessKey: string;
  s3SecretKey?: string;
  s3PathStyle: boolean;
  s3Prefix: string;
  s3SecretSet?: boolean;
  // References a saved storage connection. When set (> 0), the credentials come
  // from that connection and the inline s3 fields are ignored. 0/undefined =
  // enter credentials inline.
  connectionId?: number;
}

// isAbsolutePath requires a literal leading "/", matching the server check in
// core/handlers/core_storage.go (validateCoreStorageConfig): Core only ships
// as a Linux Docker image, so the configured path is always evaluated on that
// container's filesystem. The server deliberately does NOT use filepath.IsAbs
// there, because that check is host-OS-dependent and would accept a Windows
// drive path (e.g. "C:\\mnt\\shared") on a Windows build/test host even though
// it is not a valid absolute path on the Linux container - this check must not
// accept it either, or the client gate would pass something the server 400s.
export function isAbsolutePath(p: string): boolean {
  return p.startsWith('/');
}

// canSaveCoreStorage mirrors the server's form-completeness validation
// (core/handlers/core_storage.go validateCoreStorageConfig): a path backend
// needs an absolute, operator-confirmed path; an s3 backend needs a bucket,
// access key and a secret. It does NOT decide whether the backend is
// actually shared across every Core - that used to be a host-path Core-count
// guess this function also enforced, but checkSharedStorageReachable now
// PROVES it server-side with a real cross-Core round, so a client-side guess
// would only ever be stale or wrong. Save always runs that round; a config
// that fails it is refused there, with the per-Core reason.
export function canSaveCoreStorage(
  c: CoreStorageConfig,
  saved: CoreStorageConfig | null = null,
): boolean {
  if (c.backend === 'path') {
    return isAbsolutePath(c.path.trim()) && c.pathConfirmed === true;
  }
  if (c.backend === 's3') {
    // A selected connection supplies the credentials; the inline s3 fields are
    // then irrelevant and may be blank. The backend validates the connection
    // itself (core/handlers/core_storage.go SaveConfig).
    if ((c.connectionId ?? 0) > 0) return true;
    // A stored secret only counts as "present" when the identity it was
    // stored against (endpoint/bucket/access key) hasn't changed - mirrors
    // the backend's anti credential-rebinding rule (see s3IdentityChanged
    // below), which the UI already warns about but must also enforce here.
    const freshSecret = (c.s3SecretKey ?? '').length > 0;
    const hasSecret = freshSecret || (c.s3SecretSet === true && !s3IdentityChanged(c, saved));
    return c.s3Bucket.trim().length > 0 && c.s3AccessKey.trim().length > 0 && hasSecret;
  }
  return false;
}

// s3IdentityChanged reports whether the endpoint/bucket/access key differ from
// the last-saved snapshot. Mirrors the backend's mergeCoreStorageCandidate
// identity check (core/handlers/core_storage.go): changing any of these while
// leaving the secret blank makes the backend refuse the save/test, because a
// blank secret is only ever backfilled when the identity fields are
// unchanged (anti credential-rebinding). The panel uses this to show a
// proactive hint instead of only surfacing the rejection after a failed save.
export function s3IdentityChanged(current: CoreStorageConfig, saved: CoreStorageConfig | null): boolean {
  if (!saved) return false;
  return (
    current.s3Endpoint !== saved.s3Endpoint ||
    current.s3Bucket !== saved.s3Bucket ||
    current.s3AccessKey !== saved.s3AccessKey
  );
}
