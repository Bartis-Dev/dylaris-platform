"use client";

import React, { useCallback, useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { Globe, Plus, Trash2, ShieldAlert, Loader2, Server } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import RouteDomainPicker, { DomainAvailability } from '@/components/RouteDomainPicker';
import {
    CreateRouteRequest, ExternalRoute,
    getExternalRoutes, createExternalRoute, deleteExternalRoute,
} from '@/lib/api';

// Route-only: a DDoS-protected address pointed at a server the user already
// runs. No managed node — the edge proxies straight to their public host:port.
export default function RoutesPage() {
    const router = useRouter();
    const { gatewayEnabled } = useAppData();

    const [routes, setRoutes] = useState<ExternalRoute[]>([]);
    const [loading, setLoading] = useState(true);
    const [creating, setCreating] = useState(false);
    const [domainReq, setDomainReq] = useState<CreateRouteRequest>({ targetPort: 25565 });
    const [availability, setAvailability] = useState<DomainAvailability>('idle');
    const [targetHost, setTargetHost] = useState('');
    const [targetPort, setTargetPort] = useState(25565);
    const [error, setError] = useState('');
    const [toast, setToast] = useState('');

    const load = useCallback(async () => {
        try {
            const r = await getExternalRoutes();
            setRoutes(Array.isArray(r) ? r : []);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => { load(); }, [load]);

    // Route-only needs the gateway on; bounce when it is not.
    useEffect(() => {
        if (!gatewayEnabled) router.replace('/');
    }, [gatewayEnabled, router]);

    const submit = async () => {
        setError('');
        if (!targetHost.trim()) { setError('Enter the address of your server'); return; }
        if (availability === 'taken') { setError('That domain is already taken'); return; }
        setCreating(true);
        try {
            await createExternalRoute({ ...domainReq, targetPort, targetHost: targetHost.trim() });
            setTargetHost('');
            setToast('Route created');
            setTimeout(() => setToast(''), 3000);
            await load();
        } catch (e) {
            setError(e instanceof Error ? e.message : 'Failed to create route');
        } finally {
            setCreating(false);
        }
    };

    const remove = async (domain: string) => {
        if (!confirm(`Delete the route ${domain}?`)) return;
        await deleteExternalRoute(domain);
        await load();
    };

    if (!gatewayEnabled) return null;

    return (
        <div className="max-w-3xl mx-auto p-6 space-y-8">
            <header className="flex items-center gap-3">
                <Globe size={22} className="text-(--accent-light)" />
                <div>
                    <h1 className="text-xl font-display">Protected addresses</h1>
                    <p className="text-sm text-(--base-06)">Point a DDoS-protected address at a server you already run — no node needed.</p>
                </div>
            </header>

            {/* Create */}
            <div className="card p-5 space-y-4">
                <h2 className="font-medium text-(--base-09)">New route</h2>

                <div className="flex flex-col gap-1.5">
                    <label className="input-label">Your domain</label>
                    <RouteDomainPicker value={domainReq} onChange={setDomainReq} onAvailabilityChange={setAvailability} />
                </div>

                <div className="grid grid-cols-[1fr_auto] gap-3 max-w-md">
                    <div className="flex flex-col gap-1.5">
                        <label className="input-label">Your server address</label>
                        <div className="flex items-center gap-2">
                            <Server size={14} className="text-(--base-06) shrink-0" />
                            <input
                                type="text"
                                value={targetHost}
                                onChange={e => setTargetHost(e.target.value)}
                                placeholder="play.myhost.com or 203.0.113.7"
                                className="input-field text-sm w-full"
                            />
                        </div>
                    </div>
                    <div className="flex flex-col gap-1.5">
                        <label className="input-label">Port</label>
                        <input
                            type="number"
                            value={targetPort}
                            onChange={e => setTargetPort(Number(e.target.value))}
                            className="input-field text-sm w-24 text-center"
                        />
                    </div>
                </div>

                {/* The critical security caveat for public-IP origins. */}
                <div className="flex items-start gap-2.5 p-3 rounded-md bg-(--warning)/5 border border-(--warning)/20">
                    <ShieldAlert size={15} className="text-(--warning-light) shrink-0 mt-0.5" />
                    <p className="text-xs text-(--base-07) leading-relaxed">
                        Lock your server&apos;s firewall to only accept connections from the Dylaris edge IPs. Otherwise an attacker who finds your real IP can hit it directly and bypass the protection. A home server behind NAT has no public IP to attack, so this is automatic there.
                    </p>
                </div>

                {error && <p className="text-sm text-(--error-light)">{error}</p>}

                <button onClick={submit} disabled={creating} className="btn btn-primary inline-flex items-center gap-2 w-fit disabled:opacity-60">
                    {creating ? <Loader2 size={16} className="animate-spin" /> : <Plus size={16} />}
                    {creating ? 'Creating…' : 'Create route'}
                </button>
            </div>

            {/* List */}
            <div className="space-y-2">
                <h2 className="font-medium text-(--base-09)">Your routes</h2>
                {loading ? (
                    <div className="card p-5 text-sm text-(--base-06)">Loading…</div>
                ) : routes.length === 0 ? (
                    <div className="card p-5 text-sm text-(--base-06)">No routes yet.</div>
                ) : (
                    routes.map(rt => (
                        <div key={rt.domain} className="card px-4 py-3 flex items-center justify-between gap-3">
                            <div className="min-w-0">
                                <div className="font-mono text-sm text-(--base-09) truncate">{rt.domain}</div>
                                <div className="text-xs text-(--base-06) font-mono">→ {rt.target_ip}:{rt.target_port}</div>
                            </div>
                            <button onClick={() => remove(rt.domain)} title="Delete" className="p-2 text-(--base-06) hover:text-(--error-light) transition-colors shrink-0">
                                <Trash2 size={16} />
                            </button>
                        </div>
                    ))
                )}
            </div>

            {toast && (
                <div className="fixed bottom-6 left-1/2 -translate-x-1/2 px-4 py-2.5 rounded-md bg-(--success) text-white text-sm shadow-lg">{toast}</div>
            )}
        </div>
    );
}
