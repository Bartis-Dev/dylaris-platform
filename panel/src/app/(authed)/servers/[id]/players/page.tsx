"use client";

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useParams } from 'next/navigation';
import {
    Users, ShieldX, Skull, ShieldCheck, ShieldOff, Trash2,
    RefreshCw, Search, Send, AlertTriangle, Crown, ListChecks,
    CircleCheck, CircleAlert, X, ListPlus, MessageSquare, Terminal, Lock,
} from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import {
    rconList, rconKick, rconBan, rconUnban, rconOp, rconDeop,
    rconWhitelistAdd, rconWhitelistRemove, rconTell, getRconConfig,
    parsePlayerList, friendlyRconError, type OnlinePlayer,
} from '@/lib/api/rcon';
import { getFileContent } from '@/lib/api';
import RconConfigCard from '@/components/RconConfigCard';
import { Skeleton, SkeletonText, SkeletonCircle } from '@/components/Skeleton';

// Player Management. All operations are RCON underneath. Online
// list is polled every 10s. Bans/whitelist/ops are sourced from the JSON
// files in the active sub-server dir (banned-players.json / whitelist.json /
// ops.json) read via the FileBrowser endpoint — RCON is used to mutate, the
// files give us the authoritative view of the current state.

interface JsonPlayerEntry {
    uuid?: string;
    name: string;
    reason?: string;
    source?: string;
    created?: string;
    expires?: string;
    level?: number;
    bypassesPlayerLimit?: boolean;
}

type Section = 'online' | 'bans' | 'whitelist' | 'ops' | 'rcon';

const SECTIONS: { id: Section; label: string; Icon: React.ComponentType<{ size?: number }> }[] = [
    { id: 'online',    label: 'Online',    Icon: Users },
    { id: 'bans',      label: 'Bans',      Icon: ShieldX },
    { id: 'whitelist', label: 'Whitelist', Icon: ListChecks },
    { id: 'ops',       label: 'Operators', Icon: Crown },
    { id: 'rcon',      label: 'RCON',      Icon: Terminal },
];

// Every section except RCON itself needs a live RCON connection. When RCON is
// off we force the RCON section and lock the rest until the operator enables it.
const RCON_DEPENDENT: Section[] = ['online', 'bans', 'whitelist', 'ops'];

export default function ServerPlayersPage() {
    const params = useParams();
    const { servers } = useAppData();
    const serverId = Number(params?.id);
    const server = servers.find(s => s.id === serverId);

    const [section, setSection] = useState<Section>('online');
    const [search, setSearch] = useState('');
    const [online, setOnline] = useState<OnlinePlayer[]>([]);
    const [bans, setBans] = useState<JsonPlayerEntry[]>([]);
    const [whitelist, setWhitelist] = useState<JsonPlayerEntry[]>([]);
    const [ops, setOps] = useState<JsonPlayerEntry[]>([]);
    const [loading, setLoading] = useState(false);
    const [actionError, setActionError] = useState<string | null>(null);
    const [rconEnabled, setRconEnabled] = useState(false);
    const [rconLoaded, setRconLoaded] = useState(false);

    const [confirm, setConfirm] = useState<{
        title: string;
        message: string;
        onConfirm: () => void | Promise<void>;
        danger?: boolean;
    } | null>(null);
    const [tellPrompt, setTellPrompt] = useState<{ player: string; message: string } | null>(null);
    const [addPrompt, setAddPrompt] = useState<{ target: 'whitelist' | 'ops'; name: string } | null>(null);
    const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

    const showToast = useCallback((msg: string, ok = true) => {
        setToast({ msg, ok });
        setTimeout(() => setToast(null), 3500);
    }, []);

    const activeSub = server?.activeSubServer || '';
    const uuid = server?.uuid || '';

    // With RCON off, the effective section is always 'rcon' (the only usable
    // one); the user's chosen section resumes once RCON is enabled. This is
    // also what gates the auto rconList below against a server that has been
    // enabled but not yet restarted: RconConfigCard only reports enabled=true
    // through onEnabledChange once it has confirmed (post-restart) that RCON
    // is actually reachable, so rconEnabled here stays false - and this
    // section stays forced to 'rcon' - until then.
    const effectiveSection: Section = rconEnabled ? section : 'rcon';

    // Load the RCON state once so we know whether to lock the other sections.
    // RconConfigCard also reports flips live via onEnabledChange below.
    useEffect(() => {
        let cancelled = false;
        (async () => {
            const res = await getRconConfig(serverId);
            if (cancelled) return;
            // Keep the RCON-dependent tabs locked when a restart is still pending
            // (enabled in the DB but server.properties not yet re-read on a boot):
            // treat it as not-yet-live, matching the comment above and surviving a
            // page reload.
            if (res.success) setRconEnabled(res.enabled && !res.restartRequired);
            setRconLoaded(true);
        })();
        return () => { cancelled = true; };
    }, [serverId]);

    // Bans, whitelist and operators are read from the sub-server's JSON files,
    // which needs files.read - a DIFFERENT capability from the rcon.exec that
    // every action on this page uses. Any failure used to come back as an empty
    // array, so a member who may kick, ban and op but not read files was shown
    // "nobody is banned" and "the whitelist is empty". Those are claims, and the
    // panel did not know them to be true.
    //
    // 404 is the one failure that IS an empty list: a server nobody has ever
    // banned on has no banned-players.json. Everything else is reported.
    const loadJsonList = useCallback(async (
        filename: string,
    ): Promise<{ entries: JsonPlayerEntry[]; error?: string }> => {
        if (!activeSub || !uuid) return { entries: [] };
        const res = await getFileContent(`${activeSub}/${filename}`, uuid);
        if (!res.success) {
            if (res.status === 404) return { entries: [] };
            if (res.status === 403) {
                return { entries: [], error: `You do not have permission to read ${filename}. Managing players needs the file-read capability on this server as well.` };
            }
            return { entries: [], error: `${filename} could not be read: ${res.message || 'unknown error'}` };
        }
        try {
            const parsed = JSON.parse(res.content || '[]');
            return { entries: Array.isArray(parsed) ? parsed : [] };
        } catch {
            return { entries: [], error: `${filename} is not valid JSON - the server may be mid-write.` };
        }
    }, [activeSub, uuid]);

    // background=true is the 10s poll: it must not touch `loading`, or the whole
    // list is swapped for six skeleton cards every ten seconds. Only a first load
    // and an explicit Refresh show a loading state; a poll replaces the data in
    // place, which is what makes it invisible.
    const refresh = useCallback(async (background = false) => {
        if (!serverId || effectiveSection === 'rcon') return;
        if (!background) setLoading(true);
        setActionError(null);

        if (effectiveSection === 'online') {
            const res = await rconList(serverId);
            if (!res.success) {
                // A dial/connection error here means RCON was enabled but the
                // server has not restarted since (or crashed after) - never
                // show the raw Go dial string (it also leaks the internal
                // mc_<uuid> container hostname).
                setActionError(friendlyRconError(res.error, 'RCON unavailable'));
                setOnline([]);
            } else {
                setOnline(parsePlayerList(res.output || ''));
            }
        }
        // setActionError, not a silent empty list: the banner above the table is
        // the difference between "nobody is banned" and "you cannot see who is".
        if (effectiveSection === 'bans') {
            const { entries, error } = await loadJsonList('banned-players.json');
            setBans(entries);
            if (error) setActionError(error);
        }
        if (effectiveSection === 'whitelist') {
            const { entries, error } = await loadJsonList('whitelist.json');
            setWhitelist(entries);
            if (error) setActionError(error);
        }
        if (effectiveSection === 'ops') {
            const { entries, error } = await loadJsonList('ops.json');
            setOps(entries);
            if (error) setActionError(error);
        }

        setLoading(false);
    }, [serverId, effectiveSection, loadJsonList]);

    // Section switches DO show the skeleton: the previous section's rows would
    // otherwise sit there looking like this section's data.
    useEffect(() => { refresh(); }, [refresh]);

    // Online list auto-poll every 10s. Other sections are file-backed and
    // change only on user-initiated RCON commands — refresh on action.
    useEffect(() => {
        if (effectiveSection !== 'online') return;
        const id = setInterval(() => refresh(true), 10_000);
        return () => clearInterval(id);
    }, [effectiveSection, refresh]);

    const runRcon = async (label: string, fn: () => Promise<{ success: boolean; error?: string; output?: string }>) => {
        const res = await fn();
        if (!res.success) {
            showToast(`${label}: ${friendlyRconError(res.error, 'failed')}`, false);
        } else {
            showToast(`${label} ✓`, true);
            refresh(true);
        }
    };

    // ---- Action handlers ----

    const handleKick = (name: string) => {
        setConfirm({
            title: `Kick ${name}?`,
            message: `Disconnect ${name} from the server. They can rejoin immediately.`,
            onConfirm: () => runRcon('Kick', () => rconKick(serverId, name)),
        });
    };
    const handleBan = (name: string) => {
        setConfirm({
            title: `Ban ${name}?`,
            message: `Permanently ban ${name}. Adds an entry to banned-players.json.`,
            danger: true,
            onConfirm: () => runRcon('Ban', () => rconBan(serverId, name)),
        });
    };
    const handleUnban = (name: string) => {
        setConfirm({
            title: `Unban ${name}?`,
            message: `Remove ${name} from banned-players.json.`,
            onConfirm: () => runRcon('Unban', () => rconUnban(serverId, name)),
        });
    };
    const handleOp = (name: string) => runRcon('Op', () => rconOp(serverId, name));
    const handleDeop = (name: string) => {
        setConfirm({
            title: `De-op ${name}?`,
            message: `Removes operator privileges from ${name}.`,
            onConfirm: () => runRcon('De-op', () => rconDeop(serverId, name)),
        });
    };
    const handleWhitelistAdd = (name: string) => runRcon('Whitelist add', () => rconWhitelistAdd(serverId, name));
    const handleWhitelistRemove = (name: string) => {
        setConfirm({
            title: `Remove ${name} from whitelist?`,
            message: `${name} will be kicked when whitelist is enforced.`,
            onConfirm: () => runRcon('Whitelist remove', () => rconWhitelistRemove(serverId, name)),
        });
    };

    // ---- Current list (filtered by search) ----

    const currentList = useMemo(() => {
        const q = search.trim().toLowerCase();
        const filter = <T extends { name: string }>(rows: T[]) =>
            q ? rows.filter(p => p.name.toLowerCase().includes(q)) : rows;
        if (effectiveSection === 'online') return filter(online);
        if (effectiveSection === 'bans') return filter(bans);
        if (effectiveSection === 'whitelist') return filter(whitelist);
        if (effectiveSection === 'ops') return filter(ops);
        return [];
    }, [effectiveSection, search, online, bans, whitelist, ops]);

    if (!server) return null;

    return (
        <main className="flex-1 flex flex-col p-6 gap-4 overflow-hidden">
            <header className="flex items-center gap-3 shrink-0">
                <Users size={20} className="text-(--accent-light)" />
                <h1 className="text-base font-display font-semibold text-(--base-09)">Players</h1>
                {effectiveSection === 'online' && (
                    <span className="text-xs text-(--base-06)">Online list polls every 10s</span>
                )}
                {effectiveSection !== 'rcon' && (
                    <div className="ml-auto flex items-center gap-2">
                        <div className="relative">
                            <Search size={12} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-(--base-05)" />
                            <input
                                type="text"
                                value={search}
                                onChange={e => setSearch(e.target.value)}
                                placeholder="Search…"
                                className="input-field pl-7 w-48 text-xs py-1"
                            />
                        </div>
                        {/* Not onClick={refresh}: the handler would receive the
                            MouseEvent as `background` and every manual refresh
                            would silently become a background one. */}
                        <button onClick={() => refresh()} className="btn btn-secondary btn-sm" disabled={loading}>
                            <RefreshCw size={12} className={loading ? 'animate-spin' : ''} />
                            Refresh
                        </button>
                    </div>
                )}
            </header>

            {/* Section strip */}
            <nav className="flex gap-1 shrink-0 border-b border-(--base-03)">
                {SECTIONS.map(({ id, label, Icon }) => {
                    const locked = !rconEnabled && RCON_DEPENDENT.includes(id);
                    const active = effectiveSection === id;
                    return (
                        <button
                            key={id}
                            onClick={() => { if (!locked) setSection(id); }}
                            disabled={locked}
                            title={locked ? 'Enable RCON to unlock this section' : undefined}
                            className={`flex items-center gap-1.5 px-3 py-2 text-xs font-medium border-b-2 transition-colors ${
                                active
                                    ? 'border-(--accent) text-(--accent-light)'
                                    : locked
                                        ? 'border-transparent text-(--base-05) opacity-50 cursor-not-allowed'
                                        : 'border-transparent text-(--base-07) hover:text-(--base-09) hover:border-(--base-04)'
                            }`}
                        >
                            <Icon size={12} />
                            {label}
                            {locked && <Lock size={10} className="opacity-70" />}
                            {id === 'online' && !locked && online.length > 0 && (
                                <span className="ml-1 mono-label bg-(--base-03) px-1 rounded-sm">{online.length}</span>
                            )}
                        </button>
                    );
                })}
            </nav>

            {actionError && (
                <div className="shrink-0 flex items-start gap-2 px-3 py-2 rounded-md bg-(--error-ghost) border border-(--error)/30 text-(--error-light) text-sm">
                    <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                    <span>{actionError}</span>
                </div>
            )}

            {/* Content */}
            <div className="flex-1 overflow-y-auto">
                {!rconLoaded ? (
                    <div className="space-y-1.5">
                        {Array.from({ length: 4 }).map((_, i) => (
                            <Skeleton key={i} className="h-12 rounded-md" />
                        ))}
                    </div>
                ) : effectiveSection === 'rcon' ? (
                    <div className="space-y-3">
                        {!rconEnabled && (
                            <div className="flex items-start gap-2 px-3 py-2 rounded-md bg-(--accent-ghost) border border-(--accent)/30 text-(--base-08) text-xs">
                                <Lock size={13} className="shrink-0 mt-0.5 text-(--accent-light)" />
                                <span>
                                    RCON is off. Enable it below to unlock Online, Bans, Whitelist and Operators — live
                                    player management runs over RCON.
                                </span>
                            </div>
                        )}
                        <RconConfigCard serverId={serverId} onEnabledChange={setRconEnabled} />
                    </div>
                ) : (
                    <>
                {/* "Add to whitelist/ops" affordance for those sections */}
                {(effectiveSection === 'whitelist' || effectiveSection === 'ops') && (
                    <button
                        onClick={() => setAddPrompt({ target: effectiveSection as 'whitelist' | 'ops', name: '' })}
                        className="btn btn-secondary btn-sm mb-3"
                    >
                        <ListPlus size={12} />
                        Add player to {effectiveSection === 'whitelist' ? 'whitelist' : 'operators'}
                    </button>
                )}

                {loading ? (
                    <div className="space-y-1.5">
                        {Array.from({ length: 6 }).map((_, i) => (
                            <article key={i} className="card p-2 flex items-center gap-3">
                                <SkeletonCircle size="w-8 h-8 rounded-sm" />
                                <div className="min-w-0 flex-1">
                                    <SkeletonText width="w-32" className="h-3.5" />
                                </div>
                                <div className="flex items-center gap-1">
                                    <Skeleton className="w-7 h-7 rounded-md" />
                                    <Skeleton className="w-7 h-7 rounded-md" />
                                    <Skeleton className="w-7 h-7 rounded-md" />
                                </div>
                            </article>
                        ))}
                    </div>
                ) : currentList.length === 0 ? (
                    <div className="text-center py-12 text-sm text-(--base-06)">
                        {section === 'online' ? 'Nobody is online.' : 'No entries.'}
                    </div>
                ) : (
                    <div className="space-y-1.5">
                        {currentList.map((p: any) => (
                            <article key={`${section}-${p.name}`} className="card p-2 flex items-center gap-3">
                                {/* Player head */}
                                <img
                                    src={`https://cravatar.eu/helmavatar/${encodeURIComponent(p.name)}/32.png`}
                                    alt={p.name}
                                    className="w-8 h-8 rounded-sm shrink-0 bg-(--base-03)"
                                    style={{ imageRendering: 'pixelated' }}
                                    onError={(e) => { (e.target as HTMLImageElement).style.opacity = '0.3'; }}
                                />
                                <div className="min-w-0 flex-1">
                                    <div className="text-sm font-medium text-(--base-09)">{p.name}</div>
                                    {section === 'bans' && (p as JsonPlayerEntry).reason && (
                                        <div className="text-xs text-(--base-06) truncate">Reason: {(p as JsonPlayerEntry).reason}</div>
                                    )}
                                </div>
                                {/* Per-section actions */}
                                <div className="flex items-center gap-1">
                                    {section === 'online' && (
                                        <>
                                            <button onClick={() => setTellPrompt({ player: p.name, message: '' })} className="btn btn-secondary btn-sm" title="Whisper">
                                                <MessageSquare size={12} />
                                            </button>
                                            <button onClick={() => handleOp(p.name)} className="btn btn-secondary btn-sm" title="Op">
                                                <ShieldCheck size={12} className="text-(--accent-light)" />
                                            </button>
                                            <button onClick={() => handleKick(p.name)} className="btn btn-secondary btn-sm" title="Kick">
                                                <ShieldOff size={12} />
                                            </button>
                                            <button onClick={() => handleBan(p.name)} className="btn btn-secondary btn-sm" title="Ban">
                                                <Skull size={12} className="text-(--error)" />
                                            </button>
                                        </>
                                    )}
                                    {section === 'bans' && (
                                        <button onClick={() => handleUnban(p.name)} className="btn btn-secondary btn-sm">
                                            <CircleCheck size={12} className="text-(--success-light)" />
                                            Unban
                                        </button>
                                    )}
                                    {section === 'whitelist' && (
                                        <button onClick={() => handleWhitelistRemove(p.name)} className="btn btn-secondary btn-sm" title="Remove">
                                            <Trash2 size={12} className="text-(--error)" />
                                        </button>
                                    )}
                                    {section === 'ops' && (
                                        <button onClick={() => handleDeop(p.name)} className="btn btn-secondary btn-sm" title="De-op">
                                            <Crown size={12} className="text-(--error)" />
                                        </button>
                                    )}
                                </div>
                            </article>
                        ))}
                    </div>
                )}
                    </>
                )}
            </div>

            {/* Confirm modal */}
            {confirm && (
                <div className="modal-overlay animate-fade-in" onClick={() => setConfirm(null)}>
                    <div className="modal-panel max-w-sm" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className={`modal-title flex items-center gap-2 ${confirm.danger ? 'text-(--error-light)' : ''}`}>
                                {confirm.danger && <AlertTriangle size={18} />}
                                {confirm.title}
                            </h3>
                        </div>
                        <div className="modal-body">
                            <p className="text-sm text-(--base-07)">{confirm.message}</p>
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setConfirm(null)} className="btn btn-secondary">Cancel</button>
                            <button
                                onClick={async () => {
                                    const fn = confirm.onConfirm;
                                    setConfirm(null);
                                    await fn();
                                }}
                                className={confirm.danger ? 'btn btn-danger' : 'btn btn-primary'}
                            >
                                Confirm
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {/* Tell prompt */}
            {tellPrompt && (
                <div className="modal-overlay animate-fade-in" onClick={() => setTellPrompt(null)}>
                    <div className="modal-panel max-w-sm" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2">
                                <MessageSquare size={16} />
                                Whisper to {tellPrompt.player}
                            </h3>
                            <button onClick={() => setTellPrompt(null)} className="text-(--base-06)"><X size={16} /></button>
                        </div>
                        <div className="modal-body">
                            <input
                                type="text"
                                value={tellPrompt.message}
                                onChange={e => setTellPrompt({ ...tellPrompt, message: e.target.value })}
                                placeholder="Message…"
                                className="input-field w-full"
                                maxLength={200}
                                autoFocus
                            />
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setTellPrompt(null)} className="btn btn-secondary">Cancel</button>
                            <button
                                onClick={async () => {
                                    const msg = tellPrompt.message.trim();
                                    const target = tellPrompt.player;
                                    setTellPrompt(null);
                                    if (msg) await runRcon('Whisper', () => rconTell(serverId, target, msg));
                                }}
                                className="btn btn-primary"
                                disabled={!tellPrompt.message.trim()}
                            >
                                <Send size={12} />
                                Send
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {/* Add to whitelist/ops */}
            {addPrompt && (
                <div className="modal-overlay animate-fade-in" onClick={() => setAddPrompt(null)}>
                    <div className="modal-panel max-w-sm" onClick={e => e.stopPropagation()}>
                        <div className="modal-header">
                            <h3 className="modal-title flex items-center gap-2">
                                <ListPlus size={16} />
                                Add to {addPrompt.target === 'whitelist' ? 'whitelist' : 'operators'}
                            </h3>
                            <button onClick={() => setAddPrompt(null)} className="text-(--base-06)"><X size={16} /></button>
                        </div>
                        <div className="modal-body">
                            <input
                                type="text"
                                value={addPrompt.name}
                                onChange={e => setAddPrompt({ ...addPrompt, name: e.target.value })}
                                placeholder="Player name"
                                className="input-field w-full"
                                maxLength={32}
                                autoFocus
                            />
                        </div>
                        <div className="modal-footer">
                            <button onClick={() => setAddPrompt(null)} className="btn btn-secondary">Cancel</button>
                            <button
                                onClick={async () => {
                                    const name = addPrompt.name.trim();
                                    const target = addPrompt.target;
                                    setAddPrompt(null);
                                    if (!name) return;
                                    if (target === 'whitelist') await handleWhitelistAdd(name);
                                    else await handleOp(name);
                                }}
                                className="btn btn-primary"
                                disabled={!addPrompt.name.trim()}
                            >
                                Add
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {toast && (
                <div className="toast-container">
                    <div className="toast">
                        <div className={`toast-bar ${toast.ok ? 'bg-(--success-light)' : 'bg-(--error-light)'}`}></div>
                        {toast.ok ? <CircleCheck size={14} /> : <CircleAlert size={14} />}
                        <span className="text-sm text-(--base-09)">{toast.msg}</span>
                    </div>
                </div>
            )}
        </main>
    );
}
