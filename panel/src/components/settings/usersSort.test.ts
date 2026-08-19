import { describe, it, expect } from 'vitest';
import { sortUsers } from './UsersTab';
import type { User } from '@/lib/api';

function u(username: string, extra: Partial<User> = {}): User {
    return { id: username, username, isAdmin: false, ...extra } as User;
}

describe('sortUsers', () => {
    // The question this list is usually opened to answer is "who can do what
    // here", and creation order buried it.
    it('puts admins first, then support, then members', () => {
        const list = [u('zoe'), u('ann', { role: 'support' }), u('max', { isAdmin: true })];
        expect(sortUsers(list, 'role').map(x => x.username)).toEqual(['max', 'ann', 'zoe']);
    });

    // isAdmin is the older field; role is the newer one. An account carrying
    // only isAdmin still has to rank as an admin.
    it('treats a bare isAdmin as the admin role', () => {
        const list = [u('member'), u('legacy', { isAdmin: true })];
        expect(sortUsers(list, 'role')[0].username).toBe('legacy');
    });

    it('sorts by name when asked', () => {
        const list = [u('zoe', { isAdmin: true }), u('ann')];
        expect(sortUsers(list, 'name').map(x => x.username)).toEqual(['ann', 'zoe']);
    });

    it('sorts newest first by creation date', () => {
        const list = [
            u('old', { createdAt: '2020-01-01T00:00:00Z' }),
            u('new', { createdAt: '2026-01-01T00:00:00Z' }),
        ];
        expect(sortUsers(list, 'created').map(x => x.username)).toEqual(['new', 'old']);
    });

    // Without a total order two accounts created in the same second (a seeded
    // install) swap places between renders.
    it('breaks every tie by username', () => {
        const same = '2026-01-01T00:00:00Z';
        const list = [u('zoe', { createdAt: same }), u('ann', { createdAt: same })];
        expect(sortUsers(list, 'created').map(x => x.username)).toEqual(['ann', 'zoe']);
        expect(sortUsers([u('zoe'), u('ann')], 'role').map(x => x.username)).toEqual(['ann', 'zoe']);
    });

    it('does not reorder the array it was given', () => {
        const list = [u('zoe'), u('ann')];
        sortUsers(list, 'name');
        expect(list.map(x => x.username)).toEqual(['zoe', 'ann']);
    });
});
