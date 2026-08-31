// Stamps a nonce PLACEHOLDER onto every script tag in the static export.
//
// The panel used to mint a per-request nonce in Next middleware. A static export
// has no request, so the nonce moves to the only thing left that sees one: Core,
// which serves these files. The build writes a fixed placeholder, Core replaces
// it with a fresh value on every response and sends the matching
// Content-Security-Policy header.
//
// Why not hashes. With no server the inline script set is fixed, so 'sha256-...'
// sources would work and need no runtime step at all. They are NOT used because
// the Beam desktop client reads the nonce back out of the panel's CSP header
// (beam/app/proxy.go, beamCSPForPanel) and builds its own nonce-strict policy
// from it - finding none, it falls back to a policy with 'unsafe-inline' on
// script-src. Hashes would silently downgrade the desktop app.
//
// Runs after `next build`. Idempotent: a tag that already carries a nonce is
// left alone, so a second run over the same output changes nothing.

import { readdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

// CROSS-LANGUAGE CONTRACT: core/panelfs replaces this exact literal. Changing it
// on one side without the other leaves every script un-nonced and the browser
// blocks the whole bundle - which is loud, at least.
const PLACEHOLDER = '__DYLARIS_CSP_NONCE__';

// Matches an opening <script ...> that does not already carry a nonce.
// "</script>" cannot match: the "<" there is followed by "/", not "s".
const SCRIPT_OPEN = /<script(?![^>]*\snonce=)(?=[\s>])/g;

// A <link> that PRELOADS a script is checked against script-src too, and under
// 'strict-dynamic' the host-source is disabled - so a preload without a nonce is
// refused exactly like a script tag without one.
//
// This cost an hour to find and would never have shown up in a unit test: the
// page still rendered, the app still navigated, and the only symptom was one
// console line naming a chunk that also appears, correctly nonced, further down
// the same document. Found by loading the page in a real browser.
const LINK_PRELOAD = /<link(?![^>]*\snonce=)(?=[^>]*(?:as="script"|rel="modulepreload"))(?=[\s>])/g;

async function* htmlFiles(dir) {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
        const p = join(dir, entry.name);
        if (entry.isDirectory()) yield* htmlFiles(p);
        else if (entry.name.endsWith('.html')) yield p;
    }
}

const out = process.argv[2] || 'out';
let files = 0;
let tags = 0;

for await (const file of htmlFiles(out)) {
    const html = await readFile(file, 'utf8');
    let n = 0;
    const stamped = html
        .replace(SCRIPT_OPEN, () => {
            n++;
            return `<script nonce="${PLACEHOLDER}"`;
        })
        .replace(LINK_PRELOAD, () => {
            n++;
            return `<link nonce="${PLACEHOLDER}"`;
        });
    if (n === 0) continue;
    await writeFile(file, stamped);
    files++;
    tags += n;
}

// Zero stamped tags means the export shape changed under us - a Next version
// that emits no inline bootstrap, or an output directory that is not there. The
// build must fail rather than ship HTML whose scripts the CSP will block.
if (tags === 0) {
    console.error(`stamp-nonce: no script tags found under ${out}/ - refusing to ship an un-nonced bundle`);
    process.exit(1);
}
console.log(`stamp-nonce: ${tags} script tags and script preloads in ${files} files`);
