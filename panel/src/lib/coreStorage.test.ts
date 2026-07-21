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

describe('canSaveCoreStorage host-path single-Core constraint', () => {
  const path: CoreStorageConfig = { ...base, backend: 'path', path: '/mnt/shared', pathConfirmed: true };
  const s3: CoreStorageConfig = { ...base, backend: 's3', s3Bucket: 'b', s3AccessKey: 'k', s3SecretKey: 'sec' };

  it('an otherwise valid host path cannot be saved while a second Core is online', () => {
    expect(canSaveCoreStorage(path, null, false)).toBe(false);
  });
  it('the same config is saveable on a single Core', () => {
    expect(canSaveCoreStorage(path, null, true)).toBe(true);
  });
  it('defaults to allowed, so a caller that has not fetched the answer behaves as before', () => {
    expect(canSaveCoreStorage(path, null)).toBe(true);
  });
  it('does not block s3, which is the backend multi-Core deployments are meant to use', () => {
    expect(canSaveCoreStorage(s3, null, false)).toBe(true);
  });
  it('does not rescue a host path that is invalid for other reasons', () => {
    expect(canSaveCoreStorage({ ...path, pathConfirmed: false }, null, true)).toBe(false);
    expect(canSaveCoreStorage({ ...path, path: 'relative' }, null, true)).toBe(false);
  });
});

describe('canSaveCoreStorage with a selected storage connection', () => {
  it('a selected connection makes an s3 config saveable even with blank inline fields', () => {
    const c: CoreStorageConfig = { ...base, backend: 's3', s3Bucket: '', s3AccessKey: '', s3SecretKey: '', connectionId: 5 };
    expect(canSaveCoreStorage(c, null, true)).toBe(true);
  });
  it('connectionId 0 falls back to the inline requirements, so blank inline is not saveable', () => {
    const c: CoreStorageConfig = { ...base, backend: 's3', s3Bucket: '', s3AccessKey: '', s3SecretKey: '', connectionId: 0 };
    expect(canSaveCoreStorage(c, null, true)).toBe(false);
  });
});
