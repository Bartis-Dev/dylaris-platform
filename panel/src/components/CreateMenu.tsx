"use client";

import { useState, useEffect, useRef } from 'react';
import { Plus, Server, HardDrive, Globe, ArrowRight, Lock } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { getNodes } from '@/lib/api';
import { serverOption, nodeOption, routeOption, hasAnyCreateOption, type CreateOption, type CreateOptionsInput } from '@/lib/createOptions';
import AddNodeModal from '@/components/AddNodeModal';

// ---------------------------------------------------------------------------
// The sidebar's "+" menu.
//
// Replaces a bare "New Container" button that was rendered for everyone,
// including users who had nowhere to deploy: they could click it, fill in a
// wizard, and only then be told "No node available". Now each entry states up
// front whether it is usable and, when it is not, why and where to go.
// ---------------------------------------------------------------------------

function MenuEntry({
    icon, title, subtitle, option, onClick, onNavigate,
}: {
    icon: React.ReactNode;
    title: string;
    subtitle: string;
    option: CreateOption;
    onClick?: () => void;
    onNavigate: (href: string) => void;
}) {
    const body = (
        <>
            <div className={`w-8 h-8 rounded-md flex items-center justify-center shrink-0 ${
                option.enabled ? 'bg-(--accent-ghost) text-(--accent-light)' : 'bg-(--base-03) text-(--base-06)'
            }`}>
                {option.enabled ? icon : <Lock size={14} />}
            </div>
            <div className="min-w-0 flex-1 text-left">
                <div className={`text-sm font-medium ${option.enabled ? 'text-(--base-09)' : 'text-(--base-07)'}`}>
                    {title}
                </div>
                <div className="text-xs text-(--base-06) leading-snug">
                    {option.enabled ? subtitle : option.reason}
                </div>
                {/* The link is only useful on a BLOCKED entry: it is the way out
                    of the block. On an enabled entry the button itself is the
                    action, and a second target would just compete with it. */}
                {!option.enabled && option.href && (
                    <button
                        type="button"
                        onClick={e => { e.stopPropagation(); onNavigate(option.href!); }}
                        className="mt-1.5 inline-flex items-center gap-1 text-xs text-(--accent-light) hover:underline"
                    >
                        {option.hrefLabel || 'Learn more'} <ArrowRight size={11} />
                    </button>
                )}
            </div>
        </>
    );

    if (!option.enabled) {
        return <div className="flex items-start gap-3 px-3 py-2.5 opacity-90 cursor-default">{body}</div>;
    }
    return (
        <button
            type="button"
            onClick={onClick}
            className="w-full flex items-start gap-3 px-3 py-2.5 hover:bg-(--base-03) transition-colors rounded-md"
        >
            {body}
        </button>
    );
}

export default function CreateMenu({ onNewServer }: { onNewServer?: () => void }) {
    const { user, featureFlags, entitlement, gatewayEnabled } = useAppData();
    const [open, setOpen] = useState(false);
    const [showAddNode, setShowAddNode] = useState(false);
    const [deployableNodes, setDeployableNodes] = useState<number | null>(null);
    const wrapRef = useRef<HTMLDivElement>(null);

    const isAdmin = user?.isAdmin ?? false;

    // How many nodes this caller could actually deploy on. /api/nodes is already
    // owner-scoped for a BYON tenant, so the count is theirs, not the fleet's.
    // Only fetched when it can change the answer: with BYON off the server entry
    // is enabled regardless, and an admin is never blocked on it.
    useEffect(() => {
        if (!featureFlags.byon || isAdmin) {
            setDeployableNodes(null);
            return;
        }
        let cancelled = false;
        getNodes().then(res => {
            if (cancelled) return;
            setDeployableNodes(res.success && Array.isArray(res.nodes) ? res.nodes.length : 0);
        });
        return () => { cancelled = true; };
    }, [featureFlags.byon, isAdmin]);

    useEffect(() => {
        const handler = (e: MouseEvent) => {
            if (!wrapRef.current?.contains(e.target as Node)) setOpen(false);
        };
        document.addEventListener('click', handler);
        return () => document.removeEventListener('click', handler);
    }, []);

    const input: CreateOptionsInput = {
        isAdmin,
        byonEnabled: featureFlags.byon,
        // entitlement is null until the first fetch lands. Treating that as
        // "entitled" would flash an option the user may not have; treating it as
        // "not entitled" only ever shows a reason that corrects itself a moment
        // later, which is the safer of the two wrong states.
        entitledByon: entitlement?.byon ?? false,
        deployableNodes: deployableNodes ?? 0,
        storeEnabled: featureFlags.store,
        gatewayEnabled,
        entitledRouteOnly: entitlement?.routeOnly ?? false,
    };

    const srv = serverOption(input);
    const node = nodeOption(input);
    const route = routeOption(input);

    // Nothing usable and nothing to explain: hide the control rather than offer a
    // button that only ever says no.
    if (!hasAnyCreateOption(input) && !featureFlags.byon && !gatewayEnabled) return null;

    // Admins get instructions, not a destination: adding a node happens on the
    // host. The old entry navigated to /settings?tab=warp, a query shape the
    // settings routes do not read, so it landed on the first tab in the list and
    // left the admin somewhere unrelated.
    const onNodeClick = () => {
        setOpen(false);
        if (isAdmin) { setShowAddNode(true); return; }
        navigate(node.href || '/nodes');
    };

    const navigate = (href: string) => {
        setOpen(false);
        if (typeof window !== 'undefined') window.location.href = href;
    };

    return (
        <div ref={wrapRef} className="relative shrink-0 border-t border-(--base-03)">
            <button
                type="button"
                onClick={() => setOpen(v => !v)}
                aria-expanded={open}
                aria-haspopup="menu"
                className={`btn w-full px-4 py-3 text-sm bg-transparent border-0 justify-start transition-colors ${
                    open ? 'bg-(--base-03) text-(--base-09)' : 'text-(--accent-light) hover:bg-(--base-03)'
                }`}
            >
                <Plus size={16} />
                Create
            </button>

            {open && (
                <div className="absolute bottom-full left-2 right-2 mb-2 z-40 dropdown-menu p-1.5 animate-fade-in" role="menu">
                    <MenuEntry
                        icon={<Server size={15} />}
                        title="New server"
                        subtitle="A Minecraft server on a node you can deploy to."
                        option={srv}
                        onClick={() => { setOpen(false); onNewServer?.(); }}
                        onNavigate={navigate}
                    />
                    <MenuEntry
                        icon={<HardDrive size={15} />}
                        title={isAdmin ? 'Add a node' : 'Add your own machine'}
                        subtitle={isAdmin
                            ? 'How to join a machine to the fleet.'
                            : 'Run servers on your own hardware, connected through the overlay.'}
                        option={node}
                        onClick={onNodeClick}
                        onNavigate={navigate}
                    />
                    {/* Only where routing exists at all - otherwise the entry is
                        an offer nothing on this platform can fulfil. */}
                    {gatewayEnabled && (
                        <MenuEntry
                            icon={<Globe size={15} />}
                            title="Add a route"
                            subtitle="A protected address for a Minecraft server you run yourself."
                            option={route}
                            onClick={() => navigate('/routes')}
                            onNavigate={navigate}
                        />
                    )}
                </div>
            )}

            {showAddNode && <AddNodeModal onClose={() => setShowAddNode(false)} />}
        </div>
    );
}
