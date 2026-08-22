import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

/**
 * gateway_cname_target is a single LABEL ("route"), never a usable DNS name. It
 * only becomes an instruction once cnameTargetsFor combines it with each hoster
 * base domain.
 *
 * Two components had that right from the start; the custom-domain panel, added
 * later, rendered the label directly - "Add a CNAME to route". A customer who
 * followed it created a record that resolves nowhere, and then watched the claim
 * expire and their route be removed on the deadline. Nothing errored: the
 * instruction was simply wrong, and only a customer could have noticed.
 *
 * So this is the guard the next component gets for free. Any file that touches
 * cnameTarget must also import cnameTargetsFor - except the settings tab, which
 * is where an admin TYPES the label and is the one place the raw value belongs.
 */
const ALLOWED_WITHOUT_EXPANSION = new Set(['src/components/settings/GatewayTab.tsx']);

function sourceFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      sourceFiles(full, out);
    } else if (/\.(ts|tsx)$/.test(entry) && !/\.test\.tsx?$/.test(entry)) {
      out.push(full);
    }
  }
  return out;
}

describe('cnameTarget is never rendered raw', () => {
  it('every consumer expands the label through cnameTargetsFor', () => {
    const offenders: string[] = [];
    for (const file of sourceFiles('src')) {
      const rel = file.split(String.fromCharCode(92)).join('/');
      if (rel.endsWith('src/lib/cnameTargets.ts')) continue;
      const body = readFileSync(file, 'utf8');
      if (!/\bcnameTarget\b/.test(body)) continue;
      if (ALLOWED_WITHOUT_EXPANSION.has(rel)) continue;
      // A type declaration alone carries no instruction to anyone.
      if (/^\s*cnameTarget\??:\s*string;?\s*$/m.test(body) && !/cnameTarget[^:]/.test(body)) continue;
      if (!body.includes('cnameTargetsFor')) offenders.push(rel);
    }
    expect(offenders, 'these show or compare the bare label as if it were a domain').toEqual([]);
  });
});
