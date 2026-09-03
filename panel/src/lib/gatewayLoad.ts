// CPU and RAM history for the gateway components, from the long-term record.
//
// The live numbers come from the Redis mirror, which holds one instant and
// nothing else - it cannot draw a line. The lines come from `metric_samples`,
// where the collector writes a one-minute bucket per component. Until
// 2026-09-03 only edges were in there: CPU and RAM are typed fields on the
// telemetry record rather than entries in its gauges map, so the loop that
// turns a component's numbers into series walked past them.

import type { MetricSeries, SeriesRequest } from './api/metrics';

/**
 * The ranges the switcher offers, and the bucket each one asks for.
 *
 * A minute is the floor: that is the width the recorder writes, so a shorter
 * window is not a finer chart, it is fewer points. One minute of history would
 * be a single point, which is why the shortest range here is 15 minutes and the
 * "right now" question is answered by the number next to the bar instead.
 *
 * The steps aim at roughly a hundred points per line. The server would pick one
 * on its own (it widens until a request fits under 2000 points), but 24 hours
 * across a dozen lines would then arrive as 17000 points to draw six sparklines
 * with.
 */
export const LOAD_STEP_SECONDS: Record<string, number> = {
    '15m': 60,
    '1h': 60,
    '6h': 300,
    '24h': 900,
};

export const LOAD_RANGES = ['15m', '1h', '6h', '24h'] as const;
export type LoadRange = (typeof LOAD_RANGES)[number];

/** The six series behind the graphs: CPU and RAM for each kind. */
export const LOAD_METRICS = [
    'edge.cpu_pct', 'edge.ram_pct',
    'warp.cpu_pct', 'warp.ram_pct',
    'beam.cpu_pct', 'beam.ram_pct',
];

/** One request for every line, split per component so each machine is its own. */
export function loadSeriesRequest(range: LoadRange): SeriesRequest {
    return {
        metrics: LOAD_METRICS,
        range,
        step: LOAD_STEP_SECONDS[range],
        split: true,
    };
}

/** A component's two lines, keyed `<kind>:<id>` - the key each row builds. */
export type LoadHistory = Map<string, { cpu: number[]; ram: number[] }>;

/**
 * Fold the flat series list into one entry per component.
 *
 * `avg` and not `max`: these are gauges, so the average of the samples in a
 * bucket is what the bucket measured. A peak would be the honest choice for a
 * capacity question about a shared link, but a CPU that touches 100% for one
 * sample is an ordinary second on a busy machine, and drawing that as the
 * minute would make every line an alarm.
 *
 * A series whose subject is empty is dropped rather than folded into some
 * component's line: without `split` the API returns one folded series per
 * metric, and silently attributing that to a machine would be worse than
 * showing no line at all.
 */
export function indexLoadSeries(series: MetricSeries[] | undefined): LoadHistory {
    const out: LoadHistory = new Map();
    for (const s of series ?? []) {
        const [kind, field] = s.metric.split('.');
        if (!s.subject || (field !== 'cpu_pct' && field !== 'ram_pct')) continue;
        const key = `${kind}:${s.subject}`;
        const entry = out.get(key) ?? { cpu: [], ram: [] };
        const values = s.points.map(p => p.avg);
        if (field === 'cpu_pct') entry.cpu = values;
        else entry.ram = values;
        out.set(key, entry);
    }
    return out;
}
