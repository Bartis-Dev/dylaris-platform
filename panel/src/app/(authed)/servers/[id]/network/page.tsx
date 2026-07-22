"use client";

import { useParams, useRouter } from 'next/navigation';
import { useAppData } from '@/lib/AppDataContext';
import NetworkView from '@/views/NetworkView';

// RCON configuration moved to the Players tab's RCON sub-section (it gates live
// player management), so the Network tab is now purely proxy/endpoint wiring.
export default function ServerNetworkPage() {
    const params = useParams();
    const router = useRouter();
    const { servers, refreshServers } = useAppData();
    const server = servers.find(s => s.id === Number(params?.id));
    if (!server) return null;
    return (
        <div className="space-y-6">
            <NetworkView
                server={server}
                allServers={servers}
                onServerSelect={(id) => router.push(`/servers/${id}`)}
                onRefreshServers={refreshServers}
            />
        </div>
    );
}
