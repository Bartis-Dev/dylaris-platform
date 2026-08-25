// The DNS provider credential, entered here and stored by the gateway HUB.
//
// Core forwards this form and keeps no copy. That is the point: the Hub's
// reconciler deletes records it does not plan, so there is exactly one writer
// and exactly one place the credential lives. Nothing in this file ever receives
// the token back — `hasToken` is the only thing a screen needs to know.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export interface DnsProviderOption {
    name: string;
    label: string;
}

export interface GatewayDnsConfig {
    provider: string;
    zones: string[];
    enabled: boolean;
    // has_token says a credential is stored without revealing it. The field is
    // write-only end to end.
    has_token: boolean;
    // env_locked means a credential mounted as DNS_API_TOKEN on the hub wins
    // over anything saved here. Worth showing: otherwise a saved token that
    // never takes effect looks like a bug.
    env_locked: boolean;
    providers: DnsProviderOption[];
}

export interface GatewayDnsResponse {
    success: boolean;
    // available=false means no gateway is deployed (GATEWAY_HUB_URL unset on
    // Core). Not an error: a platform-only install has no records to write.
    available?: boolean;
    config?: GatewayDnsConfig;
    message?: string;
}

export async function getGatewayDns(): Promise<GatewayDnsResponse> {
    try {
        const res = await fetch(`${API_URL}/settings/gateway/dns`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as GatewayDnsResponse;
    } catch (err) {
        return handleError(err) as GatewayDnsResponse;
    }
}

export interface GatewayDnsSave {
    provider: string;
    // Blank KEEPS the stored credential. Sending '' to mean "clear it" would
    // disable DNS every time someone edited a zone.
    token: string;
    zones: string[];
    enabled: boolean;
}

export async function saveGatewayDns(body: GatewayDnsSave): Promise<GatewayDnsResponse> {
    try {
        const res = await fetch(`${API_URL}/settings/gateway/dns`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        });
        return (await handleResponse(res)) as GatewayDnsResponse;
    } catch (err) {
        return handleError(err) as GatewayDnsResponse;
    }
}

// parseZones turns the comma-separated field into the list the API takes.
// Exported so the rule is unit-testable: a trailing comma must not become a zone
// named '', which the backend would then refuse to match anything against.
export function parseZones(raw: string): string[] {
    return raw
        .split(',')
        .map(z => z.trim().toLowerCase())
        .filter(z => z.length > 0);
}
