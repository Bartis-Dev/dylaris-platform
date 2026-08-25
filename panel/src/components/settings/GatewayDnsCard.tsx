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
    const [acmeEnabled, setAcmeEnabled] = useState(false);
    const [acmeEmail, setAcmeEmail] = useState('');
    const [acmeDirectory, setAcmeDirectory] = useState('');
    const [acmeAgreed, setAcmeAgreed] = useState(false);
    const [saving, setSaving] = useState(false);

    const apply = (c: GatewayDnsConfig) => {
        setCfg(c);
        setProvider(c.provider);
        setZones(c.zones.join(', '));
        setEnabled(c.enabled);
        setAcmeEnabled(c.acme_enabled);
        setAcmeEmail(c.acme_email);
        setAcmeDirectory(c.acme_directory);
        setAcmeAgreed(c.acme_agreed);
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
            acme_enabled: acmeEnabled,
            acme_email: acmeEmail,
            acme_directory: acmeDirectory,
            acme_agreed: acmeAgreed,
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
        showToast('Gateway DNS settings saved', true);
    };

    return (
        <div className="card p-5 space-y-5">
            <div className="flex items-center gap-3">
                <div className="w-9 h-9 rounded-md bg-(--base-03) flex items-center justify-center">
                    <Globe size={18} className="text-(--accent-light)" />
                </div>
                <div>
                    <div className="font-medium text-sm text-(--base-09)">Automatic DNS &amp; certificates</div>
                    <div className="text-xs text-(--base-06)">
                        One provider token: the gateway keeps the edge and beam records pointed at
                        whatever is online, and gets the beam relay its TLS certificate
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

                    <div className="pt-4 border-t border-(--base-03) space-y-4">
                        <div>
                            <h3 className="mono-label">Certificates</h3>
                            <p className="text-xs text-(--base-06) mt-1.5">
                                The beam relay&apos;s client port is the one listener that needs a
                                publicly trusted certificate — the Beam app checks its chain and
                                hostname. It is obtained with the same token above, over the DNS-01
                                challenge, and renewed without a restart. Leave this off if you mount
                                a certificate on the relay yourself; that always wins.
                            </p>
                        </div>

                        <div>
                            <label className="input-label">Contact address</label>
                            <input
                                type="email"
                                value={acmeEmail}
                                onChange={e => setAcmeEmail(e.target.value)}
                                placeholder="ops@example.com"
                                className="input-field text-sm w-full"
                            />
                            <p className="text-xs text-(--base-06) mt-1.5">
                                The CA sends expiry warnings here. It is the last thing that tells you
                                this stopped working, so use an address someone reads.
                            </p>
                        </div>

                        <div>
                            <label className="input-label">Certificate authority</label>
                            <select
                                value={acmeDirectory}
                                onChange={e => setAcmeDirectory(e.target.value)}
                                className="input-field text-sm w-full"
                            >
                                <option value="">Let&apos;s Encrypt</option>
                                <option value="staging">Let&apos;s Encrypt (staging)</option>
                            </select>
                            <p className="text-xs text-(--base-06) mt-1.5">
                                Staging issues untrusted certificates against generous rate limits.
                                Use it to prove the DNS challenge works, then switch.
                            </p>
                        </div>

                        <label className="flex items-center gap-2 text-sm text-(--base-09) cursor-pointer">
                            <input
                                type="checkbox"
                                checked={acmeAgreed}
                                onChange={e => setAcmeAgreed(e.target.checked)}
                            />
                            I accept the CA&apos;s subscriber agreement
                        </label>

                        <label className="flex items-center gap-2 text-sm text-(--base-09) cursor-pointer">
                            <input
                                type="checkbox"
                                checked={acmeEnabled}
                                onChange={e => setAcmeEnabled(e.target.checked)}
                            />
                            Obtain and renew certificates
                        </label>

                        {cfg?.cert_status && (
                            <div className="space-y-2">
                                {cfg.cert_status.error && (
                                    <p className="text-xs text-(--error-light)">{cfg.cert_status.error}</p>
                                )}
                                {cfg.cert_status.note && (
                                    <p className="text-xs text-(--base-06)">{cfg.cert_status.note}</p>
                                )}
                                {(cfg.cert_status.names ?? []).length > 0 && (
                                    <div className="overflow-x-auto">
                                        <table className="w-full text-xs">
                                            <thead>
                                                <tr className="text-(--base-06) text-left">
                                                    <th className="font-normal py-1 pr-3">Name</th>
                                                    <th className="font-normal py-1 pr-3">Certificate</th>
                                                    <th className="font-normal py-1 pr-3">Expires</th>
                                                    <th className="font-normal py-1">Last error</th>
                                                </tr>
                                            </thead>
                                            <tbody>
                                                {(cfg.cert_status.names ?? []).map(n => (
                                                    <tr key={n.name} className="border-t border-(--base-03)">
                                                        <td className="py-1.5 pr-3 font-mono text-(--base-09)">{n.name}</td>
                                                        <td className="py-1.5 pr-3">
                                                            {n.have
                                                                ? <span className="text-(--success-light)">issued</span>
                                                                : <span className="text-(--base-06)">none yet</span>}
                                                        </td>
                                                        <td className="py-1.5 pr-3 text-(--base-07)">
                                                            {n.have && n.expires ? n.expires.slice(0, 10) : '—'}
                                                        </td>
                                                        <td className="py-1.5 text-(--base-06) break-all">{n.error || '—'}</td>
                                                    </tr>
                                                ))}
                                            </tbody>
                                        </table>
                                        <p className="text-xs text-(--base-06) mt-2">
                                            An error next to a certificate that already exists is a
                                            renewal failing. That is the one worth acting on now: the
                                            certificate is still valid, and will stop being so.
                                        </p>
                                    </div>
                                )}
                            </div>
                        )}
                    </div>

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
