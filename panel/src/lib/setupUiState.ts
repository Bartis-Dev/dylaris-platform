// Pure setup-wizard UI selector. Import-free on purpose so its vitest can load
// without the "@/" alias (vitest has no alias/tsconfig-paths configured, and
// api/setup.ts imports "@/lib/api/core"). The mode union is inlined here rather
// than imported from api/setup.ts for the same reason.

export type SetupUiState = 'open' | 'secret_required' | 'recovery_closed' | 'complete';

export function setupUiState(s: {
  mode: 'fresh_install' | 'lost_admin' | 'complete';
  adminSecretConfigured: boolean;
}): SetupUiState {
  // A configured secret gates admin creation in every mode.
  if (s.adminSecretConfigured) return 'secret_required';
  // No secret configured:
  if (s.mode === 'fresh_install') return 'open'; // pristine first boot is open
  if (s.mode === 'lost_admin') return 'recovery_closed'; // users but no admin, no recovery
  return 'complete'; // admin exists and no break-glass -> normal lock
}
