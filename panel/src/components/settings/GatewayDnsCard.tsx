'use client';

import { useEffect, useState } from 'react';
import { Globe, CircleAlert, Loader2 } from 'lucide-react';
import {
    getGatewayDns,
    saveGatewayDns,
    parseZones,
    GatewayDnsConfig,
} from '@/lib/api/gatewayDns';

// The DNS credential lives in the gateway Hub. This card is a form over it, not
// a second copy: Core forwards what is typed here and stores nothing, so there
// is one credential and one writer of records.

export default function GatewayDnsCard({ showToast }: { showToast: (msg: string, ok?: boolean) => void }) {
    const [loading, setLoading] = useState(true);
    const [available, setAvailable] = useState(true);
    const [cfg, setCfg] = useState<GatewayDnsConfig | null>(null);
    const [error, setError] = useState<string | null>(null);

    const [provider, setProvider] = useState('');
    const [token, setToken] = useState('');
    const [zones, setZones] = useState('');
    const [enabled, setEnabled] = useState(false);
    const [saving, setSaving] = useState(false);

    const apply = (c: GatewayDnsConfig) => {
        setCfg(c);
        setProvider(c.provider);
        setZones(c.zones.join(', '));
        setEnabled(c.enabled);
        // Never repopulated from the server, because the server never sends it.
        setToken('');
    };

    const load = async () => {
        const res = await getGatewayDns();
        setLoading(false);
        if (!res.success) {
            setError(res.message || 'Could not read the gateway DNS settings.');
            return;
        }
        setError(null);
        if (res.available === false) {
            setAvailable(false);
            return;
        }
        setAvailable(true);
        if (res.config) apply(res.config);
    };

    useEffect(() => {
        load();
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    const handleSave = async () => {
        setSaving(true);
        setError(null);
        const res = await saveGatewayDns({
            provider,
            token,
            zones: parseZones(zones),
            enabled,
        });
        setSaving(false);
        if (!res.success) {
            // Shown inline as well as in the toast: the hub's refusals name what
            // is wrong with the form ("name at least one zone"), and a toast that
            // has already faded is no help to someone fixing it.
            setError(res.message || 'Could not save.');
            showToast(res.message || 'Could not save.', false);
            return;
        }
        if (res.config) apply(res.config);
        showToast('DNS settings saved', true);
    };

    return (
        <div className="card p-5 space-y-5">
            <div className="flex items-center gap-3">
                <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                    <Globe size={18} className="text-(--accent-light)" />
                </div>
                <div>
                    <div className="font-medium text-sm text-(--base-09)">Automatic DNS</div>
                    <div className="text-xs text-(--base-06)">
                        Let the gateway keep the edge and beam records pointed at whatever is online
                    </div>
                </div>
            </div>

            {loading && (
                <div className="flex items-center gap-2 text-xs text-(--base-06)">
                    <Loader2 size={14} className="animate-spin" />
                    Reading the gateway&apos;s settings…
                </div>
            )}

            {!loading && !available && (
                <p className="text-xs text-(--base-05) italic px-3 py-3 rounded-md bg-(--base-02)">
                    No gateway is connected to this platform, so there are no records to write.
                    Nodes need none at all, and the panel and API are yours to point at your reverse
                    proxy. Set <code className="font-mono">GATEWAY_HUB_URL</code> on Core if you run
                    the gateway.
                </p>
            )}

            {!loading && available && (
                <>
                    <p className="text-xs text-(--base-06)">
                        The credential is stored by the gateway, not here — this platform keeps no
                        copy of it. The same token also gets the beam relay its TLS certificate.
                    </p>

                    {cfg?.env_locked && (
                        <div className="flex items-start gap-2 text-xs text-(--base-07) px-3 py-2 rounded-md bg-(--base-02)">
                            <CircleAlert size={14} className="shrink-0 mt-0.5 text-(--base-06)" />
                            <span>
                                The gateway has a credential mounted as an environment variable,
                                which wins over anything saved here.
                            </span>
                        </div>
                    )}

                    <div>
                        <label className="input-label">Provider</label>
                        <select
                            value={provider}
                            onChange={e => setProvider(e.target.value)}
                            className="input-field text-sm w-full"
                        >
                            <option value="">— none —</option>
                            {(cfg?.providers ?? []).map(p => (
                                <option key={p.name} value={p.name}>{p.label}</option>
                            ))}
                        </select>
                    </div>

                    <div>
                        <label className="input-label">API token</label>
                        <input
                            type="password"
                            autoComplete="off"
                            value={token}
                            onChange={e => setToken(e.target.value)}
                            placeholder={cfg?.has_token ? 'stored — leave blank to keep' : 'paste the token'}
                            className="input-field input-mono text-sm w-full"
                        />
                        <p className="text-xs text-(--base-06) mt-1.5">
                            Write-only. Leaving it blank keeps the stored one, so editing a zone does
                            not quietly erase the credential. A provider that needs more than one
                            value takes a JSON object here.
                        </p>
                    </div>

                    <div>
                        <label className="input-label">Zones</label>
                        <input
                            type="text"
                            value={zones}
                            onChange={e => setZones(e.target.value)}
                            placeholder="example.com, example.net"
                            className="input-field input-mono text-sm w-full"
                        />
                        <p className="text-xs text-(--base-06) mt-1.5">
                            Comma-separated. A name outside every zone here is never written — this
                            is the boundary of what the token is allowed to touch.
                        </p>
                    </div>

                    <label className="flex items-center gap-2 text-sm text-(--base-09) cursor-pointer">
                        <input
                            type="checkbox"
                            checked={enabled}
                            onChange={e => setEnabled(e.target.checked)}
                        />
                        Write records automatically
                    </label>

                    {error && (
                        <div className="flex items-start gap-2 text-xs text-(--error-light) px-3 py-2 rounded-md bg-(--base-02)">
                            <CircleAlert size={14} className="shrink-0 mt-0.5" />
                            <span>{error}</span>
                        </div>
                    )}

                    <button
                        type="button"
                        onClick={handleSave}
                        disabled={saving}
                        className="btn btn-primary btn-sm disabled:opacity-40"
                    >
                        {saving ? 'Saving…' : 'Save'}
                    </button>
                </>
            )}

            {!loading && error && !available && (
                <div className="flex items-start gap-2 text-xs text-(--error-light)">
                    <CircleAlert size={14} className="shrink-0 mt-0.5" />
                    <span>{error}</span>
                </div>
            )}
        </div>
    );
}
