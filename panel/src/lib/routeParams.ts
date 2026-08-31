'use client';

import { usePathname } from 'next/navigation';
import { EXPORT_PARAM } from '@/lib/exportParam';

/**
 * The id in the address bar, for a route Next exported under a placeholder.
 *
 * WHY THIS EXISTS, because `useParams()` is the obvious thing and it is wrong
 * here: the panel ships as a static export, so every dynamic route is
 * prerendered ONCE under the literal EXPORT_PARAM and Core serves that one file
 * for every id. Next treats the placeholder as a concrete value rather than a
 * wildcard, so `useParams()` hands back `{ id: "__param__" }` no matter what the
 * URL says - on a hard load and on a client-side navigation alike.
 *
 * Measured, not assumed: the served payload carries
 * `"params":{"id":"__param__"}` while `usePathname()` carries `/servers/1`. The
 * address bar is right; only the router's idea of the params is not. So this
 * reads the address bar.
 *
 * Addressed by the COLLECTION rather than by an index, so each call site says
 * what it means and cannot drift when a route gains a segment:
 *
 *   /servers/1            useRouteId('servers')  -> "1"
 *   /servers/1/t/5        useRouteId('t')        -> "5"
 *   /modpacks/7/builds/9  useRouteId('builds')   -> "9"
 *
 * Returns "" when the collection is not in the path, which every caller already
 * handles - it is the same shape a missing param had before.
 */
export function useRouteId(collection: string): string {
    const pathname = usePathname() || '';
    return routeIdFrom(pathname, collection);
}

/**
 * The pure half, so the mapping can be tested without a router.
 *
 * A placeholder value is never returned: if the segment after the collection is
 * still EXPORT_PARAM, the URL itself is the prerendered one (someone navigated
 * to it by hand) and there is no id to report.
 */
export function routeIdFrom(pathname: string, collection: string): string {
    const segs = pathname.split('/').filter(Boolean);
    const i = segs.indexOf(collection);
    if (i < 0 || i + 1 >= segs.length) return '';
    const id = segs[i + 1];
    return id === EXPORT_PARAM ? '' : decodeURIComponent(id);
}
