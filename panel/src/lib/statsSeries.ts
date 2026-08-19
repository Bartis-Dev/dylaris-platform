// Helpers for reading a headline number off a stats series.

/**
 * The most recent sample that actually carries `key`.
 *
 * Stats samples are sparse by design: the JVM heap field is omitted on any tick
 * where no fresh post-GC reading was available, so reading only the final
 * sample reported a running server as using 0 MB whenever the last tick
 * happened to fall between garbage collections.
 *
 * Returns 0 when no sample in the window carries the key, which is the honest
 * answer for a series that has never had one.
 */
export function latestValue<T extends object>(series: T[], key: keyof T): number {
    for (let i = series.length - 1; i >= 0; i--) {
        const v = series[i][key];
        if (typeof v === 'number' && Number.isFinite(v)) return v;
    }
    return 0;
}
