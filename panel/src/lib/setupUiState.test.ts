import { describe, it, expect } from 'vitest';
import { setupUiState } from './setupUiState';

describe('setupUiState', () => {
  it('fresh_install + unset -> open', () => {
    expect(setupUiState({ mode: 'fresh_install', adminSecretConfigured: false })).toBe('open');
  });
  it('fresh_install + configured -> secret_required', () => {
    expect(setupUiState({ mode: 'fresh_install', adminSecretConfigured: true })).toBe('secret_required');
  });
  it('lost_admin + configured -> secret_required', () => {
    expect(setupUiState({ mode: 'lost_admin', adminSecretConfigured: true })).toBe('secret_required');
  });
  it('lost_admin + unset -> recovery_closed', () => {
    expect(setupUiState({ mode: 'lost_admin', adminSecretConfigured: false })).toBe('recovery_closed');
  });
  it('complete + configured -> secret_required (break-glass)', () => {
    expect(setupUiState({ mode: 'complete', adminSecretConfigured: true })).toBe('secret_required');
  });
  it('complete + unset -> complete', () => {
    expect(setupUiState({ mode: 'complete', adminSecretConfigured: false })).toBe('complete');
  });
});
