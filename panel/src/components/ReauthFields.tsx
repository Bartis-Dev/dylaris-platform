"use client";

// The password (and, when the account has it, a second factor) asked for again
// before an action that outlives the session doing it.
//
// Core requires this on creating an API key and on setting security questions:
// a key authenticates by its own hash and keeps working after the password
// change that killed every session, and security questions ARE the reset path,
// so overwriting them survives even revoking every key.
//
// Shared rather than copied because of ONE rule - the code is asked for only
// when the account has 2FA - which has to match what Core does. Asking a user
// without 2FA for a code they cannot produce would block them entirely; not
// asking a user who has it would leave the second factor decorative on exactly
// the actions it is here for.

interface ReauthFieldsProps {
    twoFactorEnabled: boolean;
    password: string;
    code: string;
    onPassword: (value: string) => void;
    onCode: (value: string) => void;
    disabled?: boolean;
    idPrefix: string;
}

// reauthReady reports whether the fields hold enough to be worth sending. Six
// characters covers a TOTP code; a backup code is longer and passes the same
// test.
export function reauthReady(twoFactorEnabled: boolean, password: string, code: string): boolean {
    if (!password) return false;
    if (!twoFactorEnabled) return true;
    return code.replace(/\s/g, '').length >= 6;
}

export function ReauthFields({
    twoFactorEnabled, password, code, onPassword, onCode, disabled, idPrefix,
}: ReauthFieldsProps) {
    return (
        <div className="space-y-3 border-t border-(--base-03) pt-3">
            <p className="text-xs text-(--base-06)">
                {twoFactorEnabled
                    ? 'Confirm it is you. This grants access that outlives your current sign-in.'
                    : 'Confirm your password. This grants access that outlives your current sign-in.'}
            </p>
            <div>
                <label htmlFor={`${idPrefix}-password`} className="input-label">Your password</label>
                <input
                    id={`${idPrefix}-password`}
                    type="password"
                    autoComplete="current-password"
                    value={password}
                    onChange={e => onPassword(e.target.value)}
                    disabled={disabled}
                    className="input-field w-full disabled:opacity-40 disabled:cursor-not-allowed"
                />
            </div>
            {twoFactorEnabled && (
                <div>
                    <label htmlFor={`${idPrefix}-code`} className="input-label">
                        Authenticator code
                    </label>
                    <input
                        id={`${idPrefix}-code`}
                        inputMode="text"
                        autoComplete="one-time-code"
                        value={code}
                        onChange={e => onCode(e.target.value)}
                        disabled={disabled}
                        placeholder="6-digit code, or a backup code"
                        className="input-field w-full font-mono disabled:opacity-40 disabled:cursor-not-allowed"
                    />
                </div>
            )}
        </div>
    );
}
