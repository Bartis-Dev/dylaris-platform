import { describe, it, expect } from 'vitest';
import {
  canSaveCoreStorage,
  s3IdentityChanged,
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
  it('path: blocked for a Windows-style drive path (server only accepts a leading "/")', () => {
    expect(canSaveCoreStorage({ ...base, backend: 'path', path: 'C:\\mnt\\shared', pathConfirmed: true })).toBe(false);
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

  // canSaveCoreStorage must consult s3IdentityChanged itself, not just let the
  // UI show a warning about it: a blank secret only counts as "present" when
  // the identity it was stored against hasn't changed, matching the backend's
  // anti credential-rebinding rule (core/handlers/core_storage.go).
  describe('s3 identity-change gate (second `saved` argument)', () => {
    const saved: CoreStorageConfig = {
      ...base, backend: 's3', s3Endpoint: 'https://s3.example.com', s3Bucket: 'old', s3AccessKey: 'k1', s3SecretSet: true,
    };

    it('blank secret + unchanged identity: can save', () => {
      expect(canSaveCoreStorage({ ...saved, s3SecretKey: '' }, saved)).toBe(true);
    });
    it('blank secret + changed bucket: cannot save', () => {
      expect(canSaveCoreStorage({ ...saved, s3Bucket: 'new', s3SecretKey: '' }, saved)).toBe(false);
    });
    it('blank secret + changed endpoint: cannot save', () => {
      expect(canSaveCoreStorage({ ...saved, s3Endpoint: 'https://other.example.com', s3SecretKey: '' }, saved)).toBe(false);
    });
    it('blank secret + changed access key: cannot save', () => {
      expect(canSaveCoreStorage({ ...saved, s3AccessKey: 'k2', s3SecretKey: '' }, saved)).toBe(false);
    });
    it('newly entered secret + changed identity: can save', () => {
      expect(canSaveCoreStorage({ ...saved, s3Bucket: 'new', s3SecretKey: 'freshsecret' }, saved)).toBe(true);
    });
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
