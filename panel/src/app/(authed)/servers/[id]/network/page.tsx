"use client";

import { useParams, useRouter } from 'next/navigation';
import { useAppData } from '@/lib/AppDataContext';
import NetworkView from '@/views/NetworkView';
import RconConfigCard from '@/components/RconConfigCard';

export default function ServerNetworkPage() {
    const params = useParams();
    const router = useRouter();
    const { servers, refreshServers } = useAppData();
    const server = servers.find(s => s.id === Number(params?.id));
    if (!server) return null;
    return (
        <div className="space-y-6">
            <RconConfigCard serverId={server.id} />
            <NetworkView
                server={server}
                allServers={servers}
                onServerSelect={(id) => router.push(`/servers/${id}`)}
                onRefreshServers={refreshServers}
            />
        </div>
    );
}
