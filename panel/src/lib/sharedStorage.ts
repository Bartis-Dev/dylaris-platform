// Wording for the one node topology DYLARIS cannot support: a storage path
// mounted into more than one node.
//
// It is not merely fragile there. `.node_secret`, `.node_id` and the tenant
// network allocator all live in the first storage path, so two nodes overwrite
// each other's identity and hand the same subnet to two owners; and a migration
// between them deletes the server while reporting success. The node detects it
// and reports it here so an operator can fix the mounts.
//
// Kept out of the view so the wording is unit-testable - the panel has vitest
// only, no DOM test tooling.

export interface SharedStorageConflict {
    path: string;
    // 'peer' = another node's beacon sits on this disk, identities still
    // separate. 'identity' = another process is writing this node's OWN beacon,
    // so the node ids have already collided.
    kind: 'peer' | 'identity';
    peerNode?: string;
    peerHost?: string;
}

// peerLabel names the other node as helpfully as the beacon allows. The
// hostname is the one an operator can act on; the node id is the fallback.
export function peerLabel(c: SharedStorageConflict): string {
    return c.peerHost?.trim() || c.peerNode?.trim() || 'another node';
}

export function sharedStorageMessage(c: SharedStorageConflict): string {
    if (c.kind === 'identity') {
        // Strictly worse than a peer conflict and must not read the same: here
        // the node identity is already destroyed, which is why an operator sees
        // node ids and secrets changing under them.
        return `${c.path} is shared with ${peerLabel(c)}, and node identity has already collided there. Both nodes are overwriting the same node secret and id.`;
    }
    return `${c.path} is also mounted into ${peerLabel(c)}. A storage path must belong to exactly one node.`;
}

// summary is the headline above the list. The count matters: fixing one mount
// while a second is still shared leaves the failure in place.
export function sharedStorageSummary(conflicts: SharedStorageConflict[]): string {
    const paths = new Set(conflicts.map(c => c.path)).size;
    const worst = conflicts.some(c => c.kind === 'identity') ? 'identity' : 'peer';
    const noun = paths === 1 ? 'storage path is' : 'storage paths are';
    if (worst === 'identity') {
        return `${paths} ${noun} shared with another node, and node identity has collided`;
    }
    return `${paths} ${noun} shared with another node`;
}
