// Where the long-term statistics are written.
//
// Two answers: the Core database, or a database of its own. The choice is not
// cosmetic - it is what decides the RESOLUTION of everything the Statistics
// screen will ever show, and it cannot be changed retroactively, because there
// is no backfill. Picking it is part of switching recording on.

import { API_URL, getAuthHeader, handleResponse, handleError } from '@/lib/api/core';

export type MetricsDBMode = 'core' | 'separate';

export interface MetricsDBTarget {
    mode: MetricsDBMode;
    host: string;
    port: string;
    dbName: string;
    user: string;
    /** Write-only. The GET never returns it; see passwordSet. */
    password?: string;
    sslMode: string;
}

/** What is being written RIGHT NOW, which is not always what is configured. */
export interface MetricsDBActive {
    recording: boolean;
    separate: boolean;
    resolution?: 'minute' | 'hour';
}

/**
 * One save: whether to record, and where.
 *
 * They travel together because they ARE one decision - recording starts the
 * moment `enabled` is true and the first bucket lands at the resolution the
 * target implies, with no way to backfill or convert afterwards.
 */
export interface MetricsDBRequest extends MetricsDBTarget {
    enabled: boolean;
}

export interface MetricsDBSettings extends MetricsDBRequest {
    /**
     * A password is stored. Distinct from an empty field, which here means
     * there genuinely is none - a metrics database on a private network can
     * legitimately run without one.
     */
    passwordSet: boolean;
    active: MetricsDBActive;
    /**
     * The CORE database has the TimescaleDB extension. It does NOT make the
     * Core database record minutes - the resolution follows from which database
     * is used - it only decides whether the table is chunked and compressed.
     */
    coreTimescale: boolean;
}

/** Result of the test button. `severity` drives the colour, not `ok`. */
export interface MetricsDBTestResult {
    ok: boolean;
    severity: 'ok' | 'warning' | 'error';
    message: string;
    timescale?: boolean;
    version?: string;
}

export const emptyMetricsDBTarget: MetricsDBRequest = {
    enabled: false,
    mode: 'core',
    host: '',
    port: '5432',
    dbName: '',
    user: '',
    password: '',
    sslMode: 'disable',
};

export async function getMetricsDB(): Promise<{ success: boolean; settings?: MetricsDBSettings; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/metrics-db`, { headers: getAuthHeader() });
        return (await handleResponse(res)) as { success: boolean; settings?: MetricsDBSettings; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; settings?: MetricsDBSettings; message?: string };
    }
}

export async function saveMetricsDB(
    target: MetricsDBRequest,
): Promise<{ success: boolean; settings?: MetricsDBSettings; warning?: string; message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/metrics-db`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json', ...getAuthHeader() },
            body: JSON.stringify(target),
        });
        return (await handleResponse(res)) as { success: boolean; settings?: MetricsDBSettings; warning?: string; message?: string };
    } catch (err) {
        return handleError(err) as { success: boolean; message?: string };
    }
}

export async function testMetricsDB(
    target: MetricsDBTarget,
): Promise<{ success: boolean } & Partial<MetricsDBTestResult> & { message?: string }> {
    try {
        const res = await fetch(`${API_URL}/admin/settings/metrics-db/test`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', ...getAuthHeader() },
            body: JSON.stringify(target),
        });
        return (await handleResponse(res)) as { success: boolean } & Partial<MetricsDBTestResult>;
    } catch (err) {
        return handleError(err) as { success: boolean; message?: string };
    }
}

/**
 * Whether the form is complete enough to save or test.
 *
 * The Core option needs nothing, which is the point of it being the default: a
 * fresh install is already valid. A password is deliberately NOT required - the
 * reference deployment runs its metrics database without one, reachable only
 * from Core on a two-member network.
 */
export function metricsDBIncomplete(t: MetricsDBTarget): string | null {
    if (t.mode !== 'separate') return null;
    if (!t.host.trim()) return 'Enter the host of the separate database.';
    if (!t.dbName.trim()) return 'Enter the database name.';
    if (!t.user.trim()) return 'Enter the user to connect as.';
    const port = Number(t.port.trim());
    if (!Number.isInteger(port) || port < 1 || port > 65535) return 'Port must be a number between 1 and 65535.';
    return null;
}

/**
 * One sentence describing what a mode gives you, for the form itself rather
 * than the test result.
 *
 * Hour buckets for the Core database EITHER WAY. The extension there changes
 * how the table is stored, never the resolution, and a form that blurred the
 * two would have an operator install TimescaleDB expecting minutes.
 */
export function metricsDBModeSummary(mode: MetricsDBMode, coreTimescale: boolean): string {
    if (mode === 'separate') {
        return 'Minute buckets. Needs a TimescaleDB of its own - a plain PostgreSQL works but stores every minute as an ordinary row.';
    }
    return coreTimescale
        ? 'Hour buckets, in the database this platform already uses, as a compressed hypertable. No second database to run.'
        : 'Hour buckets, in the database this platform already uses. A few hundred megabytes a year and no extension needed.';
}
