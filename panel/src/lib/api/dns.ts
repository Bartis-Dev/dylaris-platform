// On-demand DNS + reachability check for the operator's own configured
// domains. Hits the admin-only GET /api/gateway/dns-check, which computes the
// required records from gateway_hoster_domains / gateway_cname_target /
// FRONTEND_URL and resolves them against a public resolver. No state is
// written — this is a pure verification surface.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

// Record category — drives the plain-language explanation shown per row.
export type DnsRecordCategory = 'player' | 'wildcard' | 'cname' | 'panel' | 'api' | 'beam';

export type DnsRecordType = 'A' | 'CNAME';

// Per-record verdict. ok = resolves to an expected target; mismatch = resolves
// but to the wrong value; missing = no record at all; unresolved = lookup
// failed / target itself doesn't resolve (e.g. CNAME target, panel origin).
// 'info' = advisory row, nothing to fix (e.g. the optional player-base record).
export type DnsRecordStatus = 'ok' | 'mismatch' | 'missing' | 'unresolved' | 'info';

export interface DnsRecord {
    category: DnsRecordCategory;
    type: DnsRecordType;
    name: string;
    expected: string[];
    actual: string[];
    status: DnsRecordStatus;
    hint: string;
}

export interface DnsReachability {
    target: string;
    ok: boolean;
    hint: string;
}

export interface DnsCheckResult {
    success: boolean;
    records: DnsRecord[];
    reachability: DnsReachability[];
    edges: string[];
    checkedAt: string;
    message?: string;
}

// Runs the check. The backend lookups can take a few seconds (per-record DNS
// + TCP dials), so callers should show a spinner while this is in flight.
export async function checkDns(): Promise<DnsCheckResult> {
    try {
        const res = await fetch(`${API_URL}/gateway/dns-check`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as DnsCheckResult;
    } catch (err) {
        return handleError(err) as DnsCheckResult;
    }
}
