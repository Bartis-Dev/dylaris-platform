export const API_URL = process.env.NEXT_PUBLIC_API_URL || "http://localhost:25500/api";

// Returns a plain object so callers can spread it into header objects:
//   { ...getAuthHeader(), 'Content-Type': 'application/json' }
// A Headers instance does not iterate as own properties — spreading it
// silently drops Authorization, which broke 2FA verify and similar flows.
export const getAuthHeader = (): Record<string, string> => {
  if (typeof window === 'undefined') return {};
  const token = localStorage.getItem("authToken") || localStorage.getItem("token");
  return token ? { Authorization: `Bearer ${token}` } : {};
};

// Error Handler Helper
export const handleResponse = async (response: Response) => {
    const data = await response.json();
    if (response.ok) return { success: true, ...data };
    return { success: false, message: data.message || 'Unknown error' };
};

export const handleError = (err: any) => {
    console.error("API Error:", err);
    return { success: false, message: 'Connection failed' };
};