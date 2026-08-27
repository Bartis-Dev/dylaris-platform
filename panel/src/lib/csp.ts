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
//
// apiOrigin is the origin of a cross-origin Core API (PANEL_API_URL /
// NEXT_PUBLIC_API_URL). When the panel is served on a different origin than
// Core - a self-hoster whose API lives on api.example.com, or a split-port dev
// setup - connect-src must allow it, otherwise the browser blocks every API
// fetch before it leaves the page. Empty / same-origin deployments leave it
// unset and rely on 'self'. connect-src carries NO hardcoded vendor host: the
// only cross-origin target is whatever the operator configures, so a self-host
// build's policy lists solely its own origins. The hosted deployment sets
// PANEL_API_URL like any other operator and is covered by the same path.
// tabHostSuffix is the DNS suffix proxied custom tabs are served under
// (TAB_PROXY_HOST_SUFFIX on Core). Each tab lives on its OWN host below it, and
// the panel calls that host directly to mint the tab's ticket cookie - so
// connect-src has to allow the whole family or the browser blocks the mint and
// every proxied tab fails to authorize.
//
// It is written WITHOUT a scheme on purpose: a bare host-source takes the
// document's own scheme (with a secure upgrade allowed), so one entry is
// correct for the https panel in production and the http one in dev. Hardcoding
// https would break local development silently, which is the same class of
// mistake as leaving the suffix out entirely.
export function buildCsp(nonce: string, isDev: boolean, apiOrigin?: string, tabHostSuffix?: string): string {
  const scriptSrc = [
    "'self'",
    `'nonce-${nonce}'`,
    "'strict-dynamic'",
    'https://cdnjs.cloudflare.com',
    ...(isDev ? ["'unsafe-eval'"] : []),
  ].join(' ');
  const connectSrc = [
    "'self'",
    ...(apiOrigin ? [apiOrigin] : []),
    ...(tabHostSuffix ? [`*.${tabHostSuffix}`] : []),
  ].join(' ');
  // img-src needs the API origin for the same reason connect-src does, and it
  // was missed when connect-src got it: the server-icon preview on
  // Config -> Display is an <img> pointing at Core's /files/download, so on a
  // split-origin deployment the browser refused it and the icon simply never
  // appeared. The vendor hosts stay - those are fixed third parties (skin
  // avatars, Modrinth thumbnails), not operator config.
  const imgSrc = [
    "'self'",
    'data:',
    'blob:',
    ...(apiOrigin ? [apiOrigin] : []),
    'https://cravatar.eu',
    'https://cdn.modrinth.com',
  ].join(' ');
  return [
    "default-src 'self'",
    `script-src ${scriptSrc}`,
    "style-src 'self' 'unsafe-inline'",
    `img-src ${imgSrc}`,
    "font-src 'self'",
    `connect-src ${connectSrc}`,
    "object-src 'none'",
    "base-uri 'none'",
    "form-action 'self'",
    'frame-src https: http:',
    "frame-ancestors 'none'",
  ].join('; ');
}
