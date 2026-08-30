import { describe, it, expect } from 'vitest';
import { ownStorageIncomplete, type OwnStorageInput } from '@/lib/api/ownBackupStorage';

const full = (over: Partial<OwnStorageInput['config']> = {}): OwnStorageInput => ({
    name: 'Backblaze',
    isDefault: false,
    config: {
        endpoint: 'https://s3.eu-central-003.backblazeb2.com',
        bucket: 'my-backups',
        accessKeyId: 'AK',
        secretAccessKey: 'sk',
        ...over,
    },
});

describe('connecting an account storage', () => {
    it('accepts a complete form', () => {
        expect(ownStorageIncomplete(full(), false)).toBeNull();
    });

    it('names the one missing field rather than failing generically', () => {
        expect(ownStorageIncomplete({ ...full(), name: '  ' }, false)).toMatch(/name/i);
        expect(ownStorageIncomplete(full({ endpoint: '' }), false)).toMatch(/endpoint/i);
        expect(ownStorageIncomplete(full({ bucket: '' }), false)).toMatch(/bucket/i);
        expect(ownStorageIncomplete(full({ accessKeyId: '' }), false)).toMatch(/access key ID/i);
    });

    it('requires the secret on create and not on edit', () => {
        // The form never receives the stored secret, so a blank field on an edit
        // means "keep it". Demanding it there would make every rename a
        // credential re-entry.
        expect(ownStorageIncomplete(full({ secretAccessKey: '' }), false)).toMatch(/secret/i);
        expect(ownStorageIncomplete(full({ secretAccessKey: '' }), true)).toBeNull();
    });
});
