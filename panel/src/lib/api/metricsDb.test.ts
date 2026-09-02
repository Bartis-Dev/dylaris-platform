import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import {
    metricsDBIncomplete,
    metricsDBModeSummary,
    emptyMetricsDBTarget,
    type MetricsDBTarget,
} from './metricsDb';

const separate = (over: Partial<MetricsDBTarget> = {}): MetricsDBTarget => ({
    ...emptyMetricsDBTarget,
    mode: 'separate',
    host: 'metricsdb',
    dbName: 'dylaris_metrics',
    user: 'metrics',
    ...over,
});

describe('what the statistics-database form will accept', () => {
    // The Core option is the default and needs nothing. A fresh install has to
    // be valid without anyone filling in a form.
    it('the Core database is always complete', () => {
        expect(metricsDBIncomplete(emptyMetricsDBTarget)).toBeNull();
        // Even with leftovers from a separate target that was abandoned.
        expect(metricsDBIncomplete({ ...separate({ host: '' }), mode: 'core' })).toBeNull();
    });

    // A password is optional, and this is not a lenience: the reference
    // deployment runs its metrics database with none at all, reachable only
    // from Core on a two-member network. Requiring one would make the
    // documented setup impossible to enter here.
    it('a separate database is complete without a password', () => {
        expect(metricsDBIncomplete(separate({ password: '' }))).toBeNull();
    });

    it('names the field that is missing, so the message can point at it', () => {
        expect(metricsDBIncomplete(separate({ host: '   ' }))).toMatch(/host/i);
        expect(metricsDBIncomplete(separate({ dbName: '' }))).toMatch(/database name/i);
        expect(metricsDBIncomplete(separate({ user: '' }))).toMatch(/user/i);
        expect(metricsDBIncomplete(separate({ port: 'https' }))).toMatch(/port/i);
        expect(metricsDBIncomplete(separate({ port: '0' }))).toMatch(/port/i);
        expect(metricsDBIncomplete(separate({ port: '70000' }))).toMatch(/port/i);
    });
});

describe('what each mode promises', () => {
    // The resolution follows from WHICH database is used, never from what is
    // installed in it. A summary that said "TimescaleDB found" without saying
    // "still hour buckets" would have an operator install an extension to get
    // minutes they will not get.
    it('the Core database says hour buckets whether or not TimescaleDB is there', () => {
        for (const ts of [true, false]) {
            const msg = metricsDBModeSummary('core', ts).toLowerCase();
            expect(msg).toContain('hour');
            expect(msg).not.toContain('minute');
        }
    });

    it('the separate database says minute buckets and what it needs', () => {
        const msg = metricsDBModeSummary('separate', false);
        expect(msg.toLowerCase()).toContain('minute');
        expect(msg).toContain('TimescaleDB');
    });
});

describe('the card that renders it', () => {
    const card = readFileSync(
        join(__dirname, '..', '..', 'components', 'settings', 'MetricsDatabaseCard.tsx'),
        'utf8',
    );

    // Every field edit has to clear the last test result. A green "connected"
    // banner sitting above a host that has since been retyped is a claim about
    // a connection nobody ever made.
    it('editing a field clears the previous test result', () => {
        expect(card).toMatch(/const set = \([^)]*\) => \{\s*\n\s*setTest\(null\);/);
    });

    // The form uses the shared hook rather than its own snapshot ref. Hand-rolled
    // copies of that lifecycle are what left every feature switch on this page
    // unsavable - see featuresTabSnapshots.test.ts.
    it('uses the shared settings-form lifecycle', () => {
        expect(card).toContain('useSettingsForm');
        expect(card).not.toMatch(/useRef<[^>]*\| null>\(null\)/);
    });

    // With METRICS_DB_URL set the panel is not the authority, so there must be
    // no save bar to press - not merely disabled inputs.
    it('an environment-managed target gets no save bar', () => {
        expect(card).toMatch(/form=\{locked \? undefined : form\}/);
    });

    // Three severities. "Connected, but no TimescaleDB" is neither a pass nor a
    // failure, and a two-state banner would have to call it one of them.
    it('renders warning as its own severity, not as success or error', () => {
        for (const sev of ['ok:', 'warning:', 'error:']) {
            expect(card).toContain(sev);
        }
        expect(card).toContain('--warning-border');
    });
});

describe('one setting, one writer', () => {
    const read = (rel: string) => readFileSync(join(__dirname, '..', '..', rel), 'utf8');

    // The recording switch used to live in the feature-flag bundle while the
    // database lived here. Two endpoints writing feature_metrics_enabled means
    // whichever saved last wins, and the feature card would have reverted a
    // change made on this one from its own stale copy. It is now written in
    // exactly one place, together with the target it needs.
    it('the feature-flag bundle no longer carries the metrics switch', () => {
        const flags = read('lib/api/featureFlags.ts');
        expect(flags).not.toMatch(/^\s*metrics:\s*boolean;/m);
    });

    it('the features tab renders no statistics switch of its own', () => {
        const tab = read('components/settings/FeaturesTab.tsx');
        expect(tab).not.toContain("editPlatformFlag('metrics'");
        expect(tab).not.toContain('platformFlags.metrics');
    });

    // And it is here, in the same form as the target, so one save commits both.
    it('the statistics card owns the switch', () => {
        const card = read('components/settings/MetricsDatabaseCard.tsx');
        expect(card).toContain("set({ enabled: v })");
        expect(card).toContain('SwitchRow');
    });
});
