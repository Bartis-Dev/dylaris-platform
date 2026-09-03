import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import {
    LOAD_RANGES,
    LOAD_METRICS,
    LOAD_STEP_SECONDS,
    loadSeriesRequest,
    indexLoadSeries,
} from './gatewayLoad';
import type { MetricSeries } from './api/metrics';

const point = (avg: number) => ({ t: '2026-09-03T10:00:00Z', min: avg, max: avg, sum: avg, count: 1, avg });

describe('loadSeriesRequest', () => {
    it('asks for every line in one request', () => {
        // The API caps a request at 24 metrics and splits per component on the
        // server, so this stays ONE round trip however many machines exist.
        const req = loadSeriesRequest('1h');
        expect(req.metrics).toEqual(LOAD_METRICS);
        expect(req.metrics.length).toBeLessThanOrEqual(24);
        expect(req.split).toBe(true);
    });

    it('names a step for every range it offers', () => {
        // Without a step the server picks one that merely fits under its own
        // 2000-point limit - 24 hours across a dozen lines would arrive as
        // seventeen thousand points to draw six sparklines with.
        for (const r of LOAD_RANGES) {
            expect(LOAD_STEP_SECONDS[r], `no step for ${r}`).toBeGreaterThan(0);
            expect(loadSeriesRequest(r).step).toBe(LOAD_STEP_SECONDS[r]);
        }
    });

    it('never asks for a bucket finer than the recorder writes', () => {
        // The recorder's floor is one minute. A finer step is not a finer
        // chart, it is the same points with gaps between them.
        for (const r of LOAD_RANGES) {
            expect(LOAD_STEP_SECONDS[r], `${r} asks for sub-minute buckets`).toBeGreaterThanOrEqual(60);
        }
    });

    it('starts at 15 minutes, because one minute is a single point', () => {
        expect(LOAD_RANGES[0]).toBe('15m');
    });
});

describe('indexLoadSeries', () => {
    const series: MetricSeries[] = [
        { metric: 'warp.cpu_pct', subject: 'w1', points: [point(10), point(12)] },
        { metric: 'warp.ram_pct', subject: 'w1', points: [point(30), point(31)] },
        { metric: 'beam.cpu_pct', subject: 'b1', points: [point(3)] },
    ];

    it('folds a component two metrics into one entry', () => {
        const got = indexLoadSeries(series);
        expect(got.get('warp:w1')).toEqual({ cpu: [10, 12], ram: [30, 31] });
    });

    it('keys by kind and id, which is what a row builds', () => {
        // The row looks its graph up by `${component}:${id}`. If the two sides
        // ever built the key differently every graph would silently be empty.
        const got = indexLoadSeries(series);
        expect([...got.keys()].sort()).toEqual(['beam:b1', 'warp:w1']);
    });

    it('leaves the missing half empty rather than inventing it', () => {
        // beam:b1 has CPU and no RAM in this window. An empty array draws no
        // line; a zero-filled one would draw a machine using no memory.
        expect(indexLoadSeries(series).get('beam:b1')).toEqual({ cpu: [3], ram: [] });
    });

    it('drops a series with no subject', () => {
        // Without `split` the API returns one folded series per metric.
        // Attributing that to a machine would be worse than showing no line.
        const folded: MetricSeries[] = [{ metric: 'warp.cpu_pct', points: [point(9)] }];
        expect(indexLoadSeries(folded).size).toBe(0);
    });

    it('ignores a metric that is not cpu or ram', () => {
        const other: MetricSeries[] = [{ metric: 'warp.peers', subject: 'w1', points: [point(7)] }];
        expect(indexLoadSeries(other).size).toBe(0);
    });

    it('survives no series at all', () => {
        expect(indexLoadSeries(undefined).size).toBe(0);
    });
});

/**
 * The panel asks Core for six metric names. Nothing fails if one of them is not
 * recorded: the query returns no rows, the chart draws nothing, and the screen
 * looks like a quiet machine rather than like a broken contract. That is the
 * "a reader is not evidence of a writer" shape, so the writer is checked here.
 */
describe('the metrics this screen asks for are actually produced', () => {
    const core = join(__dirname, '../../../core');
    const catalog = readFileSync(join(core, 'metrics/catalog.go'), 'utf8');
    const collector = readFileSync(join(core, 'services/metrics_collector.go'), 'utf8');

    it('every name is in the Go catalog', () => {
        for (const m of LOAD_METRICS) {
            expect(catalog, `${m} is not in metrics/catalog.go`).toContain(`"${m}"`);
        }
    });

    it('a recording site exists for each half', () => {
        // Edges are recorded by name from the edge list; warp and beam are
        // recorded by the component loop, which builds the name from the
        // component - so there is no literal "warp.cpu_pct" to grep for.
        expect(collector).toContain('"edge.cpu_pct"');
        expect(collector).toContain('"edge.ram_pct"');
        // Matched loosely on purpose: pinning the exact spacing would make
        // this fail the next time gofmt has an opinion about it.
        expect(collector, 'nothing records CPU for the non-edge components')
            .toMatch(/gs\.Component\s*\+\s*"\.cpu_pct"/);
        expect(collector, 'nothing records RAM for the non-edge components')
            .toMatch(/gs\.Component\s*\+\s*"\.ram_pct"/);
    });
});
