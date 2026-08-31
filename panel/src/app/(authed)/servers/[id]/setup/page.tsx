"use client";

import { useAppData } from '@/lib/AppDataContext';
import SetupView from '@/views/SetupView';
import { useRouteId } from '@/lib/routeParams';

export default function ServerSetupPage() {
    const paramId = useRouteId('servers');
    const { servers, libraryEnabled, refreshServers } = useAppData();
    const server = servers.find(s => s.id === Number(paramId));
    if (!server) return null;
    return <SetupView server={server} libraryEnabled={libraryEnabled} onSetupComplete={refreshServers} />;
}
