export const API_URL = process.env.NEXT_PUBLIC_API_URL || "http://localhost:25500/api";

export const getAuthHeader = () => {
  if (typeof window === 'undefined') return new Headers();
  
  const token = localStorage.getItem("authToken");
  const headers = new Headers();
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }
  return headers;
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