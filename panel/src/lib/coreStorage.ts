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

// hostPathAllowed mirrors the server's single-Core constraint on the
// filesystem backend (core/handlers/core_storage.go guardHostPathBackend): a
// host path stores files on one machine's disk, so a second Core would leave
// each instance serving only what it wrote itself. Defaults to true so a
// caller that has not fetched the server's answer yet - or a server that could
// not take the count - behaves exactly as before; the server refuses the save
// either way, this only decides whether the button is offered.
export function canSaveCoreStorage(
  c: CoreStorageConfig,
  saved: CoreStorageConfig | null = null,
  hostPathAllowed: boolean = true,
): boolean {
  if (c.backend === 'path') {
    return hostPathAllowed && isAbsolutePath(c.path.trim()) && c.pathConfirmed === true;
  }
  if (c.backend === 's3') {
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
