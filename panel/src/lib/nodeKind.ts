/**
 * The three kinds of machine, mirrored from Core.
 *
 * Core decides this in `core/handlers/nodes.go` and enforces it on
 * `GET /api/nodes?scope=external|byon`:
 *
 *   isExternalPlatformNode  n.IsExternal() && n.OwnerID == nil
 *   isBYONNode              n.OwnerID != nil
 *
 * and `models.Node.IsExternal()` is "the tag list contains `external`".
 *
 * Kept in ONE place here because the failure mode is the panel and Core
 * disagreeing about what "external" means: both sides keep working, both look
 * right, and a customer's machine shows up under the operator's own heading.
 * If these ever move, move them here - not into a component.
 */

export type NodeKind = 'platform' | 'external' | 'byon';

/** The fields the kind is decided from. Anything with these can be classified. */
export interface NodeKindFields {
    /** Set only for a BYON machine: the tenant who owns it. */
    ownerId?: string | null;
    /** Comma-separated tag list; `external` marks an operator machine outside the swarm. */
    tags?: string | null;
}

/** Whether the node carries the `external` tag. Mirrors models.Node.IsExternal. */
export function hasExternalTag(n: NodeKindFields): boolean {
    return (n.tags ?? '').split(',').some(t => t.trim() === 'external');
}

export function nodeKind(n: NodeKindFields): NodeKind {
    // Ownership first, and deliberately so: a BYON machine that also carries the
    // tag is still the customer's. Core asks the same question in the same
    // order (its external predicate requires OwnerID == nil), so a tagged
    // tenant node must never be able to appear under "your own machines".
    if (n.ownerId != null && n.ownerId !== '') return 'byon';
    return hasExternalTag(n) ? 'external' : 'platform';
}

export function isKind<T extends NodeKindFields>(nodes: T[], kind: NodeKind): T[] {
    return nodes.filter(n => nodeKind(n) === kind);
}

/** What each tab is called, and what it is for. */
export const NODE_KIND_LABEL: Record<NodeKind, string> = {
    platform: 'Nodes',
    external: 'External',
    byon: 'Customer nodes',
};

export const NODE_KIND_DESCRIPTION: Record<NodeKind, string> = {
    platform: 'The machines this platform runs on.',
    external: 'Your own machines outside the swarm. They route only through the gateway, so they need it enabled to be reachable.',
    byon: 'Machines customers brought themselves. You do not own these, and a customer has root on their own hardware.',
};
