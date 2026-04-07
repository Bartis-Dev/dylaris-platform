import { API_URL, getAuthHeader, handleResponse, handleError } from './core';

export const login = async (username: string, password: string) => {
  try {
    const res = await fetch(`${API_URL}/auth/login`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ username, password }),
    });
    
    const data = await handleResponse(res);
    
    // ULTIMATE FIX: We store the token under BOTH keys.
    // This way it doesn't matter whether the UI looks for "token" or "authToken".
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