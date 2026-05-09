import { API_URL, getAuthHeader, handleResponse, handleError } from './core';

export const login = async (username: string, password: string, totpCode?: string) => {
  try {
    const body: Record<string, string> = { username, password };
    if (totpCode) body.totpCode = totpCode;
    const res = await fetch(`${API_URL}/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });

    // 401 with requires2FA flag is the "password OK, give me the TOTP code" path
    if (res.status === 401) {
      const data = await res.json().catch(() => ({}));
      if (data.requires2FA) return { success: false, requires2FA: true, message: data.message };
    }

    const data = await handleResponse(res);

    // Store the token under BOTH keys ("token" and "authToken") so neither
    // older nor newer code paths break when reading.
    if (data.success && data.token) {
      if (typeof window !== 'undefined') {
        localStorage.setItem('token', data.token);
        localStorage.setItem('authToken', data.token);
      }
    }

    return data;
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

export const adminResetTOTP = async (userId: number) => {
  const res = await fetch(`${API_URL}/users/${userId}/2fa`, {
    method: 'DELETE',
    headers: getAuthHeader(),
  });
  return handleResponse(res);
};

export const getProfile = async () => {
  try {
    const res = await fetch(`${API_URL}/auth/profile`, {
      method: 'GET',
      headers: getAuthHeader(),
    });
    const data = await handleResponse(res);
    return data.success ? data.user : null;
  } catch (err) {
    return null;
  }
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

export const getAuthStatus = async () => {
  try {
    const res = await fetch(`${API_URL}/status`, {
      method: 'GET',
    });
    return await handleResponse(res);
  } catch (err) {
    return handleError(err);
  }
};

export const logout = () => {
  if (typeof window !== 'undefined') {
    // FIX: Also remove both keys on logout for safety
    localStorage.removeItem('token');
    localStorage.removeItem('authToken');
  }
};