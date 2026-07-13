import { NextResponse } from 'next/server';
import type { NextRequest } from 'next/server';
import { buildCsp } from '@/lib/csp';

// proxy is Next 16's renamed middleware. It applies a per-request nonce-strict
// CSP: it mints a fresh nonce, exposes it to the render via the request headers
// (Next stamps its own inline framework scripts with the nonce it finds in the
// Content-Security-Policy request header) AND sets the enforced policy on the
// response. x-nonce is set so a component that must emit its own inline script
// can read the same value. Runs on the nodejs runtime (proxy's default in Next
// 16), where the Web Crypto `crypto` global and Buffer are both available.
export function proxy(request: NextRequest) {
  const nonce = Buffer.from(crypto.randomUUID()).toString('base64');
  const isDev = process.env.NODE_ENV === 'development';
  const csp = buildCsp(nonce, isDev);

  const requestHeaders = new Headers(request.headers);
  requestHeaders.set('x-nonce', nonce);
  requestHeaders.set('Content-Security-Policy', csp);

  const response = NextResponse.next({
    request: { headers: requestHeaders },
  });
  response.headers.set('Content-Security-Policy', csp);
  return response;
}

// Run on every navigable route, but skip hashed static assets, the image
// optimizer, and the favicon (they never need a nonce, and forcing them dynamic
// would waste the static cache). The `missing` guard skips RSC prefetch requests
// so a prefetched document is not cached with a nonce that won't match the
// eventual full navigation - the documented Next nonce recipe.
export const config = {
  matcher: [
    {
      source: '/((?!_next/static|_next/image|favicon.ico).*)',
      missing: [
        { type: 'header', key: 'next-router-prefetch' },
        { type: 'header', key: 'purpose', value: 'prefetch' },
      ],
    },
  ],
};
