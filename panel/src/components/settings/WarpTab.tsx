'use client';

import { useCallback, useEffect, useState } from 'react';
import { Copy, AlertTriangle, EyeOff, Plus, Network, Trash2, Server, Circle } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import {
    getWarpRegions, upsertWarpRegion, deleteWarpRegion,
    upsertWarpLeader, deleteWarpLeader, mintWarpKey,
    type WarpRegionView,
} from '@/lib/api/types';
import { API_URL } from '@/lib/api/core';

const enrollUrl = API_URL.replace(/\/api\/?$/, '');

export default function WarpTab() {
    const { routingMode, fileAccessMode } = useAppData();
    const gateOpen = routingMode === 'gateway' && fileAccessMode === 'beam';

    const [regions, setRegions] = useState<WarpRegionView[]>([]);
    const [loading, setLoading] = useState(true);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const [newRegion, setNewRegion] = useState({ region: '', subnet: '' });
    const [addingRegion, setAddingRegion] = useState(false);

    const [form, setForm] = useState<{ name: string; policy: 'fixed' | 'general'; max_conns: number; on_new_conn: 'kill_old' | 'block'; region: string }>(
        { name: '', policy: 'general', max_conns: 5, on_new_conn: 'block', region: '' });
    const [minting, setMinting] = useState(false);
    const [revealed, setRevealed] = useState<{ name: string; apiKey: string } | null>(null);

    const showToast = (msg: string, ok = true) => { setToast({ msg, ok }); setTimeout(() => setToast(null), 3500); };

    const load = useCallback(async () => {
        const res = await getWarpRegions();
        if (res.success) setRegions(res.regions || []);
        setLoading(false);
    }, []);

    useEffect(() => { load(); }, [load]);

    const addRegion = async () => {
        const region = newRegion.region.trim();
        const subnet = newRegion.subnet.trim();
        if (!region || !subnet) { showToast('Region id and subnet are required.', false); return; }
        setAddingRegion(true);
        const res = await upsertWarpRegion({ region, subnet, enabled: true });
        setAddingRegion(false);
        if (res.success) { setNewRegion({ region: '', subnet: '' }); showToast('Region saved.'); load(); }
        else showToast(res.message || res.error || 'Save failed.', false);
    };

    const saveRegion = async (region: string, subnet: string, enabled: boolean) => {
        const res = await upsertWarpRegion({ region, subnet, enabled });
        if (res.success) { showToast('Region updated.'); load(); }
        else showToast(res.message || res.error || 'Save failed.', false);
    };

    const delRegion = async (region: string) => {
        const res = await deleteWarpRegion(region);
        if (res.success) { showToast('Region deleted.'); load(); }
        else showToast(res.message || res.error || 'Delete failed.', false);
    };

    const saveLeader = async (leaderId: string, region: string, endpoint: string, enabled: boolean) => {
        const res = await upsertWarpLeader({ leaderId, region, endpoint, enabled });
        if (res.success) { showToast('Leader saved.'); load(); }
        else showToast(res.message || res.error || 'Save failed.', false);
    };

    const delLeader = async (leaderId: string) => {
        const res = await deleteWarpLeader(leaderId);
        if (res.success) { showToast('Leader deleted.'); load(); }
        else showToast(res.message || res.error || 'Delete failed.', false);
    };

    const handleMint = async () => {
        if (!form.name.trim()) { showToast('Name required', false); return; }
        setMinting(true);
        const res = await mintWarpKey({
            name: form.name.trim(), policy: form.policy, max_conns: form.max_conns,
            on_new_conn: form.on_new_conn, region: form.region,
        });
        setMinting(false);
        if (res.success && res.api_key) {
            setRevealed({ name: form.name.trim(), apiKey: res.api_key });
            setForm({ name: '', policy: 'general', max_conns: 5, on_new_conn: 'block', region: '' });
        } else {
            showToast(res.message || res.error || 'Mint failed', false);
        }
    };

    if (loading) return <div className="space-y-6"><div className="h-8 w-40 bg-(--base-03) rounded animate-pulse" /><div className="h-64 bg-(--base-02) rounded animate-pulse" /></div>;

    return (
        <div className="space-y-6">
            <div className={`flex items-start gap-3 p-3 rounded-md border ${gateOpen ? 'border-(--success)/30 bg-(--success)/5' : 'border-(--warning)/40 bg-(--warning)/5'}`}>
                <AlertTriangle size={15} className={`mt-0.5 shrink-0 ${gateOpen ? 'text-(--success-light)' : 'text-(--warning-light)'}`} />
                <p className="text-xs text-(--base-07)">
                    {gateOpen
                        ? 'Gateway routing + Beam files are active — external/home nodes are supported. Each region is one WG identity (subnet + key); its leaders are interchangeable endpoints clients fail over between without changing IP.'
                        : 'External nodes require routing_mode=gateway AND file_access=beam (set in the Gateway tab). Until then, connecting external nodes is disabled — their servers could not receive player traffic or file access.'}
                </p>
            </div>

            {/* Regions + leaders */}
            <div className="card p-5 space-y-4">
                <h3 className="text-sm font-display font-semibold text-(--accent-light) flex items-center gap-2"><Network size={15} /> Regions & Hubs</h3>

                {regions.length === 0 && (
                    <p className="text-xs text-(--base-06)">No regions yet. Add one below (e.g. <span className="font-mono">eu-central</span> = <span className="font-mono">10.99.1.0/24</span>).</p>
                )}

                <div className="space-y-3">
                    {regions.map(r => (
                        <RegionCard key={r.region} region={r} onSaveRegion={saveRegion} onDeleteRegion={delRegion} onSaveLeader={saveLeader} onDeleteLeader={delLeader} />
                    ))}
                </div>

                {/* Add region */}
                <div className="flex flex-col md:flex-row gap-2 md:items-end border-t border-(--base-04) pt-4">
                    <div className="flex-1">
                        <label className="input-label">Region id</label>
                        <input className="input-field" value={newRegion.region} onChange={e => setNewRegion(s => ({ ...s, region: e.target.value }))} placeholder="eu-central" />
                    </div>
                    <div className="flex-1">
                        <label className="input-label">Subnet (CIDR)</label>
                        <input className="input-field" value={newRegion.subnet} onChange={e => setNewRegion(s => ({ ...s, subnet: e.target.value }))} placeholder="10.99.1.0/24" />
                    </div>
                    <button onClick={addRegion} disabled={addingRegion} className="btn btn-primary disabled:opacity-40">
                        <Plus size={14} /> Add region
                    </button>
                </div>
                <p className="text-xs text-(--base-06)">Subnets must be disjoint from your DC + overlay ranges and from each other. The first host (.1) is reserved for the region&apos;s leaders.</p>
            </div>

            {/* Mint enrollment key */}
            <div className="card p-5 space-y-4">
                <h3 className="text-sm font-display font-semibold text-(--accent-light) flex items-center gap-2"><Network size={15} /> Connect External Node</h3>
                <fieldset disabled={!gateOpen} className="space-y-4 disabled:opacity-50">
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                        <div>
                            <label className="input-label">Name</label>
                            <input className="input-field" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="home-desktop" />
                        </div>
                        <div>
                            <label className="input-label">Region</label>
                            <select className="input-field" value={form.region} onChange={e => setForm(f => ({ ...f, region: e.target.value }))}>
                                <option value="">Auto (least-loaded live region)</option>
                                {regions.map(r => <option key={r.region} value={r.region}>{r.region}</option>)}
                            </select>
                        </div>
                        <div>
                            <label className="input-label">Policy</label>
                            <select className="input-field" value={form.policy} onChange={e => setForm(f => ({ ...f, policy: e.target.value as 'fixed' | 'general' }))}>
                                <option value="general">General (N connections)</option>
                                <option value="fixed">Fixed (1 connection)</option>
                            </select>
                        </div>
                        {form.policy === 'general' ? (
                            <div>
                                <label className="input-label">Max Connections</label>
                                <input type="number" min={1} className="input-field" value={form.max_conns} onChange={e => setForm(f => ({ ...f, max_conns: parseInt(e.target.value) || 1 }))} />
                            </div>
                        ) : (
                            <div>
                                <label className="input-label">On New Connection</label>
                                <select className="input-field" value={form.on_new_conn} onChange={e => setForm(f => ({ ...f, on_new_conn: e.target.value as 'kill_old' | 'block' }))}>
                                    <option value="block">Block new</option>
                                    <option value="kill_old">Kill old</option>
                                </select>
                            </div>
                        )}
                    </div>
                    <button onClick={handleMint} disabled={minting || !gateOpen} className="btn btn-primary disabled:opacity-40">
                        <Plus size={14} /> Mint Enrollment Key
                    </button>
                </fieldset>
            </div>

            {revealed && (
                <div className="modal-overlay animate-fade-in" onClick={() => setRevealed(null)}>
                    <div className="modal-panel max-w-xl" onClick={e => e.stopPropagation()}>
                        <div className="modal-header"><h3 className="modal-title text-(--accent-light)">{revealed.name} — copy now</h3></div>
                        <div className="modal-body space-y-3">
                            <p className="text-sm text-(--base-07) flex items-start gap-2">
                                <AlertTriangle size={14} className="text-(--warning-light) shrink-0 mt-0.5" />
                                This key is shown once. Put it into the home node&apos;s deploy ENV below. The client discovers its region&apos;s leader endpoints automatically at enroll.
                            </p>
                            <div className="space-y-1">
                                <label className="mono-label">Client deploy ENV</label>
                                <pre className="p-3 rounded-md bg-(--base-02) border border-(--base-04) font-mono text-xs whitespace-pre-wrap break-all">{`LEADER=false
ENROLL_URL=${enrollUrl || '<core-url e.g. https://core.example.com:25500>'}
API_KEY=${revealed.apiKey}
TUNNEL_SUBNETS=<your-dc-subnet e.g. 10.0.0.0/24>`}</pre>
                            </div>
                            <div className="space-y-1">
                                <label className="mono-label">Then join the swarm (run on the home node, tunnel up first)</label>
                                <pre className="p-3 rounded-md bg-(--base-02) border border-(--base-04) font-mono text-xs whitespace-pre-wrap break-all">{`docker swarm join --advertise-addr <client-wg-ip> <manager-dc-ip>:2377 --token <worker-token>
# on a manager: docker node update --label-add dylaris_role=external <node-id>`}</pre>
                            </div>
                            <button onClick={() => { navigator.clipboard.writeText(revealed.apiKey); showToast('API key copied.', true); }} className="btn btn-secondary btn-sm">
                                <Copy size={12} /> Copy API key
                            </button>
                        </div>
                        <div className="modal-footer"><button onClick={() => setRevealed(null)} className="btn btn-primary"><EyeOff size={12} /> Done</button></div>
                    </div>
                </div>
            )}

            {toast && <div className={`fixed bottom-4 right-4 px-4 py-2 rounded-md text-sm font-medium ${toast.ok ? 'bg-(--success)/20 text-(--success-light) border border-(--success)/40' : 'bg-(--error)/20 text-(--error-light) border border-(--error)/40'}`}>{toast.msg}</div>}
        </div>
    );
}

function RegionCard({ region, onSaveRegion, onDeleteRegion, onSaveLeader, onDeleteLeader }: {
    region: WarpRegionView;
    onSaveRegion: (region: string, subnet: string, enabled: boolean) => void;
    onDeleteRegion: (region: string) => void;
    onSaveLeader: (leaderId: string, region: string, endpoint: string, enabled: boolean) => void;
    onDeleteLeader: (leaderId: string) => void;
}) {
    const [subnet, setSubnet] = useState(region.subnet);
    const [newLeader, setNewLeader] = useState({ leaderId: '', endpoint: '' });
    const leaders = region.leaders || [];
    const subnetDirty = subnet.trim() !== region.subnet;

    return (
        <div className="rounded-md border border-(--base-04) p-4 space-y-3">
            <div className="flex flex-wrap items-center gap-3">
                <span className="font-mono text-sm text-(--base-09)">{region.region}</span>
                <span className="badge badge-neutral">{region.peerCount} peer{region.peerCount === 1 ? '' : 's'}</span>
                <span className={`badge ${region.enabled ? 'badge-success' : 'badge-neutral'}`}>{region.enabled ? 'enabled' : 'disabled'}</span>
                <div className="ml-auto flex items-center gap-2">
                    <button
                        type="button"
                        onClick={() => onSaveRegion(region.region, region.subnet, !region.enabled)}
                        className="btn btn-secondary btn-sm"
                    >
                        {region.enabled ? 'Disable' : 'Enable'}
                    </button>
                    <button type="button" onClick={() => onDeleteRegion(region.region)} className="btn btn-danger btn-sm" title="Delete region (and its leaders)">
                        <Trash2 size={13} />
                    </button>
                </div>
            </div>

            <div className="flex items-end gap-2">
                <div className="flex-1">
                    <label className="input-label">Subnet</label>
                    <input className="input-field input-mono" value={subnet} onChange={e => setSubnet(e.target.value)} />
                </div>
                <button type="button" onClick={() => onSaveRegion(region.region, subnet.trim(), region.enabled)} disabled={!subnetDirty} className="btn btn-secondary btn-sm disabled:opacity-40">
                    Save subnet
                </button>
            </div>

            <div className="space-y-2">
                <label className="input-label flex items-center gap-1.5"><Server size={12} /> Leaders</label>
                {leaders.length === 0 && <p className="text-xs text-(--base-06)">No leader endpoints yet — clients can enroll but have nothing to dial.</p>}
                {leaders.map(l => (
                    <div key={l.leaderId} className="flex flex-wrap items-center gap-2 rounded-md border border-(--base-03) px-3 py-2">
                        <Circle size={9} className={l.alive ? 'text-(--success-light) fill-current' : 'text-(--base-05) fill-current'} aria-label={l.alive ? 'live' : 'no heartbeat'} />
                        <span className="font-mono text-xs text-(--base-08)">{l.leaderId}</span>
                        <span className="font-mono text-xs text-(--base-06)">{l.endpoint}</span>
                        <div className="ml-auto flex items-center gap-2">
                            <button type="button" onClick={() => onSaveLeader(l.leaderId, region.region, l.endpoint, !l.enabled)} className="btn btn-secondary btn-sm">
                                {l.enabled ? 'Disable' : 'Enable'}
                            </button>
                            <button type="button" onClick={() => onDeleteLeader(l.leaderId)} className="btn btn-danger btn-sm" title="Delete leader">
                                <Trash2 size={12} />
                            </button>
                        </div>
                    </div>
                ))}
                <div className="flex flex-col md:flex-row gap-2 md:items-end">
                    <div className="flex-1">
                        <label className="mono-label">Leader id</label>
                        <input className="input-field" value={newLeader.leaderId} onChange={e => setNewLeader(s => ({ ...s, leaderId: e.target.value }))} placeholder={`${region.region}-01`} />
                    </div>
                    <div className="flex-1">
                        <label className="mono-label">Endpoint (host:port)</label>
                        <input className="input-field" value={newLeader.endpoint} onChange={e => setNewLeader(s => ({ ...s, endpoint: e.target.value }))} placeholder="vpn-eu1.example.com:25599" />
                    </div>
                    <button
                        type="button"
                        onClick={() => {
                            if (!newLeader.leaderId.trim() || !newLeader.endpoint.trim()) return;
                            onSaveLeader(newLeader.leaderId.trim(), region.region, newLeader.endpoint.trim(), true);
                            setNewLeader({ leaderId: '', endpoint: '' });
                        }}
                        className="btn btn-secondary btn-sm"
                    >
                        <Plus size={12} /> Add leader
                    </button>
                </div>
            </div>
        </div>
    );
}
