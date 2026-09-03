"use client";

/**
 * A small multi-line chart, drawn as plain SVG polylines.
 *
 * Not a chart library: there is one of these per gateway component, and a
 * recharts ResponsiveContainer per row costs a resize observer and a full
 * layout pass each to draw a line with no axes and no legend.
 *
 * The scale is passed IN rather than taken from the data. Percentages want a
 * fixed 0-100 so two rows are comparable at a glance, and throughput wants one
 * maximum shared across the whole grid - otherwise a relay carrying a tenth of
 * the edge's traffic draws an identical line.
 */

export interface SparkSeries {
    values: number[];
    /** A CSS colour, normally a design token: `var(--accent)`. */
    color: string;
    /** Filled under the line. One filled series per chart at most. */
    fill?: boolean;
}

export default function Sparkline({
    series,
    max,
    height = 28,
    className = '',
    title,
    empty = 'no history',
}: {
    series: SparkSeries[];
    max: number;
    height?: number;
    className?: string;
    title?: string;
    empty?: string;
}) {
    // One point cannot be a line. Said in words rather than drawn as a flat
    // line along the floor, which would read as real, quiet traffic.
    const drawable = series.filter(s => s.values.length > 1);
    if (drawable.length === 0) {
        return (
            <div
                className={`flex items-center text-[10px] font-mono text-(--base-05) ${className}`}
                style={{ height }}
            >
                {empty}
            </div>
        );
    }

    const W = 100;
    const H = height;
    const ceiling = max > 0 ? max : 1;

    const path = (values: number[]) => {
        const step = W / (values.length - 1);
        const y = (v: number) => H - Math.min(1, Math.max(0, v / ceiling)) * (H - 2) - 1;
        return values.map((v, i) => `${(i * step).toFixed(2)},${y(v).toFixed(2)}`).join(' ');
    };

    return (
        <svg
            viewBox={`0 0 ${W} ${H}`}
            preserveAspectRatio="none"
            role="img"
            aria-label={title ?? 'history'}
            className={`w-full ${className}`}
            style={{ height }}
        >
            {title && <title>{title}</title>}
            {drawable.map((s, i) => {
                const line = path(s.values);
                return (
                    <g key={i}>
                        {s.fill && <polygon points={`0,${H} ${line} ${W},${H}`} fill={s.color} opacity={0.12} />}
                        <polyline
                            points={line}
                            fill="none"
                            stroke={s.color}
                            strokeWidth={1.5}
                            vectorEffect="non-scaling-stroke"
                        />
                    </g>
                );
            })}
        </svg>
    );
}
