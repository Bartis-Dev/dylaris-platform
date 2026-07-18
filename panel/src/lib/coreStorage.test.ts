import { describe, it, expect } from 'vitest';
import {
  canSaveCoreStorage,
  s3IdentityChanged,
  summarizeMigrateResult,
  type CoreStorageConfig,
} from './coreStorage';

const base: CoreStorageConfig = {
  backend: 'path', path: '', pathConfirmed: false,
  s3Endpoint: '', s3Bucket: '', s3Region: '', s3AccessKey: '', s3SecretKey: '',
  s3PathStyle: false, s3Prefix: '', s3SecretSet: false,
};

describe('canSaveCoreStorage', () => {
  it('path: blocked until confirmed', () => {
    expect(canSaveCoreStorage({ ...base, backend: 'path', path: '/mnt/shared', pathConfirmed: false })).toBe(false);
  });
  it('path: allowed when absolute path + confirmed', () => {
    expect(canSaveCoreStorage({ ...base, backend: 'path', path: '/mnt/shared', pathConfirmed: true })).toBe(true);
  });
  it('path: blocked when path empty even if confirmed', () => {
    expect(canSaveCoreStorage({ ...base, backend: 'path', path: '', pathConfirmed: true })).toBe(false);
  });
  it('path: blocked when path is relative', () => {
    expect(canSaveCoreStorage({ ...base, backend: 'path', path: 'relative/dir', pathConfirmed: true })).toBe(false);
  });
  it('s3: allowed with bucket + access key + fresh secret', () => {
    expect(canSaveCoreStorage({ ...base, backend: 's3', s3Bucket: 'b', s3AccessKey: 'k', s3SecretKey: 's' })).toBe(true);
  });
  it('s3: allowed with bucket + access key when a secret is already stored', () => {
    expect(canSaveCoreStorage({ ...base, backend: 's3', s3Bucket: 'b', s3AccessKey: 'k', s3SecretKey: '', s3SecretSet: true })).toBe(true);
  });
  it('s3: blocked without bucket', () => {
    expect(canSaveCoreStorage({ ...base, backend: 's3', s3Bucket: '', s3AccessKey: 'k', s3SecretKey: 's' })).toBe(false);
  });
  it('s3: blocked without any secret (fresh, none stored)', () => {
    expect(canSaveCoreStorage({ ...base, backend: 's3', s3Bucket: 'b', s3AccessKey: 'k', s3SecretKey: '', s3SecretSet: false })).toBe(false);
  });
});

describe('s3IdentityChanged', () => {
  const saved: CoreStorageConfig = { ...base, backend: 's3', s3Endpoint: 'https://s3.example.com', s3Bucket: 'old', s3AccessKey: 'k1', s3SecretSet: true };

  it('false when nothing saved yet', () => {
    expect(s3IdentityChanged({ ...base, backend: 's3' }, null)).toBe(false);
  });
  it('false when identity fields are unchanged', () => {
    expect(s3IdentityChanged({ ...saved }, saved)).toBe(false);
  });
  it('true when the bucket changes', () => {
    expect(s3IdentityChanged({ ...saved, s3Bucket: 'new' }, saved)).toBe(true);
  });
  it('true when the access key changes', () => {
    expect(s3IdentityChanged({ ...saved, s3AccessKey: 'k2' }, saved)).toBe(true);
  });
  it('true when the endpoint changes', () => {
    expect(s3IdentityChanged({ ...saved, s3Endpoint: 'https://other.example.com' }, saved)).toBe(true);
  });
});

// Carry-forward B: the real migrate response is
// { success, results: { sub: { copied, skipped, failed, errors } }, note },
// NOT { migrated: { sub: number } }. A partial failure comes back as HTTP 200
// with success:false, so the interpretation must key off the body's success
// field (and per-subsystem failed counts), never off the transport status.
describe('summarizeMigrateResult', () => {
  it('reports ok when success is true and nothing failed', () => {
    const summary = summarizeMigrateResult({
      success: true,
      results: {
        library: { copied: 3, skipped: 1, failed: 0 },
        'ticket-attachments': { copied: 2, skipped: 0, failed: 0 },
      },
      note: 'Original files were left in place.',
    });
    expect(summary.ok).toBe(true);
    expect(summary.totalCopied).toBe(5);
    expect(summary.totalSkipped).toBe(1);
    expect(summary.totalFailed).toBe(0);
    expect(summary.perSubsystem).toHaveLength(2);
  });

  // This is the exact shape the backend sends on an HTTP 200 whose body says
  // success:false (core/handlers/core_storage.go Migrate: "success" is only
  // true when every subsystem completed with zero failures). A consumer that
  // checked `response.ok` instead of this parsed body would call it a success.
  it('reports NOT ok on a partial failure even though the transport-level response was 200', () => {
    const summary = summarizeMigrateResult({
      success: false,
      results: {
        library: { copied: 3, skipped: 1, failed: 0 },
        'ticket-attachments': { copied: 0, skipped: 0, failed: 2, errors: ['file-a: disk full', 'file-b: disk full'] },
      },
      note: 'Original files were left in place.',
    });
    expect(summary.ok).toBe(false);
    expect(summary.totalFailed).toBe(2);
    expect(summary.perSubsystem.find(s => s.name === 'ticket-attachments')?.errors).toEqual([
      'file-a: disk full', 'file-b: disk full',
    ]);
  });

  it('is never fooled by success:true with a nonzero failed count', () => {
    // Defensive: the two signals (body.success and per-subsystem failed) are
    // both honored rather than trusting either alone.
    const summary = summarizeMigrateResult({
      success: true,
      results: { library: { copied: 1, skipped: 0, failed: 1, errors: ['boom'] } },
    });
    expect(summary.ok).toBe(false);
    expect(summary.totalFailed).toBe(1);
  });

  it('handles a response with no results (e.g. the 400 "not configured" error body)', () => {
    const summary = summarizeMigrateResult({ success: false, message: 'Configure Core file storage before migrating.' });
    expect(summary.ok).toBe(false);
    expect(summary.perSubsystem).toEqual([]);
    expect(summary.totalCopied).toBe(0);
  });
});
