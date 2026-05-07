"use client";

import { useParams } from 'next/navigation';
import { useAppData } from '@/lib/AppDataContext';
import SetupView from '@/views/SetupView';

export default function ServerSetupPage() {
    const params = useParams();
    const { servers, libraryEnabled, refreshServers } = useAppData();
    const server = servers.find(s => s.id === Number(params?.id));
    if (!server) return null;
    return <SetupView server={server} libraryEnabled={libraryEnabled} onSetupComplete={refreshServers} />;
}
