"use client";

import { useEffect, useState } from 'react';
import { Cpu, MemoryStick, ArrowUpCircle } from 'lucide-react';
import { getGatewayBandwidthOverview } from '@/lib/api';
import { SpliceVersionSummary } from './InfraCards';
import { useInfra } from './context';
import { spliceImageMismatch } from '@/lib/spliceDrift';
import { formatBitsPerSec, type GatewayBandwidthOverview, type GatewayComponentView } from '@/lib/bandwidth';
import type { GatewayEdge } from '@/lib/api';

/**
 * What runs the gateway: edges, warp leaders and beam relays, one compact row
 * each, with what the machine costs.
 *
 * The three sections rather than one table, and this is the whole reason: each
 * kind has a number the others do not have. An edge carries PLAYERS and pins a
 * splice version, a warp leader holds PEERS, a beam relay has TRANSFERS in
 * flight. A single table would have to show all three columns for all three
 * kinds and leave two thirds of every row structurally blank.
 *
 * Two sources, deliberately. Edges come from the infrastructure overview, which
 * knows things the telemetry record does not - the address, the splice version,
 * whether the running splice is the pinned image. Warp and beam come from the
 * bandwidth mirror, which is the only place their live CPU and RAM exist.
 */

export default function GatewayPanel() {
    const { edges } = useInfra();
    const [overview, setOverview] = useState<GatewayBandwidthOverview | null>(null);

    useEffect(() => {
        const load = () => { getGatewayBandwidthOverview().then(setOverview).catch(() => { /* keep the last snapshot */ }); };
        load();
        const t = setInterval(load, 10000);
        return () => clearInterval(t);
    }, []);

    const of = (kind: string) => (overview?.components ?? []).filter(c => c.component === kind);
    const warp = of('warp');
    const beam = of('beam');

    return (
        <div className="flex flex-col gap-5">
            <SpliceVersionSummary edges={edges} />

            <Section
                title="Edges"
                count={edges.length}
                // Named rather than implied: an operator staring at an empty
                // section should not have to guess whether it is broken or idle.
                empty="No edges registered. Edges auto-discover through Redis once one is deployed."
            >
                {edges.map(e => <EdgeRow key={e.edge_id} edge={e} />)}
            </Section>

            <Section
                title="Warp leaders"
                count={warp.length}
                empty="No warp leader reporting. A leader appears here once it publishes telemetry."
            >
                {warp.map(c => (
                    <ComponentRow
                        key={c.id}
                        comp={c}
                        // Both numbers, because the gap between them IS the
                        // health of the overlay: a configured peer is a row,
                        // an active one is a tunnel that handshook recently.
                        extra={`${fmtGauge(c, 'peers_active')} / ${fmtGauge(c, 'peers')} peers up`}
                    />
                ))}
            </Section>

            <Section
                title="Beam relays"
                count={beam.length}
                empty="No beam relay reporting. A relay appears here once it publishes telemetry."
            >
                {beam.map(c => (
                    <ComponentRow key={c.id} comp={c} extra={`${fmtGauge(c, 'active_transfers')} transfers`} />
                ))}
            </Section>
        </div>
    );
}

// fmtGauge reads one of a component's own gauges. A gauge a component never
// published is shown as "-" and never as 0: "nothing connected" and "this build
// does not report it" are opposite claims, and a 0 states the alarming one.
function fmtGauge(c: GatewayComponentView, name: string): string {
    const v = c.gauges?.[name];
    return v === undefined ? '-' : String(Math.round(v));
}

function Section({ title, count, empty, children }: {
    title: string; count: number; empty: string; children: React.ReactNode;
}) {
    return (
        <section className="flex flex-col gap-2">
            <div className="flex items-center gap-2">
                <h3 className="text-sm font-semibold text-(--base-09)">{title}</h3>
                <span className="text-[10px] font-mono tabular-nums px-1.5 py-0.5 rounded-full bg-(--base-03) text-(--base-06)">{count}</span>
            </div>
            {count === 0 ? (
                <div className="card p-6 text-center text-(--base-06) text-sm">{empty}</div>
            ) : (
                <div className="card divide-y divide-(--base-03)">{children}</div>
            )}
        </section>
    );
}

/** A CPU or RAM reading: the number, with a thin bar behind it. */
function Meter({ icon, pct, label }: { icon: React.ReactNode; pct: number; label: string }) {
    return (
        <div className="flex items-center gap-1.5 w-24 shrink-0" title={`${label} ${pct.toFixed(0)}%`}>
            <span className="text-(--base-05)">{icon}</span>
            <div className="flex-1 h-1 rounded-full bg-(--base-03) overflow-hidden">
                <div
                    className="h-full rounded-full bg-(--accent) transition-all duration-500"
                    style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
                />
            </div>
            <span className="text-[11px] font-mono tabular-nums text-(--base-07) w-8 text-right">{pct.toFixed(0)}%</span>
        </div>
    );
}

function StatusDot({ online }: { online: boolean }) {
    return (
        <span
            className={`w-2 h-2 rounded-full shrink-0 ${online ? 'bg-(--success-light) shadow-[0_0_6px_var(--success-light)]' : 'bg-(--error)'}`}
            aria-label={online ? 'online' : 'offline'}
        />
    );
}

function Row({ children }: { children: React.ReactNode }) {
    return <div className="flex items-center gap-3 px-4 py-2.5 flex-wrap text-xs">{children}</div>;
}

function EdgeRow({ edge }: { edge: GatewayEdge }) {
    const online = edge.status === 'online';
    const s = edge.stats;
    const running = edge.splice_version || '';
    const latest = edge.splice_version_latest || '';
    // Behind: the pin names an older version than the edge image carries.
    const behind = !!latest && running !== latest;
    // A DIFFERENT fault, invisible to the version comparison: the running
    // splice is not the image the pin resolves to, at the same version string.
    const wrongImage = spliceImageMismatch(edge.splice_image_running || '', edge.splice_image_available || '');

    return (
        <Row>
            <StatusDot online={online} />
            <span className="font-medium text-(--base-09) truncate min-w-32">{edge.name}</span>
            <span className="font-mono text-(--base-05) truncate">{edge.ip}:{edge.service_port}</span>
            {edge.region && <span className="mono-label text-(--base-06)">{edge.region}</span>}
            <div className="flex items-center gap-3 ml-auto flex-wrap justify-end">
                {online && s ? (
                    <>
                        <Meter icon={<Cpu size={11} />} pct={s.cpu} label="CPU" />
                        <Meter icon={<MemoryStick size={11} />} pct={s.ram_pct} label="RAM" />
                        <span className="font-mono tabular-nums text-(--base-07) w-24 text-right">
                            {s.active_mc_streams} players
                        </span>
                        <span className="font-mono tabular-nums text-(--base-06) w-28 text-right">
                            tx {formatBitsPerSec(s.tx_speed * 8)}
                        </span>
                    </>
                ) : (
                    <span className="font-mono text-(--error-light)">offline</span>
                )}
                {behind ? (
                    <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-(--warning)/10 mono-label text-(--warning-light)">
                        <ArrowUpCircle size={9} /> splice {running ? `v${running}` : 'unknown'} &rarr; v{latest}
                    </span>
                ) : wrongImage ? (
                    <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-(--danger)/10 mono-label text-(--danger-light)">
                        <ArrowUpCircle size={9} /> splice v{running || latest} &middot; wrong image
                    </span>
                ) : (running || latest) ? (
                    <span className="font-mono text-(--base-06)">splice v{running || latest}</span>
                ) : null}
            </div>
        </Row>
    );
}

function ComponentRow({ comp, extra }: { comp: GatewayComponentView; extra: string }) {
    return (
        <Row>
            {/* Alive is always true here: a component has a mirror entry only
                while it is reporting, and the entry expires on its own. */}
            <StatusDot online={comp.alive} />
            <span className="font-medium text-(--base-09) truncate min-w-32">{comp.id}</span>
            <span className="font-mono text-(--base-05) truncate">{comp.host}</span>
            {comp.region && <span className="mono-label text-(--base-06)">{comp.region}</span>}
            <div className="flex items-center gap-3 ml-auto flex-wrap justify-end">
                <Meter icon={<Cpu size={11} />} pct={comp.cpuPct} label="CPU" />
                <Meter icon={<MemoryStick size={11} />} pct={comp.ramPct} label="RAM" />
                <span className="font-mono tabular-nums text-(--base-07) w-24 text-right">{extra}</span>
                <span className="font-mono tabular-nums text-(--base-06) w-28 text-right">
                    tx {formatBitsPerSec(comp.txBps)}
                </span>
                {comp.uptimeSec !== undefined && comp.uptimeSec > 0 && (
                    <span className="font-mono text-(--base-05) w-16 text-right" title="process uptime">
                        up {formatUptime(comp.uptimeSec)}
                    </span>
                )}
            </div>
        </Row>
    );
}

// formatUptime renders a process age at one unit of precision. Days matter,
// seconds do not: what a reader takes from this column is "did this restart
// recently", not the exact age.
export function formatUptime(sec: number): string {
    if (sec < 60) return `${Math.floor(sec)}s`;
    if (sec < 3600) return `${Math.floor(sec / 60)}m`;
    if (sec < 86400) return `${Math.floor(sec / 3600)}h`;
    return `${Math.floor(sec / 86400)}d`;
}
