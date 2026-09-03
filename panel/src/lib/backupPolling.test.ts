import { describe, expect, it } from 'vitest';
import { restoresNeedPolling } from '@/lib/backupPolling';
import type { BackupRestore } from '@/lib/api/types';

const restore = (over: Partial<BackupRestore>): BackupRestore => ({
    id: 1,
    runId: 1,
    serverId: 1,
    requestedAt: '2026-09-03T00:00:00Z',
    status: 'queued',
    errorMessage: '',
    ...over,
});

describe('restoresNeedPolling', () => {
    it('polls while a restore is genuinely in progress', () => {
        expect(restoresNeedPolling([restore({ status: 'queued' })])).toBe(true);
        expect(restoresNeedPolling([restore({ status: 'running' })])).toBe(true);
    });

    // The one that mattered. The row is queued and stays queued, because only
    // the node's result ever writes it and the node is gone - so polling it was
    // a timer with no end.
    it('does not poll a queued restore Core has marked stalled', () => {
        expect(restoresNeedPolling([restore({ status: 'queued', stalled: true })])).toBe(false);
    });

    it('still polls when a live restore sits beside a stalled one', () => {
        expect(
            restoresNeedPolling([
                restore({ id: 1, status: 'queued', stalled: true }),
                restore({ id: 2, status: 'running' }),
            ]),
        ).toBe(true);
    });

    it('does not poll finished history', () => {
        expect(
            restoresNeedPolling([
                restore({ id: 1, status: 'success' }),
                restore({ id: 2, status: 'failed' }),
            ]),
        ).toBe(false);
    });

    it('does not poll an empty history', () => {
        expect(restoresNeedPolling([])).toBe(false);
    });
});
