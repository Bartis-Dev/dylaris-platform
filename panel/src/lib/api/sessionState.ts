/**
 * Whether a session exists, without the panel ever holding the credential.
 *
 * The session is an HttpOnly cookie now, so this code CANNOT read it - that is
 * the whole point, and it is also the problem this file solves. The panel still
 * needs to answer "am I signed in?" before its first API call, or every page
 * load flashes the login screen on the way to the dashboard.
 *
 * Core sets a second, deliberately readable cookie beside the session that
 * carries nothing but the fact that one exists. Forging it grants nothing: the
 * API still refuses without the real cookie, and the only thing a forged flag
 * buys is a rendered page whose data requests then 401 and bounce to /login.
 *
 * Treat this as a HINT, never as authorization. The server decides.
 */

const HINT_COOKIE = 'dylaris_signed_in';

/** True when Core's readable companion cookie is present. */
export function hasSession(): boolean {
    if (typeof document === 'undefined') return false;
    return document.cookie
        .split(';')
        .some(c => c.trim().startsWith(`${HINT_COOKIE}=`));
}

/**
 * Drops the hint locally so the UI switches to signed-out immediately.
 *
 * The real session cookie is HttpOnly and can only be cleared by Core
 * (POST /api/auth/logout). This is the optimistic half: it stops the panel
 * rendering an authenticated shell for the moment between the click and the
 * navigation. If the server call fails, the next request 401s and lands the
 * user on /login anyway.
 */
export function forgetSessionHint(): void {
    if (typeof document === 'undefined') return;
    document.cookie = `${HINT_COOKIE}=; Path=/; Max-Age=0; SameSite=Lax`;
}

/**
 * Clears the tokens older builds left in localStorage.
 *
 * Anyone upgrading has a JWT sitting there from before the session moved into a
 * cookie. It is dead weight and it is exactly the thing this change exists to
 * get rid of, so it is deleted on the next load rather than left to expire
 * quietly in storage an XSS could read.
 *
 * Safe to call on every boot: removeItem on a missing key does nothing.
 */
export function purgeLegacyTokens(): void {
    if (typeof window === 'undefined') return;
    try {
        localStorage.removeItem('token');
        localStorage.removeItem('authToken');
    } catch {
        // Storage can throw in sandboxed contexts. Nothing here is required for
        // the app to work - the cookie is the session.
    }
}
