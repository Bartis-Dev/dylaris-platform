import { API_URL, getAuthHeader, handleResponse, handleError } from './core';

// One bullet in a release. `services` is empty for entries that concern nobody's
// deployment - a route-only customer runs nothing, so their entries name no
// component and are purely informational.
export interface ReleaseEntry {
    text: string;
    services?: string[] | null;
}

// A mandatory update declared by a release.
export interface ReleaseRequired {
    deadline: string;
    immediate: boolean;
    note?: string;
}

// One release block. All four categories are always present, including empty
// ones: "no security fixes this time" and "nobody filled this in" must not look
// the same.
export interface Release {
    version: string;
    required?: ReleaseRequired | null;
    features: ReleaseEntry[] | null;
    breaking: ReleaseEntry[] | null;
    security: ReleaseEntry[] | null;
    fixes: ReleaseEntry[] | null;
}

// One running copy of a component. An EMPTY version means "not reporting" - an
// image built before release stamping, or a node that has not checked in - and
// is never outdated, because an unknown version cannot be ordered.
export interface UpdateInstance {
    label: string;
    version?: string;
    outdated: boolean;
}

// One component's standing. `latest` is the newest release that NAMES this
// component, which is why a release touching only the node does not mark Core
// as behind.
export interface UpdateComponent {
    // The ROW's identity, which is not always the service: the operator's
    // cluster and their external machines are two rows and one component,
    // because a release names `node` and both of them install it.
    key: string;
    // What the row is called. Empty falls back to the panel's own name for the
    // service, which is what every non-node row does.
    label?: string;
    service: string;
    latest?: string;
    outdated: boolean;
    // Whether `instances` is the WHOLE set. True for Cores and nodes, which
    // announce themselves to Core; false for the panel, which is a static
    // bundle in a browser and can only report the copy that served this
    // request. A fraction is a claim about completeness, so it is only shown
    // where that claim holds.
    countable: boolean;
    instances: UpdateInstance[];
}

// A mandatory update this reader is subject to. `passed` means the deadline is
// already behind us, so the component is being refused rather than warned.
export interface UpdateRequirement {
    service: string;
    minVersion: string;
    deadline: string;
    passed: boolean;
    note?: string;
}

export interface UpdatesResponse {
    success: boolean;
    feed: 'platform' | 'hosted';
    latest?: string;
    seen?: string;
    components: UpdateComponent[];
    releases: Release[];
    required?: UpdateRequirement[] | null;
}

// No panel version is sent any more, and there is none to send: the bundle is
// compiled into Core's binary, so its version IS Core's. It used to be stamped
// in by CI and passed as ?panelVersion= so Core could say whether the panel
// specifically was behind - a question that can no longer have a different
// answer from "is Core behind".

// getUpdates - available to every signed-in user, not just admins. An admin gets
// the platform notes and every component; everyone else gets the customer notes
// and the nodes they own. Fails soft on the server: unreachable release notes
// fall back to the copy embedded in the build, which reads as "up to date".
export async function getUpdates(): Promise<{ success: boolean; message?: string } & Partial<UpdatesResponse>> {
    try {
        const res = await fetch(`${API_URL}/updates`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// markUpdatesSeen - acknowledge everything published so far, clearing the badge.
// The version comes from the server, so a stale panel cannot acknowledge a
// release it never displayed.
export async function markUpdatesSeen() {
    try {
        const res = await fetch(`${API_URL}/me/updates-seen`, {
            method: 'PUT',
            headers: getAuthHeader(),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
