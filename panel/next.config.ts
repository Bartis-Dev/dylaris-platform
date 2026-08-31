import type { NextConfig } from "next";

// `next dev` and `next build` deliberately get DIFFERENT shapes here, and the
// reason is the session.
//
// The panel ships as a static bundle inside Core's binary, so the build is an
// export: no Next server at runtime, and nothing is lost because every component
// is already a client component. But the DEV server is a server, on a port of
// its own - and a session that is a host-only cookie does not cross a port. A
// dev panel talking to Core at localhost:25500 could log in and never hold the
// result: the browser discards a Set-Cookie from a cross-origin fetch, and sends
// nothing back afterwards.
//
// So dev proxies /api to Core instead of pointing the browser at it. That makes
// development same-origin, which is what production is, so the dev loop exercises
// the same cookie path rather than one that only worked while the token sat in
// localStorage. `output: export` is dropped in dev because it disables rewrites.
const isDev = process.env.NODE_ENV === "development";

// Where Core listens for the dev proxy. An env var so a developer running Core
// on another port or host does not have to edit this file.
const coreOrigin = process.env.DEV_CORE_ORIGIN || "http://localhost:25500";

const nextConfig: NextConfig = isDev
    ? {
        async rewrites() {
            return [{ source: "/api/:path*", destination: `${coreOrigin}/api/:path*` }];
        },
    }
    : {
        // Compiled into Core, see core/panelfs. What DID depend on a Next server
        // was the nonce CSP in proxy.ts; that moved to Core, see
        // scripts/stamp-nonce.mjs and core/panelfs.
        output: "export",
    };

export default nextConfig;
