"use client";

import { useState, useEffect } from 'react';
import { X, ShieldCheck, ShieldOff, Copy, Check, AlertTriangle } from 'lucide-react';
import { QRCodeSVG } from 'qrcode.react';
import { setupTOTP, verifyTOTP, disableTOTP } from '@/lib/api/auth';

interface UserProfile {
    username: string;
    minecraftUsername?: string;
    email?: string;
    is2FAEnabled?: boolean;
}

interface ProfilePopupProps {
  currentUser: UserProfile;
  onClose: () => void;
  onUpdate: (data: {
      newUsername?: string;
      oldPassword: string;
      newPassword?: string;
      minecraftUsername?: string;
      email?: string;
  }) => Promise<void>;
  onTwoFactorChange?: () => void;
  error: string;
  success: string;
}

const ProfilePopup: React.FC<ProfilePopupProps> = ({ currentUser, onClose, onUpdate, onTwoFactorChange, error, success }) => {
  const [currentView, setCurrentView] = useState("general");

  const [newUsername, setNewUsername] = useState(currentUser.username || "");
  const [minecraftUsername, setMinecraftUsername] = useState(currentUser.minecraftUsername || "");
  const [email, setEmail] = useState(currentUser.email || "");

  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");

  const [loading, setLoading] = useState(false);

  // 2FA wizard state
  const [twoFactorOpen, setTwoFactorOpen] = useState(false);
  const [twoFactorMode, setTwoFactorMode] = useState<'enable' | 'disable'>('enable');
  const [twoFactorEnabled, setTwoFactorEnabled] = useState(currentUser.is2FAEnabled || false);

  const handleSubmit = async (e: React.FormEvent) => {
      e.preventDefault();
      setLoading(true);
      await onUpdate({
          newUsername,
          oldPassword,
          newPassword: newPassword === confirmPassword ? newPassword : "",
          minecraftUsername,
          email,
      });
      setLoading(false);
  };

  return (
    <>
    <div className="modal-overlay animate-fade-in">
      <div className="modal-panel w-full max-w-md">
        <div className="modal-header flex justify-between items-center">
          <h2 className="modal-title">Profile Settings</h2>
          <button onClick={onClose} className="text-(--base-06) hover:text-(--error-light) transition-colors">
            <X size={20} />
          </button>
        </div>

        <div className="flex gap-1 px-6 pt-4 border-b border-(--base-03)">
          <button onClick={() => setCurrentView("general")} className={`pb-2.5 px-3 font-medium text-sm transition-colors ${currentView === "general" ? "border-b-2 border-(--accent) text-(--accent-light)" : "text-(--base-07) hover:text-(--base-09)"}`}>General</button>
          <button onClick={() => setCurrentView("security")} className={`pb-2.5 px-3 font-medium text-sm transition-colors ${currentView === "security" ? "border-b-2 border-(--accent) text-(--accent-light)" : "text-(--base-07) hover:text-(--base-09)"}`}>Security</button>
        </div>

        <div className="modal-body">
          {error && <div className="alert alert-error mb-4 font-medium">{error}</div>}
          {success && <div className="alert alert-success mb-4 font-medium">{success}</div>}

          <form onSubmit={handleSubmit} className="space-y-4">
            {currentView === "general" && (
              <div className="space-y-4 animate-fade-in">
                <div className="flex flex-col gap-[5px]">
                  <label className="input-label">Username</label>
                  <input type="text" value={newUsername} onChange={e => setNewUsername(e.target.value)} disabled={loading} className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed" />
                </div>
                <div className="flex flex-col gap-[5px]">
                  <label className="input-label">Email (Optional)</label>
                  <input type="email" value={email} onChange={e => setEmail(e.target.value)} disabled={loading} className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed" />
                </div>
                <div className="flex flex-col gap-[5px]">
                  <label className="input-label">Minecraft Username (For Avatar)</label>
                  <input type="text" value={minecraftUsername} onChange={e => setMinecraftUsername(e.target.value)} disabled={loading} className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed" />
                </div>
              </div>
            )}

            {currentView === "security" && (
              <div className="space-y-4 animate-fade-in">
                <div className="flex flex-col gap-[5px]">
                  <label className="input-label">New Password</label>
                  <input type="password" value={newPassword} onChange={e => setNewPassword(e.target.value)} disabled={loading} className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed" placeholder="Leave blank to keep current" />
                </div>
                {newPassword && (
                  <div className="flex flex-col gap-[5px]">
                    <label className="input-label">Confirm Password</label>
                    <input type="password" value={confirmPassword} onChange={e => setConfirmPassword(e.target.value)} disabled={loading} className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed" />
                  </div>
                )}

                {/* 2FA section */}
                <div className="pt-2">
                  <div className="flex items-center justify-between gap-3 p-3 rounded-md bg-(--base-02) border border-(--base-03)">
                    <div className="flex items-start gap-2.5 min-w-0">
                      {twoFactorEnabled
                        ? <ShieldCheck size={16} className="text-(--success-light) shrink-0 mt-0.5" />
                        : <ShieldOff size={16} className="text-(--base-06) shrink-0 mt-0.5" />}
                      <div className="min-w-0">
                        <div className="font-medium text-sm text-(--base-09)">Two-Factor Authentication</div>
                        <div className="text-xs text-(--base-06)">
                          {twoFactorEnabled
                            ? 'Enabled — login requires an authenticator code.'
                            : 'Add an extra layer of security via TOTP authenticator app.'}
                        </div>
                      </div>
                    </div>
                    <button
                      type="button"
                      onClick={() => {
                        setTwoFactorMode(twoFactorEnabled ? 'disable' : 'enable');
                        setTwoFactorOpen(true);
                      }}
                      className={`shrink-0 px-3 py-1.5 rounded-md text-xs font-medium transition-colors ${
                        twoFactorEnabled
                          ? 'bg-(--error-ghost) text-(--error-light) hover:bg-(--error)/15 border border-(--error)/15'
                          : 'bg-(--accent-ghost) text-(--accent-light) hover:bg-(--accent)/15 border border-(--accent-border)'
                      }`}
                    >
                      {twoFactorEnabled ? 'Disable' : 'Enable'}
                    </button>
                  </div>
                </div>
              </div>
            )}

            <div className="pt-4 border-t border-(--base-03)">
              <div className="flex flex-col gap-[5px]">
                <label className="input-label">Current Password <span className="opacity-70">(required to save profile changes)</span></label>
                <input type="password" value={oldPassword} onChange={e => setOldPassword(e.target.value)} required disabled={loading} className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed" />
              </div>
            </div>

            <button type="submit" disabled={loading} className="btn btn-primary btn-lg w-full mt-4">
              {loading ? 'Saving...' : 'Save Changes'}
            </button>
          </form>
        </div>
      </div>
    </div>

    {twoFactorOpen && (
      <TwoFactorWizard
        mode={twoFactorMode}
        onClose={() => setTwoFactorOpen(false)}
        onComplete={() => {
          setTwoFactorOpen(false);
          setTwoFactorEnabled(twoFactorMode === 'enable');
          onTwoFactorChange?.();
        }}
      />
    )}
    </>
  );
};

// ─────────────────────────────────────────────
// Two-Factor Wizard
// ─────────────────────────────────────────────

function TwoFactorWizard({ mode, onClose, onComplete }: {
  mode: 'enable' | 'disable';
  onClose: () => void;
  onComplete: () => void;
}) {
  if (mode === 'enable') return <EnableWizard onClose={onClose} onComplete={onComplete} />;
  return <DisableWizard onClose={onClose} onComplete={onComplete} />;
}

type EnableStep = 'loading' | 'scan' | 'backup';

function EnableWizard({ onClose, onComplete }: { onClose: () => void; onComplete: () => void }) {
  const [step, setStep] = useState<EnableStep>('loading');
  const [secret, setSecret] = useState('');
  const [otpURL, setOtpURL] = useState('');
  const [code, setCode] = useState('');
  const [backupCodes, setBackupCodes] = useState<string[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [acknowledged, setAcknowledged] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    setupTOTP().then(res => {
      if (res?.success) {
        setSecret(res.secret);
        setOtpURL(res.otpAuthURL);
        setStep('scan');
      } else {
        setError(res?.message || 'Failed to start 2FA setup');
      }
    });
  }, []);

  const handleVerify = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      const res = await verifyTOTP(secret, code.replace(/\s/g, ''));
      if (res?.success && Array.isArray(res.backupCodes)) {
        setBackupCodes(res.backupCodes);
        setStep('backup');
      } else {
        setError(res?.message || 'Invalid code');
      }
    } finally {
      setBusy(false);
    }
  };

  const copyAll = async () => {
    try {
      await navigator.clipboard.writeText(backupCodes.join('\n'));
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch { /* ignore */ }
  };

  return (
    <div className="modal-overlay animate-fade-in z-50">
      <div className="modal-panel w-full max-w-md">
        <div className="modal-header flex items-center justify-between">
          <h2 className="modal-title flex items-center gap-2">
            <ShieldCheck size={18} />
            Enable Two-Factor
          </h2>
          <button onClick={onClose} className="text-(--base-06) hover:text-(--error-light)"><X size={18} /></button>
        </div>

        <div className="modal-body">
          {error && (
            <div className="alert alert-error mb-3">
              {error}
            </div>
          )}

          {step === 'loading' && (
            <p className="text-sm text-(--base-06) py-8 text-center">Generating secret…</p>
          )}

          {step === 'scan' && (
            <form onSubmit={handleVerify} className="space-y-4">
              <p className="text-sm text-(--base-07)">
                Scan this QR with your authenticator app (Google Authenticator, Authy, 1Password etc.) and enter the 6-digit code shown to confirm.
              </p>
              <div className="flex justify-center bg-white p-4 rounded-md">
                <QRCodeSVG value={otpURL} size={180} />
              </div>
              <div className="flex flex-col gap-[5px]">
                <label className="input-label">Or enter this secret manually</label>
                <code className="input-mono w-full bg-(--base-02) border border-(--base-03) rounded-md px-3 py-2 text-xs text-(--base-08) break-all select-all">{secret}</code>
              </div>
              <div className="flex flex-col gap-[5px]">
                <label className="input-label">Verification code</label>
                <input
                  type="text"
                  value={code}
                  onChange={e => setCode(e.target.value)}
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  placeholder="123 456"
                  className="input-field input-mono w-full text-center tracking-widest"
                />
              </div>
              <button type="submit" disabled={busy || code.replace(/\s/g, '').length < 6} className="btn btn-primary w-full">
                {busy ? 'Verifying…' : 'Verify & Enable'}
              </button>
            </form>
          )}

          {step === 'backup' && (
            <div className="space-y-4">
              <div className="alert alert-warning text-(--warning) text-xs">
                <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                <span>
                  Store these backup codes somewhere safe. Each code works exactly once and lets you log in if you lose your authenticator. They will never be shown again.
                </span>
              </div>
              <div className="grid grid-cols-2 gap-2">
                {backupCodes.map(c => (
                  <code key={c} className="bg-(--base-02) border border-(--base-03) rounded-md px-2.5 py-1.5 text-xs font-mono text-(--base-09) text-center select-all">
                    {c}
                  </code>
                ))}
              </div>
              <button type="button" onClick={copyAll} className="btn btn-secondary btn-sm w-full">
                {copied ? <><Check size={12} /> Copied</> : <><Copy size={12} /> Copy all codes</>}
              </button>
              <label className="flex items-start gap-2 text-xs text-(--base-07) cursor-pointer pt-1">
                <input type="checkbox" checked={acknowledged} onChange={e => setAcknowledged(e.target.checked)} className="mt-0.5" />
                <span>I have saved these codes in a secure location.</span>
              </label>
              <button
                type="button"
                onClick={onComplete}
                disabled={!acknowledged}
                className="btn btn-primary w-full disabled:opacity-40 disabled:cursor-not-allowed"
              >
                Done
              </button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function DisableWizard({ onClose, onComplete }: { onClose: () => void; onComplete: () => void }) {
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      const res = await disableTOTP(password, code.replace(/\s/g, ''));
      if (res?.success) {
        onComplete();
      } else {
        setError(res?.message || 'Failed to disable 2FA');
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="modal-overlay animate-fade-in z-50">
      <div className="modal-panel w-full max-w-md">
        <div className="modal-header flex items-center justify-between">
          <h2 className="modal-title flex items-center gap-2">
            <ShieldOff size={18} className="text-(--error-light)" />
            Disable Two-Factor
          </h2>
          <button onClick={onClose} className="text-(--base-06) hover:text-(--error-light)"><X size={18} /></button>
        </div>

        <div className="modal-body space-y-4">
          {error && (
            <div className="alert alert-error">
              {error}
            </div>
          )}
          <p className="text-sm text-(--base-07)">
            Verify your password and provide a current authenticator code (or backup code) to disable 2FA.
          </p>
          <form onSubmit={handleSubmit} className="space-y-3">
            <div className="flex flex-col gap-[5px]">
              <label className="input-label">Password</label>
              <input type="password" value={password} onChange={e => setPassword(e.target.value)} className="input-field w-full" required />
            </div>
            <div className="flex flex-col gap-[5px]">
              <label className="input-label">Authenticator code or backup code</label>
              <input
                type="text"
                value={code}
                onChange={e => setCode(e.target.value)}
                inputMode="text"
                placeholder="123 456"
                className="input-field input-mono w-full text-center tracking-widest"
                required
              />
            </div>
            <button type="submit" disabled={busy} className="btn btn-danger w-full">
              {busy ? 'Disabling…' : 'Disable 2FA'}
            </button>
          </form>
        </div>
      </div>
    </div>
  );
}

export default ProfilePopup;
