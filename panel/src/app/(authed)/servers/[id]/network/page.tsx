"use client";

import { useParams, useRouter } from 'next/navigation';
import { useAppData } from '@/lib/AppDataContext';
import NetworkView from '@/views/NetworkView';

export default function ServerNetworkPage() {
    const params = useParams();
    const router = useRouter();
    const { servers, refreshServers } = useAppData();
    const server = servers.find(s => s.id === Number(params?.id));
    if (!server) return null;
    return (
        <NetworkView
            server={server}
            allServers={servers}
            onServerSelect={(id) => router.push(`/servers/${id}`)}
            onRefreshServers={refreshServers}
        />
    );
}
