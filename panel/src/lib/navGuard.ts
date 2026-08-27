/**
 * Does clicking this link leave the page that is currently showing?
 *
 * The unsaved-changes guards need the answer, and only for one reason: a form
 * registers its dirty state while it is mounted, so navigating anywhere that
 * unmounts it takes the edits with it. The prompt is skipped only when the click
 * is a genuine no-op.
 *
 * "Same page" means the SAME path, not a related one. The guards used to also
 * treat `pathname.startsWith(href + '/')` as staying, which is the expression
 * that decides which nav item is highlighted - an ancestor link is highlighted
 * while you are on its child. Highlighting and leaving are different questions,
 * and borrowing the first to answer the second let two links in the shipped
 * navigation drop unsaved work without asking:
 *
 *   - the navbar's Settings button (/settings) while on any /settings/* screen,
 *     every one of which is a form;
 *   - a server in the sidebar (/servers/<id>) while on that same server's
 *     /config/properties, /config/display or /config/proxy - all three register
 *     unsaved changes, and all three are reached from a page the link points at.
 *
 * Both cases are a click on the item that is already lit up, which is exactly
 * when an operator expects nothing to happen.
 */
export function leavesPage(pathname: string, href: string): boolean {
    if (!href) return true;
    return href !== pathname;
}
