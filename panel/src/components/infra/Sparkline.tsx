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
    /** Filled under the line. */
    fill?: boolean;
    /**
     * Which way this series grows from the baseline.
     *
     * Giving any series a direction turns the chart into the mirrored shape
     * every network graph uses: the baseline moves to the middle, "up" grows
     * upward and "down" grows downward. It is the one arrangement where the two
     * directions cannot be confused, because the picture says which is which
     * without a legend to read.
     */
    direction?: 'up' | 'down';
}

export default function Sparkline({
    series,
    max,
    height = 28,
    className = '',
    title,
    empty = 'no history',
    grid,
    showBaseline = true,
}: {
    series: SparkSeries[];
    max: number;
    height?: number;
    className?: string;
    title?: string;
    empty?: string;
    /**
     * Faint horizontal lines, as fractions of the scale (0.5 = halfway up).
     *
     * The point of them is to make a FIXED scale visible. Without a reference
     * line, a flat line at 3% and a flat line at 90% are the same picture in a
     * small box, and the reader has to trust the number beside it instead of
     * reading the chart.
     */
    grid?: number[];
    /**
     * Draw the mirrored chart's own zero line. Off when the surrounding box
     * draws one continuous line across itself instead - two lines at the same
     * y is two renderers landing on the same subpixel by agreement, and only
     * one of them can be the one a reader is meant to see.
     */
    showBaseline?: boolean;
}) {
    // One point cannot be a line. Said in words rather than drawn as a flat
    // line along the floor, which would read as real, quiet traffic.
    const drawable = series.filter(s => s.values.length > 1);
    if (drawable.length === 0) {
        // Same height as a drawn chart on purpose: a box that collapses when it
        // has nothing yet makes the row jump the moment the first point lands.
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
    const mirrored = series.some(s => s.direction);
    // Where zero sits, and how far a full-scale reading reaches from it.
    const base = mirrored ? H / 2 : H - 1;
    const span = mirrored ? H / 2 - 1 : H - 2;

    const path = (s: SparkSeries) => {
        const step = W / (s.values.length - 1);
        const sign = s.direction === 'down' ? 1 : -1;
        const y = (v: number) => base + sign * Math.min(1, Math.max(0, v / ceiling)) * span;
        return s.values.map((v, i) => `${(i * step).toFixed(2)},${y(v).toFixed(2)}`).join(' ');
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
            {(grid ?? []).map(f => (
                <line
                    key={f}
                    x1={0}
                    x2={W}
                    y1={H - f * (H - 2) - 1}
                    y2={H - f * (H - 2) - 1}
                    stroke="var(--base-04)"
                    strokeWidth={1}
                    // Solid, not dashed: the viewBox is stretched horizontally
                    // (preserveAspectRatio="none"), and non-scaling-stroke keeps
                    // the WIDTH but not the dash pattern - dashes come out
                    // smeared at whatever width the box happens to be.
                    vectorEffect="non-scaling-stroke"
                />
            ))}
            {mirrored && showBaseline && (
                // The zero line. Without it a mirrored chart has no visible
                // origin, and "below the middle" stops meaning anything.
                <line
                    x1={0}
                    x2={W}
                    y1={base}
                    y2={base}
                    stroke="var(--base-04)"
                    strokeWidth={1}
                    vectorEffect="non-scaling-stroke"
                />
            )}
            {drawable.map((s, i) => {
                const line = path(s);
                const closeAt = mirrored ? base : H;
                return (
                    <g key={i}>
                        {s.fill && <polygon points={`0,${closeAt} ${line} ${W},${closeAt}`} fill={s.color} opacity={0.14} />}
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
