import type { StoreAccountSummary } from '@/lib/api/store';

// Presentation rules for the store account card. Pure, so the ones that decide
// whether a switch can be moved and what an allowance bar says are testable
// without a storefront.

// usagePct is what the bar fills to.
//
// A ceiling of zero is NOT a full bar. It means the tenant may hold none of this
// at all, and drawing 100% would read as "you are at your limit" to somebody who
// never had one. Anything at or over the ceiling is 100 - a bar that keeps
// growing past its track is a bar that has stopped meaning anything.
export function usagePct(used: number, ceiling: number): number {
    if (!Number.isFinite(used) || !Number.isFinite(ceiling) || ceiling <= 0) return 0;
    return Math.max(0, Math.min(100, Math.round((used / ceiling) * 100)));
}

export type SwitchState =
    | { kind: 'on' }
    | { kind: 'off' }
    | { kind: 'unavailable'; reason: string };

// trafficSwitch and backupSwitch decide whether the tenant may move the control
// at all, and say why when they may not.
//
// Three different "no"s, and collapsing them is how somebody spends an afternoon
// on the wrong problem: nothing to bill against, no meter configured by the
// operator, or a subscription that is not billed through Stripe at all. Only the
// second is anybody's fault, and it is not the tenant's.
function billingSwitch(
    s: StoreAccountSummary,
    enabled: boolean | undefined,
    meterConfigured: boolean | undefined,
    what: string,
): SwitchState {
    if (!s.subscribed) {
        // A grant is not a missing subscription, and saying so was actively
        // misleading: a tenant whose BYON works read "there is no active
        // subscription" as though their access were broken. A grant made in the
        // panel writes Core's billing row and creates no store subscription at
        // all, so there is nothing to meter - and nothing sweeps them either,
        // because the guard only walks Stripe subscriptions.
        if (s.granted) {
            return {
                kind: 'unavailable',
                reason: `Your access was granted by an administrator rather than bought, so there is no subscription to meter ${what} against. Nothing is charged, and nothing is stopped for going over.`,
            };
        }
        return { kind: 'unavailable', reason: `There is no active subscription to bill ${what} against.` };
    }
    if (!s.stripe) {
        return {
            kind: 'unavailable',
            reason: 'This subscription was granted by an administrator rather than billed through the store, so there is nothing to meter.',
        };
    }
    if (!meterConfigured) {
        return {
            kind: 'unavailable',
            reason: `Metered ${what} is not set up on the store yet. Nothing you can do from here.`,
        };
    }
    return enabled ? { kind: 'on' } : { kind: 'off' };
}

export function trafficSwitch(s: StoreAccountSummary): SwitchState {
    return billingSwitch(s, s.trafficBillingEnabled, s.trafficMeterConfigured, 'traffic');
}

export function backupSwitch(s: StoreAccountSummary): SwitchState {
    return billingSwitch(s, s.backupBillingEnabled, s.backupMeterConfigured, 'backup storage');
}

// consequenceOf is the sentence under each switch: what happens when the
// allowance runs out, in the state the switch is currently in.
//
// Written as a consequence rather than as a setting name because that is the
// decision being made. "Metered billing: off" tells a tenant nothing; "you will
// be stopped rather than charged" tells them exactly what they are choosing.
export function consequenceOf(kind: 'traffic' | 'backup', on: boolean): string {
    if (kind === 'traffic') {
        return on
            ? 'Past your included traffic you keep running and the extra is billed by the terabyte.'
            : 'Past your included traffic your servers stop being reachable until the next period. Nothing is charged.';
    }
    return on
        ? 'Past your included backup storage new backups keep running and the extra is billed per block.'
        : 'Past your included backup storage new backups are refused. Nothing is charged, and nothing already stored is deleted.';
}
