import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // The panel ships as a static bundle inside Core's binary, so there is no
  // Next server at runtime. Everything here is a client component already - no
  // API routes, no server actions, no server-side data fetching - so nothing is
  // lost. What DID depend on a server was the nonce CSP in proxy.ts; that moved
  // to Core, see scripts/stamp-nonce.mjs and core/panelfs.
  output: "export",
};

export default nextConfig;
