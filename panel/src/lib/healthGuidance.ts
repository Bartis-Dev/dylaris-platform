// Operator guidance for a classified health failure.
//
// Kept out of the component so it can be unit-tested: the panel has no
// component-testing setup, and the thing worth pinning here is the mapping, not
// the markup. The slugs come from core/database/rediserror.go
// (RedisFailure.Slug) and are part of the API contract - a rename on either
// side silently drops the guidance back to nothing.

export interface HealthGuidance {
    /** What the operator should do about it. */
    advice: string;
    /**
     * Whether waiting can fix this. False means a human has to act, which is
     * also why the failure is not allowed to gate the container healthcheck -
     * restarting Core cannot repair a credential or an ACL rule.
     */
    selfHealing: boolean;
}

const REDIS_GUIDANCE: Record<string, HealthGuidance> = {
    unreachable: {
        advice:
            'Core cannot open a connection to Redis. Check that the Redis container is running and reachable at REDIS_ADDR. Core keeps retrying and recovers on its own once it is back.',
        selfHealing: true,
    },
    auth: {
        // Redis answers WRONGPASS identically for a wrong password, an unknown
        // username and a disabled user, and does not say which. Naming only one
        // would send an operator looking in the wrong place.
        advice:
            'Redis rejected the credentials. Check REDIS_USER and REDIS_PASSWORD against the user in the Redis ACL file. Redis reports the same error whether the password is wrong, the user does not exist, or the user is disabled, so check all three. This does not clear by itself.',
        selfHealing: false,
    },
    permission: {
        advice:
            'The connection and the password are fine, but the Redis ACL user is not allowed to run the command named in the message below. Grant it in the ACL file and reload it. This does not clear by itself.',
        selfHealing: false,
    },
    server_error: {
        advice:
            'Redis is reachable and answered, but with an error. The server message below is the place to start - a server still loading its dataset reports itself this way, for example.',
        selfHealing: true,
    },
};

const GUIDANCE_BY_COMPONENT: Record<string, Record<string, HealthGuidance>> = {
    redis: REDIS_GUIDANCE,
};

/**
 * Returns the guidance for a component's failure class, or null when there is
 * none to give. Null is the normal case: most components report no cause at
 * all, and an unknown cause from a newer Core must degrade quietly to the
 * plain detail-and-reason card rather than render a blank block.
 */
export function healthGuidance(componentKey: string, cause?: string): HealthGuidance | null {
    if (!cause) return null;
    const forComponent = GUIDANCE_BY_COMPONENT[componentKey];
    if (!forComponent) return null;
    return forComponent[cause] ?? null;
}
