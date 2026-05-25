"use client";

import React, { useState, useEffect, useMemo, useRef } from 'react';
import {
    CreateRouteRequest,
    DomainCheckRequest,
    GatewayRouteOptions,
    HosterValidation,
    checkDomainAvailability,
    getGatewayRouteOptions,
} from '@/lib/api';
import { AlertCircle, Check, Globe, Loader2, X } from 'lucide-react';

// Availability state surfaced to the parent so it can disable submit when
// the picker is mid-check or has landed on a taken domain.
export type DomainAvailability = 'idle' | 'checking' | 'available' | 'taken';

interface Props {
    value: CreateRouteRequest;
    onChange: (next: CreateRouteRequest) => void;
    onAvailabilityChange?: (state: DomainAvailability) => void;
    error?: string;
    portChildren?: React.ReactNode; // The port select rendered next to the domain
}

const VALIDATION_HINT: Record<HosterValidation, string> = {
    letters: 'letters only',
    alphanumeric: 'letters + numbers',
    dns: 'letters, numbers and -',
};

const VALIDATION_REGEX: Record<HosterValidation, RegExp> = {
    letters: /^[a-z]+$/,
    alphanumeric: /^[a-z0-9]+$/,
    dns: /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/,
};

// Up to apex + 2 subdomains (a.b.c.d = 4 labels max)
const CUSTOM_DOMAIN_REGEX = /^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/;
function customDomainLabelsOk(d: string) {
    const labels = d.split('.').filter(Boolean);
    return labels.length >= 2 && labels.length <= 4;
}

/**
 * RouteDomainPicker is the user-facing domain entry for new routes.
 *
 * Renders one of three modes based on what the admin enabled:
 *   - hoster picker: subdomain text input + dropdown of base domains
 *   - custom domain: full-FQDN input + CNAME hint
 *   - legacy fallback: plain domain input (only when admin set up nothing)
 *
 * The component owns the mode toggle and writes the right shape into
 * `value` so the caller can post the request as-is.
 */
export default function RouteDomainPicker({ value, onChange, onAvailabilityChange, error, portChildren }: Props) {
    const [opts, setOpts] = useState<GatewayRouteOptions | null>(null);
    const [mode, setMode] = useState<'hoster' | 'custom' | 'legacy'>('legacy');
    const [subdomain, setSubdomain] = useState('');
    const [hosterDomain, setHosterDomain] = useState('');
    const [customDomain, setCustomDomain] = useState('');

    // Live availability state. The server endpoint is cheap; we still debounce
    // ~600ms so a fast typist doesn't fire one request per keystroke. A request
    // counter guards against out-of-order responses overwriting a fresher one
    // (e.g. user types "play", check fires, user backspaces to "pla", second
    // check fires; if the first response lands last we'd otherwise show the
    // wrong result).
    const [availability, setAvailability] = useState<DomainAvailability>('idle');
    const [availabilityReason, setAvailabilityReason] = useState<string>('');
    const reqCounter = useRef(0);

    useEffect(() => {
        getGatewayRouteOptions().then(res => {
            setOpts(res);
            const hosters = res.hosterDomains || [];
            if (hosters.length > 0) {
                setMode('hoster');
                setHosterDomain(hosters[0].domain);
            } else if (res.customDomainsEnabled) {
                setMode('custom');
            } else {
                setMode('legacy');
            }
        });
    }, []);

    const activeHoster = useMemo(
        () => (opts?.hosterDomains || []).find(h => h.domain === hosterDomain),
        [opts, hosterDomain],
    );

    // Push state up whenever any of the inputs change.
    useEffect(() => {
        if (mode === 'hoster') {
            onChange({ ...value, subdomain, hosterDomain, customDomain: undefined, domain: undefined });
        } else if (mode === 'custom') {
            onChange({ ...value, customDomain, subdomain: undefined, hosterDomain: undefined, domain: undefined });
        } else {
            onChange({ ...value, domain: customDomain, subdomain: undefined, hosterDomain: undefined, customDomain: undefined });
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [mode, subdomain, hosterDomain, customDomain]);

    // Debounced availability check. We only hit the API when the input passes
    // local format validation, so the user never sees a "checking..." spinner
    // for input we already know is malformed.
    useEffect(() => {
        let req: DomainCheckRequest | null = null;
        if (mode === 'hoster' && subdomain && hosterDomain && activeHoster) {
            const lower = subdomain.toLowerCase();
            if (VALIDATION_REGEX[activeHoster.validation].test(lower)) {
                req = { subdomain: lower, hosterDomain };
            }
        } else if (mode === 'custom' && customDomain) {
            const lower = customDomain.toLowerCase();
            if (CUSTOM_DOMAIN_REGEX.test(lower) && customDomainLabelsOk(lower)) {
                req = { customDomain: lower };
            }
        } else if (mode === 'legacy' && customDomain) {
            req = { domain: customDomain.toLowerCase() };
        }

        if (!req) {
            setAvailability('idle');
            setAvailabilityReason('');
            return;
        }

        setAvailability('checking');
        const myReq = ++reqCounter.current;
        const handle = setTimeout(async () => {
            try {
                const res = await checkDomainAvailability(req!);
                if (myReq !== reqCounter.current) return; // stale
                if (res.available) {
                    setAvailability('available');
                    setAvailabilityReason('');
                } else {
                    setAvailability('taken');
                    setAvailabilityReason(res.reason || 'already taken');
                }
            } catch {
                if (myReq !== reqCounter.current) return;
                // Network / 500 — don't block the user; treat as idle so the
                // server-side create is still the source of truth.
                setAvailability('idle');
                setAvailabilityReason('');
            }
        }, 600);

        return () => clearTimeout(handle);
    }, [mode, subdomain, hosterDomain, customDomain, activeHoster]);

    useEffect(() => {
        onAvailabilityChange?.(availability);
    }, [availability, onAvailabilityChange]);

    if (!opts) {
        return <div className="h-10 bg-(--base-02) rounded-md animate-pulse" />;
    }

    const hosters = opts.hosterDomains || [];
    const showHosterMode = hosters.length > 0;
    const showCustomMode = opts.customDomainsEnabled;

    // Live validation hint
    let hint: string | null = null;
    if (mode === 'hoster' && subdomain && activeHoster) {
        const ok = VALIDATION_REGEX[activeHoster.validation].test(subdomain.toLowerCase());
        if (!ok) hint = `Allowed: ${VALIDATION_HINT[activeHoster.validation]}`;
    } else if (mode === 'custom' && customDomain) {
        const lower = customDomain.toLowerCase();
        if (!CUSTOM_DOMAIN_REGEX.test(lower) || !customDomainLabelsOk(lower)) {
            hint = 'Format: yourdomain.com — up to 2 subdomain levels (e.g. play.mc.example.com)';
        } else if (hosters.some(h => lower === h.domain || lower.endsWith('.' + h.domain))) {
            hint = `${lower} is on a hoster domain — use the subdomain picker instead.`;
        }
    }

    // Compute the live preview domain from the current picker state.
    const noDomainConfigured = !showHosterMode && !showCustomMode;
    let previewDomain: string | null = null;
    if (!noDomainConfigured) {
        if (mode === 'hoster' && subdomain && hosterDomain) {
            previewDomain = `${subdomain}.${hosterDomain}`;
        } else if ((mode === 'custom' || mode === 'legacy') && customDomain) {
            previewDomain = customDomain;
        }
    }

    return (
        <div className="max-w-md space-y-2">
            {/* No-domain empty state */}
            {noDomainConfigured && (
                <div className="flex items-center gap-2 px-3 py-2.5 rounded-md bg-(--base-02) border border-(--base-03) text-sm text-(--base-05) cursor-not-allowed select-none">
                    <AlertCircle size={14} className="shrink-0 text-(--base-05)" />
                    <span>No domain configured yet — add one under Settings → Gateway.</span>
                </div>
            )}

            {/* Mode tabs (only when both hoster and custom are available) */}
            {!noDomainConfigured && showHosterMode && showCustomMode && (
                <div className="flex bg-(--base-03) p-1 rounded-md w-fit">
                    <button
                        type="button"
                        onClick={() => setMode('hoster')}
                        className={`px-3 py-1 text-xs rounded-md transition-colors ${mode === 'hoster' ? 'bg-(--accent) text-white' : 'text-(--base-07) hover:text-(--base-09)'}`}
                    >
                        Subdomain
                    </button>
                    <button
                        type="button"
                        onClick={() => setMode('custom')}
                        className={`px-3 py-1 text-xs rounded-md transition-colors ${mode === 'custom' ? 'bg-(--accent) text-white' : 'text-(--base-07) hover:text-(--base-09)'}`}
                    >
                        Custom Domain
                    </button>
                </div>
            )}

            {/* Hoster mode */}
            {!noDomainConfigured && mode === 'hoster' && showHosterMode && (
                <div className="flex gap-2 items-stretch">
                    <input
                        type="text"
                        value={subdomain}
                        onChange={e => setSubdomain(e.target.value.toLowerCase())}
                        placeholder="play"
                        className="input-field text-sm w-28 min-w-0"
                    />
                    <span className="flex items-center text-(--base-06) text-sm font-mono px-1">.</span>
                    <select
                        value={hosterDomain}
                        onChange={e => setHosterDomain(e.target.value)}
                        className="input-field text-sm w-44"
                    >
                        {hosters.map(h => (
                            <option key={h.domain} value={h.domain}>{h.domain}</option>
                        ))}
                    </select>
                    {portChildren}
                </div>
            )}

            {/* Custom mode */}
            {!noDomainConfigured && mode === 'custom' && showCustomMode && (
                <div className="space-y-2">
                    <div className="flex gap-2 items-stretch">
                        <div className="flex-1 flex items-center gap-2">
                            <Globe size={14} className="text-(--accent-light) shrink-0" />
                            <input
                                type="text"
                                value={customDomain}
                                onChange={e => setCustomDomain(e.target.value.toLowerCase())}
                                placeholder="play.example.com"
                                className="input-field flex-1 text-sm"
                            />
                        </div>
                        {portChildren}
                    </div>
                    {opts.cnameTarget && (
                        <div className="flex items-start gap-2 p-2.5 rounded-md bg-(--accent)/5 border border-(--accent)/20 text-xs">
                            <AlertCircle size={12} className="text-(--accent-light) shrink-0 mt-0.5" />
                            <span className="text-(--base-07)">
                                Set a CNAME record on your domain pointing to{' '}
                                <code className="font-mono text-(--accent-light) bg-(--base-02) px-1.5 py-0.5 rounded">{opts.cnameTarget}</code>
                                {' '}before saving — otherwise the route will not resolve.
                            </span>
                        </div>
                    )}
                </div>
            )}

            {/* Live preview */}
            {previewDomain && (
                <p className="text-xs text-(--base-06) font-mono">
                    Your domain will be: <span className="text-(--primary-light)">{previewDomain}</span>
                </p>
            )}

            {/* Availability indicator. Suppressed when a local format hint is
                already visible, since the API never gets called for invalid
                input and would just show a stale state. */}
            {!hint && availability === 'checking' && (
                <div className="flex items-center gap-1.5 text-xs text-(--base-06)">
                    <Loader2 size={12} className="animate-spin" />
                    Checking availability…
                </div>
            )}
            {!hint && availability === 'available' && (
                <div className="flex items-center gap-1.5 text-xs text-(--success-light)">
                    <Check size={12} />
                    Available
                </div>
            )}
            {!hint && availability === 'taken' && (
                <div className="flex items-center gap-1.5 text-xs text-(--error-light)">
                    <X size={12} />
                    {availabilityReason || 'Not available'}
                </div>
            )}

            {hint && (
                <div className="flex items-center gap-1.5 text-xs text-(--warning-light)">
                    <AlertCircle size={12} />
                    {hint}
                </div>
            )}
            {error && (
                <div className="flex items-center gap-1.5 text-xs text-(--error-light)">
                    <AlertCircle size={12} />
                    {error}
                </div>
            )}
        </div>
    );
}
