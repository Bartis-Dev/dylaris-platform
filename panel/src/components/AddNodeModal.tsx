"use client";

import { useState } from 'react';
import { X, Server, Network, Copy, Check, ExternalLink, Info } from 'lucide-react';
import { useAppData } from '@/lib/AppDataContext';
import { coreOrigin } from '@/lib/api/core';

// ---------------------------------------------------------------------------
// "How do I add a node?" — the admin answer.
//
// The sidebar's "+ Create -> Add a node" used to navigate an admin to a
// settings tab and leave them there. Adding a node is a HOST-side operation:
// nothing in the panel performs it, the panel only shows the result. So the
// honest response to the button is the instructions, not a screen.
//
// Two shapes, and which ones exist depends on the install:
//   - Fleet node: a machine the operator owns, joined to the Swarm/compose
//     stack. Always available.
//   - External node: a machine reached over the warp overlay (a customer's or a
//     remote box). Only meaningful once the gateway subsystem is routing, so
//     that tab appears only then.
// ---------------------------------------------------------------------------

const enrollUrl = coreOrigin();

const FLEET_COMPOSE = `# Add this service to the stack you deploy on the new host.
# It joins the SAME overlay network as core and redis, so nothing is published.
services:
  node:
    image: ghcr.io/bartis-dev/dylaris-platform-node:latest
    restart: unless-stopped
    environment:
      NODE_ID: "<stable-id-for-this-machine>"
      # Single-use, first boot only. Mint it under Settings -> Nodes.
      NODE_ENROLL_TOKEN: "<enroll-token-from-panel>"
      CORE_GRPC_ADDR: "core:25501"
      REDIS_ADDR: "redis:6379"
      # No CLUSTER_SECRET: the node fetches a scoped Redis credential over gRPC
      # once it has enrolled. Handing it fleet credentials undoes that.
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /dev:/dev:ro
      - node_data:/app/dylaris_data
    networks: [dylaris_net]
    cap_add: [SYS_ADMIN]

volumes:
  node_data:
networks:
  dylaris_net:
    external: true`;

const FLEET_STEPS = `# 1. Deploy the stack on the new host (Swarm):
docker stack deploy -c docker-stack.yml dylaris

#    ...or with compose on a single host:
docker compose up -d node

# 2. Watch it enroll. The token is consumed on first success.
docker compose logs -f node

# 3. It appears under Settings -> Nodes within ~30s.`;

function CodeBlock({ code, label }: { code: string; label: string }) {
    const [copied, setCopied] = useState(false);
    return (
        <div>
            <div className="flex items-center justify-between mb-1.5">
                <span className="mono-label">{label}</span>
                <button
                    type="button"
                    onClick={async () => {
                        try {
                            await navigator.clipboard.writeText(code);
                            setCopied(true);
                            setTimeout(() => setCopied(false), 1800);
                        } catch { /* clipboard blocked; the text is selectable */ }
                    }}
                    className="btn btn-secondary btn-sm"
                >
                    {copied ? <><Check size={12} /> Copied</> : <><Copy size={12} /> Copy</>}
                </button>
            </div>
            <pre className="input-mono text-[11px] leading-relaxed bg-(--base-02) border border-(--base-03) rounded-md p-3 overflow-x-auto text-(--base-08) whitespace-pre">
                {code}
            </pre>
        </div>
    );
}

type NodeKind = 'fleet' | 'external';

export default function AddNodeModal({ onClose }: { onClose: () => void }) {
    const { gatewayEnabled } = useAppData();
    const [tab, setTab] = useState<NodeKind>('fleet');

    // An external node reaches Core over the warp overlay, which is part of the
    // gateway subsystem. With routing on ip_port there is no overlay deployed,
    // so offering the tab would document a path that does not exist here.
    const showExternal = gatewayEnabled;
    const active: NodeKind = showExternal ? tab : 'fleet';

    return (
        <div className="modal-overlay animate-fade-in" onClick={onClose}>
            <div className="modal-panel w-full max-w-3xl flex flex-col max-h-[88vh]" onClick={e => e.stopPropagation()}>
                <div className="modal-header flex items-start justify-between gap-4">
                    <div>
                        <h3 className="modal-title flex items-center gap-2">
                            <Server size={16} className="text-(--accent-light)" />
                            Add a node
                        </h3>
                        <p className="text-xs text-(--base-07) mt-1">
                            A node is added on the machine itself. The panel mints the token and shows the result.
                        </p>
                    </div>
                    <button onClick={onClose} className="p-1 text-(--base-06) hover:text-(--base-09) transition-colors">
                        <X size={18} />
                    </button>
                </div>

                {showExternal && (
                    <div className="px-6 pt-4 shrink-0">
                        <div className="flex gap-1 border-b border-(--base-03)">
                            {([
                                { id: 'fleet' as const, label: 'Fleet node', Icon: Server },
                                { id: 'external' as const, label: 'External node', Icon: Network },
                            ]).map(({ id, label, Icon }) => (
                                <button
                                    key={id}
                                    type="button"
                                    onClick={() => setTab(id)}
                                    className={`flex items-center gap-1.5 px-3 py-2 text-xs font-medium border-b-2 transition-colors ${
                                        active === id
                                            ? 'border-(--accent) text-(--accent-light)'
                                            : 'border-transparent text-(--base-07) hover:text-(--base-09) hover:border-(--base-04)'
                                    }`}
                                >
                                    <Icon size={12} />
                                    {label}
                                </button>
                            ))}
                        </div>
                    </div>
                )}

                <div className="modal-body flex-1 overflow-y-auto space-y-4">
                    {active === 'fleet' ? (
                        <>
                            <p className="text-sm text-(--base-07)">
                                A machine you own, on the same Docker network as Core and Redis. It needs no public
                                port and no overlay: it dials Core over the internal network.
                            </p>
                            <div className="alert alert-info text-xs">
                                <Info size={13} className="shrink-0 mt-0.5" />
                                <span>
                                    Mint the single-use enrollment token first under{' '}
                                    <a href="/settings/nodes" className="text-(--accent-light) hover:underline">
                                        Settings &rarr; Nodes
                                    </a>
                                    , then paste it into <code>NODE_ENROLL_TOKEN</code> below. It is consumed on the
                                    node&apos;s first successful connect.
                                </span>
                            </div>
                            <CodeBlock label="Stack service" code={FLEET_COMPOSE} />
                            <CodeBlock label="Deploy" code={FLEET_STEPS} />
                            <p className="text-xs text-(--base-06)">
                                Core is reachable at <code className="text-(--base-08)">{enrollUrl}</code>. If the new
                                host is not on the same overlay network, it is an external node — see the other tab.
                            </p>
                        </>
                    ) : (
                        <>
                            <p className="text-sm text-(--base-07)">
                                A machine anywhere else — a remote box or a customer&apos;s. It joins the warp overlay
                                with a mint-once key and dials Core through the tunnel, so it needs no public IP and no
                                port forwarding.
                            </p>
                            <div className="alert alert-info text-xs">
                                <Info size={13} className="shrink-0 mt-0.5" />
                                <span>
                                    The full kit — the warp key, the overlay addresses and the ready-made compose file
                                    with everything filled in — is generated under{' '}
                                    <a href="/settings/warp" className="text-(--accent-light) hover:underline inline-flex items-center gap-1">
                                        Settings &rarr; Warp &rarr; External nodes <ExternalLink size={10} />
                                    </a>
                                    . It cannot be shown here because the key is revealed exactly once, at mint time.
                                </span>
                            </div>
                            <ol className="text-sm text-(--base-07) space-y-2 list-decimal pl-5">
                                <li>
                                    In <span className="text-(--base-09)">Settings &rarr; Warp &rarr; External nodes</span>,
                                    mint a warp key for the region the machine should belong to.
                                </li>
                                <li>
                                    Choose the kit: <span className="text-(--base-09)">Node</span> to run Minecraft
                                    servers on that machine, or <span className="text-(--base-09)">Route-only</span> to
                                    give an already-running server a protected address without hosting it.
                                </li>
                                <li>Copy the generated compose file onto the machine and start it.</li>
                                <li>The node registers itself and appears under Settings &rarr; Nodes within ~30s.</li>
                            </ol>
                            <a href="/settings/warp" className="btn btn-primary btn-sm inline-flex w-fit">
                                Open Warp settings <ExternalLink size={12} />
                            </a>
                        </>
                    )}
                </div>

                <div className="modal-footer">
                    <button onClick={onClose} className="btn btn-secondary">Close</button>
                </div>
            </div>
        </div>
    );
}
