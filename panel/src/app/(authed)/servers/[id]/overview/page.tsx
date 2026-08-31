"use client";

import { useAppData } from '@/lib/AppDataContext';
import OverviewView from '@/views/OverviewView';
import { useRouteId } from '@/lib/routeParams';

export default function ServerOverviewPage() {
    const paramId = useRouteId('servers');
    const { servers } = useAppData();
    const server = servers.find(s => s.id === Number(paramId));
    if (!server) return null;
    return <OverviewView server={server} />;
}
