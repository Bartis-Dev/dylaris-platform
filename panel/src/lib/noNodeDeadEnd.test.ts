import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';

/**
 * The create-server wizard shows a "No nodes yet" panel to any non-admin whose
 * account has no online node. That is the highest purchase intent in the whole
 * panel: the customer opened the wizard to spend money.
 *
 * It shipped with a single Close button. Every other surface that tells a user
 * something is missing offers the way forward (the nodes page header, the store
 * account page, the NotIncluded block in DeployKit); this one branch did not,
 * and nothing errored - the customer simply could not proceed.
 *
 * So the check is not "does a Link exist somewhere in this file" but "does the
 * showNoNodeMsg branch of the FOOTER offer more than a dismiss button".
 */
const WIZARD = 'src/components/CreateServerWizard.tsx';

/** The footer's showNoNodeMsg branch: from the ternary to its closing `) : (`. */
function noNodeFooterBranch(source: string): string {
  const footer = source.indexOf('{/* Footer */}');
  expect(footer, 'the wizard no longer has a marked Footer block').toBeGreaterThan(-1);
  const branch = source.indexOf('showNoNodeMsg ?', footer);
  expect(branch, 'the footer no longer branches on showNoNodeMsg').toBeGreaterThan(-1);
  const end = source.indexOf(') : (', branch);
  expect(end, 'the showNoNodeMsg branch is unterminated').toBeGreaterThan(-1);
  return source.slice(branch, end);
}

describe('the no-node wizard panel is not a dead end', () => {
  const branch = noNodeFooterBranch(readFileSync(WIZARD, 'utf8'));

  it('offers a route out, not only a dismiss', () => {
    const navigates = /<Link\b|<a\b|router\.(push|replace)/.test(branch);
    expect(navigates, 'a customer with no node can only Close this panel').toBe(true);
  });

  it('sends them to /nodes, which carries the store link for every reason they got here', () => {
    expect(branch).toMatch(/["']\/nodes["']/);
  });
});
