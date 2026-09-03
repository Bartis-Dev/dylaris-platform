import type { BackupRestore } from '@/lib/api/types';

/**
 * Whether the Backups view should keep refreshing on its timer.
 *
 * The subtlety, and the reason this is a named function with a test rather than
 * an inline `.some()`: a STALLED restore is queued and is not pending. Core
 * marks it when the node that would run it is gone, so nothing about that row
 * will change until the node comes back - and the row itself never moves on its
 * own, because only the node's result ever writes it.
 *
 * Counting it as pending polled three endpoints every five seconds for as long
 * as the tab stayed open. Not for a while: forever.
 */
export function restoresNeedPolling(restores: readonly BackupRestore[]): boolean {
    return restores.some(r => (r.status === 'queued' || r.status === 'running') && !r.stalled);
}
