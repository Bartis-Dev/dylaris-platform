// Pure setup-wizard UI selector. Import-free on purpose so its vitest can load
// without the "@/" alias (vitest has no alias/tsconfig-paths configured, and
// api/setup.ts imports "@/lib/api/core"). The mode union is inlined here rather
// than imported from api/setup.ts for the same reason.

export type SetupUiState =
  | 'open'            // pristine first boot, no secret needed
  | 'secret_required' // a form, gated by ADMIN_SECRET
  | 'recovery_closed' // users but no admin and no secret: nothing to offer
  | 'disabled'        // env SETUP is off on an instance that already has an admin
  | 'complete';       // nothing to do here

// setupUiState picks the screen. `open` is CORE's answer, not ours: it is
// computed there by the same gate the create endpoint enforces, so the page
// cannot offer a form the handler will refuse.
//
// The reason for a separate 'disabled' state: "setup is switched off" and "you
// need the admin secret" are different problems with different fixes, and an
// operator sent hunting for a secret that would not have helped is the failure
// this avoids.
export function setupUiState(s: {
  mode: 'fresh_install' | 'lost_admin' | 'complete';
  adminSecretConfigured: boolean;
  open: boolean;
}): SetupUiState {
  // Core says the door is shut. Only possible once an admin exists - with no
  // admin, Core ignores the switch rather than locking everyone out.
  if (!s.open) return s.mode === 'complete' ? 'disabled' : 'complete';
  // A configured secret gates admin creation in every mode.
  if (s.adminSecretConfigured) return 'secret_required';
  // No secret configured:
  if (s.mode === 'fresh_install') return 'open'; // pristine first boot is open
  if (s.mode === 'lost_admin') return 'recovery_closed'; // users but no admin, no recovery
  // Open, no secret, and an admin exists. Core refuses this on submit, so
  // offering the form would be a lie; the missing-token banner explains it.
  return 'recovery_closed';
}
