import { API_URL, GATE_TIMEOUT_MS, getAuthHeader, handleResponse, handleError } from './core';

export const login = async (username: string, password: string, totpCode?: string) => {
  try {
    const body: Record<string, string> = { username, password };
    if (totpCode) body.totpCode = totpCode;
    const res = await fetch(`${API_URL}/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });

    // Read the body EXACTLY ONCE. A Response body is a one-shot stream: this
    // used to be read here for the 2FA/verification flags and then again inside
    // handleResponse, where the second read throws "body stream already read".
    // handleResponse treats a throw as "the body was not JSON" and reports
    // `Request failed (401)` - so every wrong password showed a status code
    // instead of Core's actual message. Do not reintroduce a second read.
    //
    // handleResponse is deliberately not used at all here: its 401 branch
    // treats the status as an expired session, and a failed LOGIN never is one.
    const data: any = await res.json().catch(() => null);

    if (res.ok) {
      // Store the token under BOTH keys ("token" and "authToken") so neither
      // older nor newer code paths break when reading.
      if (data?.token && typeof window !== 'undefined') {
        localStorage.setItem('token', data.token);
        localStorage.setItem('authToken', data.token);
      }
      return { success: true, ...(data ?? {}) };
    }

    // 401 with requires2FA flag is the "password OK, give me the TOTP code" path
    if (res.status === 401 && data?.requires2FA) {
      return { success: false, requires2FA: true, message: data.message };
    }

    // 403 covers the policy-driven gates: unverified email or
    // missing 2FA-setup when required. Surface the flags so the
    // login form can route to the right next step.
    if (res.status === 403 && data?.requiresVerification) {
      return { success: false, requiresVerification: true, email: data.email, message: data.message };
    }
    if (res.status === 403 && data?.requires2FASetup) {
      return {
        success: false,
        requires2FASetup: true,
        setupToken: data.setupToken,
        setupTokenExpires: data.setupTokenExpires,
        message: data.message,
      };
    }

    // Keep the status only when there was no parsable body to quote.
    return { success: false, message: data?.message || `Request failed (${res.status})` };
  } catch (err) {
    return handleError(err);
  }
};

// --- 2FA / TOTP ---

export const setupTOTP = async () => {
  const res = await fetch(`${API_URL}/auth/2fa/setup`, {
    method: 'POST',
    headers: getAuthHeader(),
  });
  return handleResponse(res);
};

export const verifyTOTP = async (secret: string, code: string) => {
  const res = await fetch(`${API_URL}/auth/2fa/verify`, {
    method: 'POST',
    headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ secret, code }),
  });
  return handleResponse(res);
};

export const disableTOTP = async (password: string, code: string) => {
  const res = await fetch(`${API_URL}/auth/2fa/disable`, {
    method: 'POST',
    headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ password, code }),
  });
  return handleResponse(res);
};

export const adminResetTOTP = async (userId: string) => {
  const res = await fetch(`${API_URL}/users/${userId}/2fa`, {
    method: 'DELETE',
    headers: getAuthHeader(),
  });
  return handleResponse(res);
};

// regenerateBackupCodes — issues a fresh set of 10 single-use codes for an
// already-2FA-enabled user. Same defence-in-depth as disable: requires
// current password + a valid TOTP/backup code.
export const regenerateBackupCodes = async (password: string, code: string) => {
  const res = await fetch(`${API_URL}/auth/2fa/regenerate-backup-codes`, {
    method: 'POST',
    headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ password, code }),
  });
  return handleResponse(res);
};

export const get2FAStatus = async () => {
  const res = await fetch(`${API_URL}/auth/2fa/status`, {
    headers: getAuthHeader(),
  });
  return handleResponse(res);
};

// setupTOTPWithToken / verifyTOTPWithToken — variants of the regular setup
// flow that authenticate via an explicit Bearer token (the short-lived
// setup-token issued by the login endpoint when 2FA enrollment is forced).
// Necessary because at this point the user does NOT have a session JWT in
// localStorage yet — the regular getAuthHeader() would send nothing.
export const setupTOTPWithToken = async (setupToken: string) => {
  const res = await fetch(`${API_URL}/auth/2fa/setup`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${setupToken}` },
  });
  return handleResponse(res);
};

export const verifyTOTPWithToken = async (setupToken: string, secret: string, code: string) => {
  const res = await fetch(`${API_URL}/auth/2fa/verify`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${setupToken}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ secret, code }),
  });
  return handleResponse(res);
};

// Resolves to the user, or null when the session is genuinely not valid.
// REJECTS when the API could not be reached at all - and the difference
// matters: the only caller treats null as "log the user out".
//
// This used to catch everything and return null, so a Core that was down for a
// few seconds looked exactly like an expired token. Reproduced on the testbed:
// stopping Core and reloading the panel wiped BOTH localStorage tokens and
// dropped the user on /login, with a token that was still perfectly valid. A
// deploy logged out every open tab.
//
// The timeout is what makes the rejection reachable at all: a host that
// accepts the connection and never answers neither resolves nor rejects.
export const getProfile = async () => {
  const res = await fetch(`${API_URL}/auth/profile`, {
    method: 'GET',
    headers: getAuthHeader(),
    signal: AbortSignal.timeout(GATE_TIMEOUT_MS),
  });
  const data = await handleResponse(res);
  return data.success ? data.user : null;
};

export const updateProfile = async (data: any) => {
  try {
    const res = await fetch(`${API_URL}/auth/profile`, {
      method: 'PUT',
      headers: getAuthHeader(),
      body: JSON.stringify(data),
    });
    return await handleResponse(res);
  } catch (err) {
    return handleError(err);
  }
};

export const logout = () => {
  if (typeof window !== 'undefined') {
    // Remove both token keys: login writes 'token' and 'authToken'.
    localStorage.removeItem('token');
    localStorage.removeItem('authToken');
  }
};