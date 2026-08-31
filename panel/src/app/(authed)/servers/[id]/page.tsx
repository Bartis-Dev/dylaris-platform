"use client";

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { useAppData } from '@/lib/AppDataContext';
import { useRouteId } from '@/lib/routeParams';

export default function ServerIndexPage() {
    const paramId = useRouteId('servers');
    const router = useRouter();
    const { servers, ready } = useAppData();

    useEffect(() => {
        if (!ready) return;
        const id = Number(paramId);
        const srv = servers.find(s => s.id === id);
        if (!srv) return; // Layout will show "not found"
        const target = srv.status === 'pending_setup' ? 'setup' : 'overview';
        router.replace(`/servers/${id}/${target}`);
    }, [ready, paramId, servers, router]);

    return (
        <div className="flex h-full items-center justify-center text-(--base-06) text-sm">
            Loading server…
        </div>
    );
}
