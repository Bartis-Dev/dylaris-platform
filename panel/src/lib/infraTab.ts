// Which half of "my infrastructure" to show.
//
// Extracted because this expression has been wrong twice: once honouring
// ?tab=routes on a platform with no gateway routing (opening a panel whose
// endpoints 409), and once stranding route-only customers on a platform with
// BYON off, where the page returned "your own hardware is turned off" with the
// routes tab unreachable behind it.

export type InfraTab = 'machines' | 'routes';

export interface InfraAvailability {
    /** feature_byon_enabled: this platform runs servers on tenant machines. */
    machines: boolean;
    /** Gateway routing is on, so protected addresses exist. */
    routes: boolean;
}

/**
 * Resolves the tab against what the PLATFORM has, never against the URL alone.
 * Entitlement is a separate question the panels answer for themselves - someone
 * unentitled still gets the tab, and an explanation inside it.
 *
 * Returns null when neither half exists: the caller has nothing to show and
 * says so instead of rendering an empty tab.
 */
export function resolveInfraTab(requested: string | null, have: InfraAvailability): InfraTab | null {
    if (!have.machines && !have.routes) return null;
    if (!have.machines) return 'routes';
    if (have.routes && requested === 'routes') return 'routes';
    return 'machines';
}

/** The bar only earns its space when there is a choice to make. */
export function showInfraTabBar(have: InfraAvailability): boolean {
    return have.machines && have.routes;
}
