// buildCsp assembles the Panel's Content-Security-Policy. It is the single
// source of truth for the policy so it can be unit-tested without booting Next,
// and so the Beam proxy can mirror the same allowances. script-src is
// nonce-strict ('nonce-<N>' + 'strict-dynamic', no 'unsafe-inline'); style-src
// stays pragmatic ('unsafe-inline') because Next and Tailwind emit inline
// <style> the browser would otherwise block, and script - not style - is the
// high-value injection target. Dev adds 'unsafe-eval' for React Refresh / HMR.
// https://cdnjs.cloudflare.com is kept as a belt-and-suspenders host-source for
// jszip (injected at runtime by trusted code; ignored by CSP3 browsers under
// 'strict-dynamic', honored by older ones).
export function buildCsp(nonce: string, isDev: boolean): string {
  const scriptSrc = [
    "'self'",
    `'nonce-${nonce}'`,
    "'strict-dynamic'",
    'https://cdnjs.cloudflare.com',
    ...(isDev ? ["'unsafe-eval'"] : []),
  ].join(' ');
  return [
    "default-src 'self'",
    `script-src ${scriptSrc}`,
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob: https://cravatar.eu https://cdn.modrinth.com",
    "font-src 'self'",
    "connect-src 'self' https://api.dylaris.com",
    "object-src 'none'",
    "base-uri 'none'",
    "form-action 'self'",
    'frame-src https: http:',
    "frame-ancestors 'none'",
  ].join('; ');
}
