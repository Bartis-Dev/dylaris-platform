'use client';

import { useEffect, useRef, useState } from 'react';
import { Save, Copy, AlertTriangle, EyeOff, Plus, Network } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import {
	getWarpSettings, saveWarpSettings, mintWarpKey,
	type WarpSettings,
} from '@/lib/api/types';
import { API_URL } from '@/lib/api/core';

const enrollUrl = API_URL.replace(/\/api\/?$/, '');

export default function WarpTab() {
	const { routingMode, fileAccessMode } = useAppData();
	const gateOpen = routingMode === 'gateway' && fileAccessMode === 'beam';

	const [settings, setSettings] = useState<WarpSettings>({ clientSubnet: '10.0.99.0/24', leaderEndpoint: '' });
	const [loading, setLoading] = useState(true);
	const [saving, setSaving] = useState(false);
	const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
	const snapshot = useRef<string>('');

	const [form, setForm] = useState<{ name: string; policy: 'fixed' | 'general'; max_conns: number; on_new_conn: 'kill_old' | 'block' }>(
		{ name: '', policy: 'general', max_conns: 5, on_new_conn: 'block' });
	const [minting, setMinting] = useState(false);
	const [revealed, setRevealed] = useState<{ name: string; apiKey: string } | null>(null);

	const showToast = (msg: string, ok = true) => { setToast({ msg, ok }); setTimeout(() => setToast(null), 3500); };

	useEffect(() => {
		getWarpSettings().then(res => {
			if (res.success) {
				const s = { clientSubnet: res.clientSubnet || '10.0.99.0/24', leaderEndpoint: res.leaderEndpoint || '' };
				setSettings(s);
				snapshot.current = JSON.stringify(s);
			}
			setLoading(false);
		});
	}, []);

	const dirty = snapshot.current !== '' && JSON.stringify(settings) !== snapshot.current;

	const handleSave = async () => {
		setSaving(true);
		const res = await saveWarpSettings(settings);
		showToast(res.success ? 'Warp settings saved.' : (res.message || res.error || 'Save failed.'), res.success);
		if (res.success) snapshot.current = JSON.stringify(settings);
		setSaving(false);
	};

	const handleMint = async () => {
		if (!form.name.trim()) { showToast('Name required', false); return; }
		setMinting(true);
		const res = await mintWarpKey({ name: form.name.trim(), policy: form.policy, max_conns: form.max_conns, on_new_conn: form.on_new_conn });
		setMinting(false);
		if (res.success && res.api_key) {
			setRevealed({ name: form.name.trim(), apiKey: res.api_key });
			setForm({ name: '', policy: 'general', max_conns: 5, on_new_conn: 'block' });
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
						? 'Gateway routing + Beam files are active — external/home nodes are supported. Servers on external nodes route player traffic through the edge and use Beam for files.'
						: 'External nodes require routing_mode=gateway AND file_access=beam (set in the Gateway tab). Until then, connecting external nodes is disabled — their servers could not receive player traffic or file access.'}
				</p>
			</div>

			<div className="card p-5 space-y-4">
				<h3 className="text-sm font-display font-semibold text-(--accent-light)">Leader & Subnet</h3>
				<div>
					<label className="input-label">WG Client Subnet</label>
					<input className="input-field" value={settings.clientSubnet}
						onChange={e => setSettings(s => ({ ...s, clientSubnet: e.target.value }))} placeholder="10.0.99.0/24" />
					<p className="text-xs text-(--base-06) mt-1">Subnet warp allocates client IPs from (a Hetzner route → leader). Must be disjoint from your DC + overlay ranges.</p>
				</div>
				<div>
					<label className="input-label">Leader Endpoint</label>
					<input className="input-field" value={settings.leaderEndpoint}
						onChange={e => setSettings(s => ({ ...s, leaderEndpoint: e.target.value }))} placeholder="vpn.example.com:51820" />
					<p className="text-xs text-(--base-06) mt-1">Public host:port clients dial to reach the warp leader (the gateway host's public address).</p>
				</div>
				<button onClick={handleSave} disabled={!dirty || saving} className="btn btn-primary disabled:opacity-40">
					<Save size={14} /> Save Settings
				</button>
			</div>

			<div className="card p-5 space-y-4">
				<h3 className="text-sm font-display font-semibold text-(--accent-light) flex items-center gap-2"><Network size={15} /> Connect External Node</h3>
				<fieldset disabled={!gateOpen} className="space-y-4 disabled:opacity-50">
					<div className="grid grid-cols-1 md:grid-cols-2 gap-4">
						<div>
							<label className="input-label">Name</label>
							<input className="input-field" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="home-desktop" />
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
								This key is shown once. Put it into the home node's deploy ENV below.
							</p>
							<div className="space-y-1">
								<label className="mono-label">Client deploy ENV</label>
								<pre className="p-3 rounded-md bg-(--base-02) border border-(--base-04) font-mono text-xs whitespace-pre-wrap break-all">{`LEADER=false
LEADER_ENDPOINT=${settings.leaderEndpoint || '<leader-endpoint>'}
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
