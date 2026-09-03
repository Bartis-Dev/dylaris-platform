"use client";

/**
 * A throughput sparkline, drawn as a plain SVG polyline.
 *
 * Not a chart library: there is one of these per gateway component, and a
 * recharts ResponsiveContainer per card costs a resize observer and a full
 * layout pass each to draw a line with no axes, no legend and no tooltip.
 *
 * The scale is passed IN rather than taken from the series. Every card in a row
 * shares one maximum, so a warp leader carrying a tenth of the edge's traffic
 * looks like a tenth instead of looking identical - which is what a
 * per-series scale would draw.
 */
export default function Sparkline({
    values,
    max,
    className = '',
    title,
}: {
    values: number[];
    max: number;
    className?: string;
    title?: string;
}) {
    // One point cannot be a line, and a flat zero series is worth saying rather
    // than drawing as a line along the floor that reads like real quiet traffic.
    if (values.length < 2) {
        return <div className={`h-8 flex items-center text-[10px] font-mono text-(--base-05) ${className}`}>no history</div>;
    }

    const W = 100;
    const H = 28;
    const ceiling = max > 0 ? max : 1;
    const step = W / (values.length - 1);
    const y = (v: number) => H - Math.min(1, Math.max(0, v / ceiling)) * (H - 2) - 1;
    const line = values.map((v, i) => `${(i * step).toFixed(2)},${y(v).toFixed(2)}`).join(' ');

    return (
        <svg
            viewBox={`0 0 ${W} ${H}`}
            preserveAspectRatio="none"
            role="img"
            aria-label={title ?? 'throughput history'}
            className={`w-full h-8 ${className}`}
        >
            {title && <title>{title}</title>}
            <polygon points={`0,${H} ${line} ${W},${H}`} fill="var(--accent)" opacity={0.12} />
            <polyline points={line} fill="none" stroke="var(--accent)" strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
        </svg>
    );
}
