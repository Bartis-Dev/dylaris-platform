"use client";

import { useAppData } from '@/lib/AppDataContext';
import ConsoleView from '@/views/ConsoleView';
import { useRouteId } from '@/lib/routeParams';

export default function ServerConsolePage() {
    const paramId = useRouteId('servers');
    const { servers } = useAppData();
    const server = servers.find(s => s.id === Number(paramId));
    if (!server) return null;
    return <ConsoleView server={server} />;
}
