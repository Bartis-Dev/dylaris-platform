import type { Entitlement } from '@/lib/api/entitlement';

/**
 * One sentence explaining WHY a tenant has the access they have.
 *
 * Separate from the component because the wording is the feature: "no" and
 * "no, because nobody has defined any plans yet" send an operator to completely
 * different places, and only one of them is a problem.
 */
export function entitlementExplanation(e: Entitlement): string {
    switch (e.source) {
        case 'suspended':
            return 'Suspended, so nothing is available until the status below is set back to active.';
        case 'unlimited':
            // Not a fallback: with no plans defined, the platform genuinely
            // allows everything, which is every self-host install today.
            return 'No plans are defined on this platform, so everything is available to everyone. Define plans to change that.';
        case 'grant':
            return 'From your manual grant only. It ends on its own when the period is over.';
        case 'plan+grant':
            return 'From their plan plus your manual grant.';
        case 'plan':
            return 'From their plan.';
        default:
            return 'Neither their plan nor a grant gives them anything.';
    }
}
