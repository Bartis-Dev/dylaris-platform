import { describe, it, expect } from 'vitest';
import { setupUiState } from './setupUiState';

// The rule these pin: the page never offers a form Core will refuse, and it
// names the right cause when it refuses.
//
// `open` comes from Core, computed by the gate the create endpoint enforces.
// Recomputing it here would be the drift this whole shape exists to prevent.
describe('setupUiState', () => {
  it('a pristine first boot with no secret is open', () => {
    expect(setupUiState({ mode: 'fresh_install', adminSecretConfigured: false, open: true })).toBe('open');
  });

  it('a configured secret gates the form in every open mode', () => {
    expect(setupUiState({ mode: 'fresh_install', adminSecretConfigured: true, open: true })).toBe('secret_required');
    expect(setupUiState({ mode: 'lost_admin', adminSecretConfigured: true, open: true })).toBe('secret_required');
    expect(setupUiState({ mode: 'complete', adminSecretConfigured: true, open: true })).toBe('secret_required');
  });

  it('a lost admin with no secret has no recovery to offer', () => {
    expect(setupUiState({ mode: 'lost_admin', adminSecretConfigured: false, open: true })).toBe('recovery_closed');
  });

  // The case env SETUP was added for. Named separately from secret_required
  // because the fix is a different env var.
  it('a live instance with setup switched off says so', () => {
    expect(setupUiState({ mode: 'complete', adminSecretConfigured: true, open: false })).toBe('disabled');
    expect(setupUiState({ mode: 'complete', adminSecretConfigured: false, open: false })).toBe('disabled');
  });

  // Core never closes the door while no admin exists, so these combinations
  // only arise from the transport-failure fallback, which reports mode
  // 'complete'. Whatever produced them, offering a form would be wrong.
  it('never offers a form when Core says the door is shut', () => {
    for (const mode of ['fresh_install', 'lost_admin'] as const) {
      expect(setupUiState({ mode, adminSecretConfigured: true, open: false })).not.toBe('open');
      expect(setupUiState({ mode, adminSecretConfigured: true, open: false })).not.toBe('secret_required');
    }
  });

  // Open, no secret, and an admin already exists: Core refuses this on submit,
  // so the page must not pretend otherwise.
  it('does not offer a secretless form on a live instance', () => {
    expect(setupUiState({ mode: 'complete', adminSecretConfigured: false, open: true })).toBe('recovery_closed');
  });
});
