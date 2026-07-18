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

export function canSaveCoreStorage(c: CoreStorageConfig, saved: CoreStorageConfig | null = null): boolean {
  if (c.backend === 'path') {
    return isAbsolutePath(c.path.trim()) && c.pathConfirmed === true;
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

// --- Migrate result interpretation (carry-forward B) -----------------------
//
// The real /api/settings/core-storage/migrate response is:
//   { success: bool, results: { "<subsystem>": { copied, skipped, failed,
//   errors: [...] } }, note: "..." }
// NOT { migrated: { sub: number } }. Critically, a PARTIAL failure returns
// HTTP 200 with success:false. A caller that branches on the HTTP status
// (res.ok) instead of the response BODY's success field would render a
// failed migration as a success. summarizeMigrateResult is the single place
// that interprets the body, so the UI (and its tests) never re-derive this
// logic ad hoc.

export interface MigrateSubsystemResult {
  copied: number;
  skipped: number;
  failed: number;
  errors?: string[];
}

export interface MigrateCoreStorageResponse {
  success: boolean;
  results?: Record<string, MigrateSubsystemResult>;
  note?: string;
  message?: string;
}

export interface MigrateSubsystemSummary {
  name: string;
  copied: number;
  skipped: number;
  failed: number;
  errors: string[];
}

export interface MigrateSummary {
  // true only when the response body says success AND no subsystem reported
  // a failure. Never derived from HTTP status - the caller must pass in the
  // already-parsed body (see coreStorage.test.ts for the HTTP-200-with-
  // success:false regression case).
  ok: boolean;
  totalCopied: number;
  totalSkipped: number;
  totalFailed: number;
  perSubsystem: MigrateSubsystemSummary[];
  note?: string;
}

export function summarizeMigrateResult(res: MigrateCoreStorageResponse): MigrateSummary {
  const results = res.results ?? {};
  const perSubsystem = Object.entries(results).map(([name, r]) => ({
    name,
    copied: r.copied ?? 0,
    skipped: r.skipped ?? 0,
    failed: r.failed ?? 0,
    errors: r.errors ?? [],
  }));
  const totalCopied = perSubsystem.reduce((sum, s) => sum + s.copied, 0);
  const totalSkipped = perSubsystem.reduce((sum, s) => sum + s.skipped, 0);
  const totalFailed = perSubsystem.reduce((sum, s) => sum + s.failed, 0);
  return {
    ok: res.success === true && totalFailed === 0,
    totalCopied,
    totalSkipped,
    totalFailed,
    perSubsystem,
    note: res.note,
  };
}
