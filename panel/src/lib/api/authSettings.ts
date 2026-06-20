import { API_URL, getAuthHeader, handleResponse, handleError } from './core';

export interface AuthPolicy {
    registrationEnabled: boolean;
    emailVerifyRequired: boolean;
    passwordMinLength: number;
    defaultNewUserAllRegions: boolean;
    // 2FA enforcement
    require2FAForAdmins: boolean;
    require2FAForAllUsers: boolean;
    // password reset link lifetime (minutes, 5–1440)
    passwordResetLinkTTLMinutes: number;
    // security questions
    securityQuestionsEnabled: boolean;
    securityQuestionsRequiredAtSignup: boolean;
    securityQuestionsRequiredAtReset: boolean;
    securityQuestionsCount: number;
    // auto-delete inactive users
    inactiveDeleteEnabled: boolean;
    inactiveDaysBeforeDelete: number;
    historyGraceExtraDays: number;
    deleteEmailWarningDays: number;
    deletionMode: 'anonymize' | 'hard_delete';
}

export interface SMTPConfig {
    host: string;
    port: number;
    username: string;
    /** Write-only — never present on GET responses. */
    password?: string;
    fromEmail: string;
    fromName: string;
    /** "none" | "starttls" | "tls" */
    encryption: string;
    /** GET-only flag: true when a password is already stored.
     *  Lets the UI show "leave blank to keep" placeholder. */
    passwordSet?: boolean;
}

export async function getAuthPolicy() {
    try {
        const res = await fetch(`${API_URL}/admin/settings/auth`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function saveAuthPolicy(policy: AuthPolicy) {
    try {
        const res = await fetch(`${API_URL}/admin/settings/auth`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(policy),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// Demo account: the single read-only user that sees the demo servers.
export async function getDemoAccount() {
    try {
        const res = await fetch(`${API_URL}/admin/settings/demo-account`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function setDemoAccount(username: string) {
    try {
        const res = await fetch(`${API_URL}/admin/settings/demo-account`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ username }),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function getSMTPConfig() {
    try {
        const res = await fetch(`${API_URL}/admin/settings/smtp`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function saveSMTPConfig(config: SMTPConfig) {
    try {
        const res = await fetch(`${API_URL}/admin/settings/smtp`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify(config),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

export async function testSendSMTP(to?: string) {
    try {
        const res = await fetch(`${API_URL}/admin/settings/smtp/test`, {
            method: 'POST',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ to: to || '' }),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
