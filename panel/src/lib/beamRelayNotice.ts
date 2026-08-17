/**
 * Whether the "Beam relay address not set" warning should fire.
 *
 * Pure because getting it wrong is invisible in the good case and permanent in
 * the bad one: the banner sat at the top of the panel forever on installs that
 * had a perfectly good relay.
 *
 * Two independent reasons it was wrong:
 *
 *  1. It read `manualOverride ?? relayAddress`. `??` only falls through on
 *     null/undefined, and Core's ManualOverride field carries no `omitempty`, so
 *     it is ALWAYS present — as "" when no override is set. The fallback was
 *     therefore unreachable, and every install on a DISCOVERED relay was told it
 *     had none. `relayAddress` alone is the right field anyway: Core's
 *     resolveRelay already lets a manual override win over discovery, so it is
 *     the effective answer.
 *
 *  2. It ignored the gateway. A Beam relay only exists inside the gateway
 *     subsystem; with routing on ip_port there is no relay to configure and
 *     nothing is broken, so warning about a missing one is noise about a feature
 *     the install does not run.
 */
export interface BeamRelayInput {
    /** Beam itself is switched on. */
    enabled: boolean;
    /** The EFFECTIVE relay address Core resolved (discovery or manual). */
    relayAddress?: string | null;
    /** The gateway is actually routing (routing_mode gateway|both). */
    gatewayEnabled: boolean;
}

export function shouldWarnBeamRelayMissing(i: BeamRelayInput): boolean {
    if (!i.gatewayEnabled) return false;
    if (!i.enabled) return false;
    return (i.relayAddress ?? '').trim() === '';
}
