"use client";

import React, { useState, useEffect, useMemo } from 'react';
import { getUsers, User, createServer, getNodes, Node } from '../lib/api';
import { X, Server, CircleCheck, Info, ArrowRight, Rocket, Network, HardDrive } from 'lucide-react';

interface StoragePathInfo {
    path: string;
    total_bytes: number;
    free_bytes: number;
    used_bytes: number;
    server_count: number;
}

async function fetchNodeStorage(nodeId: number): Promise<StoragePathInfo[]> {
    try {
        const token = localStorage.getItem('authToken') || localStorage.getItem('token');
        const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:25500/api';
        const res = await fetch(`${API_URL}/nodes/${nodeId}/storage`, {
            headers: { Authorization: `Bearer ${token}` },
        });
        const data = await res.json();
        return data.success ? (data.storage || []) : [];
    } catch {
        return [];
    }
}

function fmtGB(bytes: number): string {
    return (bytes / (1024 * 1024 * 1024)).toFixed(1);
}

interface CreateServerWizardProps {
    isOpen: boolean;
    onClose: () => void;
    proxiesEnabled?: boolean;
}

export default function CreateServerWizard({ isOpen, onClose, proxiesEnabled = true }: CreateServerWizardProps) {
    const [step, setStep] = useState(1);
    const [loading, setLoading] = useState(false);
    const [users, setUsers] = useState<User[]>([]);
    const [nodes, setNodes] = useState<Node[]>([]);
    const [searchTerm, setSearchTerm] = useState("");

    const [nodeId, setNodeId] = useState("");
    const [nodeSearch, setNodeSearch] = useState("");
    const [activeTagFilters, setActiveTagFilters] = useState<string[]>([]);

    const [ownerId, setOwnerId] = useState<number | null>(null);
    const [serverType, setServerType] = useState<'game' | 'proxy'>('game');
    const [ram, setRam] = useState(2048);
    const [cpuLimit, setCpuLimit] = useState(0);
    const [diskLimit, setDiskLimit] = useState(20);
    const [storagePath, setStoragePath] = useState('auto');
    const [storagePaths, setStoragePaths] = useState<StoragePathInfo[]>([]);

    useEffect(() => {
        if (!isOpen) return;
        setStep(1);
        setNodeId(""); setOwnerId(null); setServerType('game'); setRam(2048); setCpuLimit(0); setDiskLimit(20);
        setSearchTerm(""); setNodeSearch(""); setActiveTagFilters([]);

        getUsers().then(res => {
            if (res.success && res.users) {
                setUsers(res.users);
                if (res.users.length > 0) setOwnerId(res.users[0].id);
            }
        });

        getNodes().then(res => {
            if (res.success && res.nodes) {
                setNodes(res.nodes);
                const online = res.nodes.filter((n: Node) => n.status === 'online');
                if (online.length > 0) setNodeId(String(online[0].id));
            }
        });
    }, [isOpen]);

    // Load storage paths when node changes
    useEffect(() => {
        if (!nodeId) { setStoragePaths([]); return; }
        fetchNodeStorage(Number(nodeId)).then(setStoragePaths);
        setStoragePath('auto');
    }, [nodeId]);

    const onlineNodes = useMemo(() => nodes.filter(n => n.status === 'online'), [nodes]);

    const allTags = useMemo(() =>
        [...new Set(onlineNodes.flatMap(n => (n.tags || '').split(',').map(t => t.trim()).filter(Boolean)))],
        [onlineNodes]
    );

    const filteredNodes = useMemo(() =>
        onlineNodes
            .filter(n => !nodeSearch || n.name.toLowerCase().includes(nodeSearch.toLowerCase()) || n.address.includes(nodeSearch))
            .filter(n => activeTagFilters.length === 0 || activeTagFilters.every(tag => (n.tags || '').split(',').map(t => t.trim()).includes(tag))),
        [onlineNodes, nodeSearch, activeTagFilters]
    );

    const toggleTagFilter = (tag: string) => {
        setActiveTagFilters(prev => prev.includes(tag) ? prev.filter(t => t !== tag) : [...prev, tag]);
    };

    const filteredUsers = useMemo(() =>
        users.filter(u => u.username.toLowerCase().includes(searchTerm.toLowerCase())),
        [users, searchTerm]
    );

    const handleCreate = async () => {
        setLoading(true);
        if (!nodeId) { alert("Please select a Node."); setLoading(false); return; }
        if (!ownerId) { alert("Please select an owner."); setLoading(false); return; }

        const randomPart = Math.random().toString(36).substring(2, 14);
        const serverUuid = `${ownerId}_${randomPart}`;

        const payload: Record<string, unknown> = {
            uuid: serverUuid,
            name: "",
            nodeId,
            ownerId,
            serverType,
            docker: { ram, cpuLimit, diskLimit: diskLimit > 0 ? diskLimit * 1024 : 0 },
        };
        if (storagePath !== 'auto') {
            payload.storagePath = storagePath;
        }

        const result = await createServer(payload);
        if (result.success) {
            onClose();
        } else {
            alert("Error: " + result.message);
        }
        setLoading(false);
    };

    if (!isOpen) return null;

    return (
        <div className="modal-overlay animate-fade-in">
            <div className="modal-panel w-full max-w-4xl flex flex-col max-h-[90vh]">

                {/* Header */}
                <div className="modal-header flex justify-between items-center">
                    <div>
                        <h2 className="modal-title text-2xl">Deploy Server</h2>
                        <p className="input-label mt-1">Step {step} of 2 — Software setup happens after deployment</p>
                    </div>
                    <button onClick={onClose} className="text-(--base-06) hover:text-(--error-light) transition-colors">
                        <X size={24} />
                    </button>
                </div>

                {/* Progress Bar */}
                <div className="flex w-full h-0.5 bg-(--base-03)">
                    <div className="h-full bg-(--accent) transition-all duration-300" style={{ width: `${step * 50}%` }} />
                </div>

                {/* Body */}
                <div className="modal-body overflow-y-auto flex-1 p-8">
                    <form onSubmit={(e) => e.preventDefault()}>

                        {step === 1 && (
                            <div className="space-y-5 animate-fade-in">
                                {/* Server Type Selection */}
                                <h3 className="text-base font-display font-bold text-(--base-09) border-b border-(--base-03) pb-2">Server Type</h3>
                                <div className={`grid gap-3 ${proxiesEnabled ? 'grid-cols-2' : 'grid-cols-1'}`}>
                                    <button
                                        type="button"
                                        onClick={() => setServerType('game')}
                                        className={`card text-left p-4 cursor-pointer transition-all flex items-center gap-3 ${
                                            serverType === 'game'
                                                ? 'border-(--accent-border) bg-(--accent-ghost)'
                                                : 'hover:border-(--base-05)'
                                        }`}
                                    >
                                        <div className="w-10 h-10 rounded-md bg-(--base-03) flex items-center justify-center">
                                            <Server size={20} className="text-(--primary-light)" />
                                        </div>
                                        <div>
                                            <div className="font-medium text-sm text-(--base-09)">Game Server</div>
                                            <div className="text-xs text-(--base-06)">Minecraft, Paper, Forge, etc.</div>
                                        </div>
                                        {serverType === 'game' && <CircleCheck size={16} className="text-(--accent-light) ml-auto" />}
                                    </button>
                                    {proxiesEnabled && (
                                        <button
                                            type="button"
                                            onClick={() => setServerType('proxy')}
                                            className={`card text-left p-4 cursor-pointer transition-all flex items-center gap-3 ${
                                                serverType === 'proxy'
                                                    ? 'border-(--accent-border) bg-(--accent-ghost)'
                                                    : 'hover:border-(--base-05)'
                                            }`}
                                        >
                                            <div className="w-10 h-10 rounded-md bg-(--base-03) flex items-center justify-center">
                                                <Network size={20} className="text-(--accent-light)" />
                                            </div>
                                            <div>
                                                <div className="font-medium text-sm text-(--base-09)">Proxy Server</div>
                                                <div className="text-xs text-(--base-06)">BungeeCord, Waterfall, Velocity</div>
                                            </div>
                                            {serverType === 'proxy' && <CircleCheck size={16} className="text-(--accent-light) ml-auto" />}
                                        </button>
                                    )}
                                </div>

                                <h3 className="text-base font-display font-bold text-(--base-09) border-b border-(--base-03) pb-2">1. Target Node</h3>

                                <div className="space-y-3">
                                    <input
                                        autoFocus
                                        type="text"
                                        placeholder="Search nodes by name or address..."
                                        value={nodeSearch}
                                        onChange={e => setNodeSearch(e.target.value)}
                                        className="input-field w-full"
                                    />
                                    {allTags.length > 0 && (
                                        <div className="flex flex-wrap gap-2">
                                            {allTags.map(tag => (
                                                <button
                                                    key={tag}
                                                    type="button"
                                                    onClick={() => toggleTagFilter(tag)}
                                                    className={`badge transition-all ${
                                                        activeTagFilters.includes(tag)
                                                            ? 'bg-(--accent) text-white border-(--accent)'
                                                            : 'badge-accent hover:bg-(--accent)/20'
                                                    }`}
                                                >
                                                    {tag}
                                                </button>
                                            ))}
                                        </div>
                                    )}
                                </div>

                                <div className="grid grid-cols-2 lg:grid-cols-3 gap-3 max-h-64 overflow-y-auto pr-1">
                                    {filteredNodes.length === 0 ? (
                                        <div className="col-span-full text-center py-8 text-(--base-06)">
                                            <Server size={30} className="mb-2 block opacity-40 mx-auto" />
                                            <p className="text-sm">No online nodes found{nodeSearch || activeTagFilters.length > 0 ? ' matching your filters' : ' — add a Node in Settings first'}.</p>
                                        </div>
                                    ) : (
                                        filteredNodes.map(node => {
                                            const isSelected = nodeId === String(node.id);
                                            const tags = (node.tags || '').split(',').map(t => t.trim()).filter(Boolean);
                                            return (
                                                <button
                                                    key={node.id}
                                                    type="button"
                                                    onClick={() => setNodeId(String(node.id))}
                                                    className={`card text-left p-4 cursor-pointer transition-all ${
                                                        isSelected
                                                            ? 'border-(--accent-border) bg-(--accent-ghost)'
                                                            : 'hover:border-(--base-05)'
                                                    }`}
                                                >
                                                    <div className="flex items-center justify-between mb-1">
                                                        <span className="font-medium text-sm truncate text-(--base-09)">{node.name}</span>
                                                        {isSelected && <CircleCheck size={14} className="text-(--accent-light)" />}
                                                    </div>
                                                    {node.serverCount !== undefined && (
                                                        <p className="text-xs text-(--base-06) mb-2">{node.serverCount} server{node.serverCount !== 1 ? 's' : ''}</p>
                                                    )}
                                                    {tags.length > 0 && (
                                                        <div className="flex flex-wrap gap-1">
                                                            {tags.map(tag => (
                                                                <span key={tag} className="badge badge-accent text-[9px]">
                                                                    {tag}
                                                                </span>
                                                            ))}
                                                        </div>
                                                    )}
                                                </button>
                                            );
                                        })
                                    )}
                                </div>
                            </div>
                        )}

                        {step === 2 && (
                            <div className="space-y-5 animate-fade-in">
                                <h3 className="text-base font-display font-bold text-(--base-09) border-b border-(--base-03) pb-2">2. Resources & Owner</h3>

                                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                    {/* Left column: Resources */}
                                    <div className="space-y-4">
                                        <h4 className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">Resources</h4>

                                        <div className="flex flex-col gap-[5px]">
                                            <label className="input-label">RAM Limit (MB)</label>
                                            <div className="relative">
                                                <input
                                                    type="number"
                                                    value={ram}
                                                    onChange={e => setRam(Number(e.target.value))}
                                                    className="input-field w-full [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                                                />
                                                <span className="absolute right-3 top-[9px] text-(--base-06) font-mono text-sm pointer-events-none">MB</span>
                                            </div>
                                            <p className="text-xs text-(--base-06)">+512 MB OOM buffer added automatically</p>
                                        </div>

                                        <div className="flex flex-col gap-[5px]">
                                            <label className="input-label">CPU Limit (Cores)</label>
                                            <div className="relative">
                                                <input
                                                    type="number"
                                                    step="0.5"
                                                    value={cpuLimit}
                                                    onChange={e => setCpuLimit(Number(e.target.value))}
                                                    className="input-field w-full [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                                                />
                                                <span className="absolute right-3 top-[9px] text-(--base-06) font-mono text-sm pointer-events-none">Cores</span>
                                            </div>
                                            <p className="text-xs text-(--base-06)">0 = no limit</p>
                                        </div>

                                        <div className="flex flex-col gap-[5px]">
                                            <label className="input-label">Storage Limit (GB)</label>
                                            <div className="relative">
                                                <input
                                                    type="number"
                                                    min={0}
                                                    step={1}
                                                    value={diskLimit}
                                                    onChange={e => setDiskLimit(Number(e.target.value))}
                                                    className="input-field w-full [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                                                />
                                                <span className="absolute right-3 top-[9px] text-(--base-06) font-mono text-sm pointer-events-none">GB</span>
                                            </div>
                                            <p className="text-xs text-(--base-06)">0 = unlimited</p>
                                        </div>

                                        {/* Storage Path Selection */}
                                        {storagePaths.length > 1 && (
                                            <div className="flex flex-col gap-[5px]">
                                                <label className="input-label flex items-center gap-1.5">
                                                    <HardDrive size={13} /> Storage Path
                                                </label>
                                                <select
                                                    value={storagePath}
                                                    onChange={e => setStoragePath(e.target.value)}
                                                    className="input-field"
                                                >
                                                    <option value="auto">Auto (most free space)</option>
                                                    {storagePaths.map(s => (
                                                        <option key={s.path} value={s.path}>
                                                            {s.path} ({fmtGB(s.free_bytes)} GB free, {s.server_count} servers)
                                                        </option>
                                                    ))}
                                                </select>
                                                <p className="text-xs text-(--base-06)">Auto distributes servers evenly across storage paths.</p>
                                            </div>
                                        )}
                                    </div>

                                    {/* Right column: Assign Owner */}
                                    <div className="space-y-2">
                                        <div className="flex items-center justify-between">
                                            <h4 className="font-mono text-[10px] uppercase tracking-[0.08em] text-(--base-06)">Assign Owner</h4>
                                            <span className="font-mono text-[10px] text-(--base-06)">
                                                {filteredUsers.length} / {users.length}
                                            </span>
                                        </div>
                                        <input
                                            type="text"
                                            placeholder="Search user..."
                                            value={searchTerm}
                                            onChange={e => setSearchTerm(e.target.value)}
                                            className="input-field w-full"
                                        />
                                        <div className="flex flex-col gap-1 max-h-[280px] overflow-y-auto rounded-md border border-(--base-03) bg-(--base-02) p-1.5">
                                            {filteredUsers.length === 0 ? (
                                                <p className="text-xs text-(--base-06) text-center py-6 italic">No matching users.</p>
                                            ) : (
                                                filteredUsers.map(user => {
                                                    const selected = ownerId === user.id;
                                                    return (
                                                        <button
                                                            key={user.id}
                                                            type="button"
                                                            onClick={() => setOwnerId(user.id)}
                                                            className={`flex items-center gap-2 px-2 py-1.5 rounded-md text-left transition-colors ${
                                                                selected
                                                                    ? 'bg-(--accent-ghost) border border-(--accent-border)'
                                                                    : 'border border-transparent hover:bg-(--base-03)'
                                                            }`}
                                                        >
                                                            <div className="w-7 h-7 rounded-md bg-(--base-03) flex items-center justify-center font-medium text-xs text-(--base-08) shrink-0">
                                                                {user.username.charAt(0).toUpperCase()}
                                                            </div>
                                                            <span className="flex-1 truncate text-sm text-(--base-09)">{user.username}</span>
                                                            {user.isAdmin && (
                                                                <span className="font-mono text-[9px] uppercase text-(--accent-light)">admin</span>
                                                            )}
                                                            {selected && <CircleCheck size={14} className="text-(--accent-light) shrink-0" />}
                                                        </button>
                                                    );
                                                })
                                            )}
                                        </div>
                                    </div>
                                </div>

                                <div className="bg-(--primary-ghost) p-3 rounded-lg border border-(--primary-border) text-sm text-(--base-07)">
                                    <Info size={14} className="inline align-middle mr-1 text-(--primary-light)" />
                                    Java version, Minecraft software, and server name are configured in the Setup tab after deployment.
                                </div>
                            </div>
                        )}
                    </form>
                </div>

                {/* Footer */}
                <div className="modal-footer">
                    {step > 1 ? (
                        <button onClick={() => setStep(step - 1)} className="btn btn-secondary px-6 py-2 text-sm">Back</button>
                    ) : (
                        <div></div>
                    )}

                    {step < 2 ? (
                        <button
                            onClick={() => {
                                if (!nodeId) return alert("Please select a node.");
                                setStep(2);
                            }}
                            className="btn btn-primary px-8 py-2 text-sm"
                        >
                            Next <ArrowRight size={18} className="ml-1" />
                        </button>
                    ) : (
                        <button
                            onClick={handleCreate}
                            disabled={loading}
                            className="btn px-8 py-2 text-sm bg-(--success) text-white border-(--success) hover:bg-(--success-light) disabled:opacity-50"
                        >
                            {loading ? 'Creating...' : 'CREATE SERVER'} <Rocket size={18} className="ml-1" />
                        </button>
                    )}
                </div>
            </div>
        </div>
    );
}
