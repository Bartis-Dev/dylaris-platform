// Pure, framework-free logic for the storage connection banner so it can be
// unit-tested without React.
//
// Core reports each storage backend separately over the
// `storage.connection.changed` SSE event and over GET /api/storage/connection.
// The two degraded words are NOT synonyms and must not be collapsed:
//   - `path` degrades to `unavailable`: requests fail immediately.
//   - `s3` degrades to `reconnecting`: requests block waiting for recovery.
// The user-facing copy below is written around that difference, because it is
// the only thing that tells an owner whether a running upload is dead or just
// stalled.

export type StorageBackend = 'path' | 's3';
export type BackendConnectionState = 'ok' | 'unavailable' | 'reconnecting';

export interface BackendConnection {
  state: BackendConnectionState;
  /** RFC3339 instant the current state began. Null while `ok`. */
  since: string | null;
}

export interface StorageConnectionState {
  path: BackendConnection;
  s3: BackendConnection;
}

/** Wire shape of GET /api/storage/connection (both keys always present). */
export interface StorageConnectionResponse {
  path?: unknown;
  s3?: unknown;
}

/** Wire shape of one `storage.connection.changed` SSE payload. */
export interface StorageConnectionEventPayload {
  backend?: unknown;
  state?: unknown;
  since?: unknown;
}

export type BannerSeverity = 'error' | 'warning';

export interface StorageBannerView {
  severity: BannerSeverity;
  title: string;
  message: string;
  /** Earliest `since` across the degraded backends, or null when unknown. */
  since: string | null;
}

const OK: BackendConnection = { state: 'ok', since: null };

export const INITIAL_STORAGE_CONNECTION: StorageConnectionState = { path: OK, s3: OK };

const KNOWN_STATES: readonly BackendConnectionState[] = ['ok', 'unavailable', 'reconnecting'];

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null;
}

// An unrecognised state word degrades to `ok` rather than to a degraded state:
// the banner is an interrupt, and inventing an outage from a garbled frame is
// worse than missing one. Note this deliberately does NOT reject a state that
// the other backend owns (a `path` that reports `reconnecting`); dropping a
// real degradation signal over a vocabulary mismatch is the worse failure.
function normaliseState(v: unknown): BackendConnectionState {
  return typeof v === 'string' && (KNOWN_STATES as readonly string[]).includes(v)
    ? (v as BackendConnectionState)
    : 'ok';
}

function normaliseSince(v: unknown, state: BackendConnectionState): string | null {
  if (state === 'ok') return null;
  return typeof v === 'string' && v.length > 0 ? v : null;
}

/** Turn one untrusted `{state, since}` value into a known BackendConnection. */
export function normaliseBackendConnection(v: unknown): BackendConnection {
  if (!isRecord(v)) return OK;
  const state = normaliseState(v.state);
  return { state, since: normaliseSince(v.since, state) };
}

/** Turn an untrusted GET /api/storage/connection body into a full state. */
export function normaliseStorageConnection(v: unknown): StorageConnectionState {
  if (!isRecord(v)) return INITIAL_STORAGE_CONNECTION;
  return {
    path: normaliseBackendConnection(v.path),
    s3: normaliseBackendConnection(v.s3),
  };
}

/**
 * Apply one `storage.connection.changed` payload to the current state.
 * Core sends one event per backend that changed, never a combined document,
 * so exactly one half is ever touched. An unusable payload returns `current`
 * by reference so React can skip the re-render.
 */
export function applyStorageConnectionEvent(
  current: StorageConnectionState,
  payload: unknown,
): StorageConnectionState {
  if (!isRecord(payload)) return current;
  const backend = payload.backend;
  if (backend !== 'path' && backend !== 's3') return current;
  const next = normaliseBackendConnection(payload);
  if (backend === 'path') return { path: next, s3: current.s3 };
  return { path: current.path, s3: next };
}

// Labels match the backend names in the Core storage settings tab
// (CoreStorageTab BACKENDS) so an admin reading this banner knows which of the
// two configured backends to go look at.
const PATH_LABEL = 'Filesystem Path';
const S3_LABEL = 'S3-compatible';

// Compared as instants, not as strings. Lexicographic order only agrees with
// chronological order while both stamps use the same zone spelling, so a single
// offset-formatted stamp (`+02:00` beside a `Z`) would silently pick the later
// one. Unparseable input falls back to the other value rather than to NaN.
function earliestSince(a: string | null, b: string | null): string | null {
  if (!a) return b;
  if (!b) return a;
  const ta = Date.parse(a);
  const tb = Date.parse(b);
  if (Number.isNaN(ta)) return b;
  if (Number.isNaN(tb)) return a;
  return ta <= tb ? a : b;
}

/**
 * Decide what the banner shows. Returns null when there is nothing to say, so
 * the component costs nothing in steady state. Two degraded backends produce
 * ONE message rather than two stacked banners.
 */
export function selectStorageBanner(s: StorageConnectionState): StorageBannerView | null {
  const pathDown = s.path.state !== 'ok';
  const s3Down = s.s3.state !== 'ok';
  if (!pathDown && !s3Down) return null;

  if (pathDown && s3Down) {
    return {
      severity: 'error',
      title: 'File storage is not working',
      message:
        `Both storage backends lost their connection. ${PATH_LABEL} storage is unreachable, so uploads and downloads to it fail immediately. ` +
        `${S3_LABEL} storage is being retried, so uploads to it wait instead of failing. Do not start a new upload until this banner clears.`,
      since: earliestSince(s.path.since, s.s3.since),
    };
  }

  if (pathDown) {
    return {
      severity: 'error',
      title: `${PATH_LABEL} storage is unreachable`,
      message:
        'Uploads and downloads fail immediately while this is showing. Do not start a new upload until this banner clears, ' +
        'and check the storage settings if it stays.',
      since: s.path.since,
    };
  }

  return {
    severity: 'warning',
    title: `${S3_LABEL} storage lost its connection`,
    message:
      'The connection is being retried. Uploads wait instead of failing, so one you start now may sit without progress, ' +
      'and it can still fail if the connection does not come back.',
    since: s.s3.since,
  };
}
