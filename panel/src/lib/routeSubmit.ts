import type { CreateRouteRequest } from '@/lib/api/types';

/**
 * The domain half of a route-create request, for either a new route or an edit.
 *
 * Editing is posting the SAME domain again, which is the only overwrite Core
 * permits on a route you own. That makes the branch here load-bearing rather
 * than cosmetic: carrying the picker's value into a save would post whatever
 * the form last held, leave the original route untouched, and spend a second
 * address out of the tenant's allowance to do it.
 *
 * `editing` is the domain being edited, or null while creating.
 */
export function routeSubmitRequest(
    editing: string | null,
    picked: CreateRouteRequest,
    targetPort: number,
): CreateRouteRequest {
    if (editing) {
        // Only the domain and the port. Subdomain / hosterDomain / customDomain
        // are how the picker BUILDS a domain, and Core resolves them ahead of
        // the plain one - so leaving them in would let a stale picker value
        // decide which route gets rewritten.
        return { domain: editing, targetPort };
    }
    return { ...picked, targetPort };
}
