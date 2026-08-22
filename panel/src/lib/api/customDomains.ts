// A tenant's own custom-domain ownership claims.
//
// Pointing your own domain at the platform needs proof that you control it,
// because otherwise anyone could claim any name. The proof is the DNS record
// itself: only the zone's owner can make a domain resolve to us.
//
// Everything here is scoped to the signed-in user inside Core. A block is
// recorded per (user, domain), never per domain - a global block would let
// someone lock a competitor's domain out of the platform by entering it and
// never configuring it.

import { API_URL, getAuthHeader, handleResponse } from '@/lib/api/core';

// pending   - inside the grant, waiting for DNS to point at us
// verified  - proven, routes may be added freely
// blocked   - one missed deadline; another attempt is allowed
// permablocked - out of attempts; only the TXT record lifts it
export type CustomDomainState = 'pending' | 'verified' | 'blocked' | 'permablocked';

export interface CustomDomain {
  domain: string;
  state: CustomDomainState;
  attempts: number;
  deadlineAt?: string;
  // Present only for a permanently blocked domain that has been issued a token.
  txtName?: string;
  txtValue?: string;
}

export const listCustomDomains = async (): Promise<CustomDomain[]> => {
  const res = await fetch(`${API_URL}/gateway/custom-domains`, { headers: getAuthHeader() });
  const data = await handleResponse(res);
  return data?.domains ?? [];
};

/** Mints (or returns) the TXT record that lifts a permanent block. */
export const issueCustomDomainToken = async (domain: string): Promise<CustomDomain> => {
  const res = await fetch(
    `${API_URL}/gateway/custom-domains/${encodeURIComponent(domain)}/txt-token`,
    { method: 'POST', headers: getAuthHeader() },
  );
  const data = await handleResponse(res);
  return data?.domain;
};

/** Checks the published TXT record. 409 means "not visible yet", not "wrong". */
export const verifyCustomDomainTXT = async (domain: string) => {
  const res = await fetch(
    `${API_URL}/gateway/custom-domains/${encodeURIComponent(domain)}/verify-txt`,
    { method: 'POST', headers: getAuthHeader() },
  );
  return handleResponse(res);
};
