// Pure display + policy helpers for the owner Access page. The backend
// permissions model is unchanged: stored mode values stay off/simple/advanced,
// and these helpers only decide how the panel labels and gates them.

import type { PermissionsMode, Preset } from '@/lib/api/authzCatalog';
import type { Grant } from '@/lib/api/grants';
import type { Server } from '@/lib/api';

// Stored value -> displayed mode name. simple == "Full-only", advanced ==
// "Admin-roles"; the stored strings never change.
export const MODE_LABELS: Record<PermissionsMode, string> = {
    off: 'Off',
    simple: 'Full-only',
    advanced: 'Admin-roles',
};

export const MODE_HELP: Record<PermissionsMode, string> = {
    off: 'Nobody but the owner and panel staff can act on a server.',
    simple: 'Invite a friend as a complete server admin. One single full-access level, no per-role picking.',
    advanced: 'Define named server roles and hand out granular per-friend capability overrides.',
};

export function modeLabel(mode: PermissionsMode): string { return MODE_LABELS[mode]; }
export function modeHelp(mode: PermissionsMode): string { return MODE_HELP[mode]; }

// A scope (numeric server id, or null for account-wide) points at a proxy.
// The inherit flag only does anything for a proxy, so the UI shows the inherit
// checkbox only when this is true. serverType is optional; treat missing as a
// game server.
export function isProxyScope(servers: Server[], serverId: number | null): boolean {
    if (serverId == null) return false;
    return servers.find(s => s.id === serverId)?.serverType === 'proxy';
}

// The code-defined "Server admin" preset id. Full-only mode grants exactly
// this preset's caps and never a lesser one.
export const FULL_ACCESS_PRESET_ID = 'admin';

export function fullAccessCaps(presets: Preset[]): string[] {
    return presets.find(p => p.id === FULL_ACCESS_PRESET_ID)?.capabilities ?? [];
}

function sameSet(a: string[], b: string[]): boolean {
    if (a.length !== b.length) return false;
    const bs = new Set(b);
    return a.every(x => bs.has(x));
}

// Honest label for a grant's access level. Only reports "Full access" when the
// grant carries exactly the full-access caps, no denies, and no named role -
// so a legacy or partial grant is never mislabeled.
export function describeGrantAccess(g: Grant, fullAccessCapIds: string[]): string {
    if (g.serverRoleName) return g.serverRoleName;
    if (fullAccessCapIds.length > 0 && g.denyCaps.length === 0 && sameSet(g.grantCaps, fullAccessCapIds)) {
        return 'Full access';
    }
    return 'Custom';
}

// Whether the grant can be edited without the current mode misrepresenting it.
// Advanced supports arbitrary caps so any grant is editable. Full-only only
// offers "Full access", so a partial/legacy grant is revoke-only there. Off
// disables editing entirely.
export function canEditInMode(g: Grant, mode: PermissionsMode, fullAccessCapIds: string[]): boolean {
    if (mode === 'off') return false;
    if (mode === 'advanced') return true;
    return describeGrantAccess(g, fullAccessCapIds) === 'Full access';
}
