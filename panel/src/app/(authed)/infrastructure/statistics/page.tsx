"use client";

import StatisticsPanel from '@/views/infrastructure/StatisticsPanel';

/**
 * No TabGuard here, deliberately.
 *
 * The other gated tabs guard on something the panel already knows (the gateway
 * is off, so there is nothing to show). Whether statistics are available is a
 * SERVER fact - the feature flag and whether the metrics database opened - and
 * the panel would have to guess at it. The endpoint answers with the reason,
 * and StatisticsPanel renders that reason, so there is one source of truth
 * instead of a client-side copy that can disagree with it.
 */
export default function Page() {
    return <StatisticsPanel />;
}
