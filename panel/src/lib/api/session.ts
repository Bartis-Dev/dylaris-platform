// Shared handling for an expired session. Both API paths (the core.ts
// handleResponse helpers and the legacy fetchAPI wrapper in types.ts) funnel
// their 401 responses through here so the behavior is identical.
//
// A 401 while a token is present means the session expired mid-use. We clear
// both token keys and hard-navigate to /login. The hard navigation (not
// router.push) is deliberate: unloading the document tears down every open
// setInterval poller and EventSource/SSE stream on the page, so none keep
// hammering the API with a dead token. A 401 WITHOUT a token is a failed
// login, not an expired session, so we leave it to the caller.
export function handleUnauthorized(response: Response): boolean {
  if (response.status !== 401 || typeof window === "undefined") return false;

  const hadToken = !!(localStorage.getItem("authToken") || localStorage.getItem("token"));
  if (!hadToken) return false;

  localStorage.removeItem("token");
  localStorage.removeItem("authToken");
  try {
    // Return the user where they were after re-login (login page reads this).
    sessionStorage.setItem("postLoginRedirect", window.location.pathname + window.location.search);
  } catch {
    // sessionStorage can throw in sandboxed/private contexts; the redirect below still works.
  }
  window.location.href = "/login";
  return true;
}
