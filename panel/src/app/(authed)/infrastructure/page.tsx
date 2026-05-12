"use client";

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import InfrastructureView from '@/views/InfrastructureView';
import { useAppData } from '@/lib/AppDataContext';

export default function InfrastructurePage() {
    const router = useRouter();
    const { gatewayEnabled, user, ready } = useAppData();

    useEffect(() => {
        if (!ready) return;
        if (!user?.isAdmin) router.replace('/servers');
    }, [ready, user?.isAdmin, router]);

    if (!ready) return null;
    if (!user?.isAdmin) {
        return (
            <main className="flex-1 flex items-center justify-center text-(--error) font-semibold text-xl font-display">
                Access denied. Administrator rights required.
            </main>
        );
    }

    return (
        <main className="flex-1 flex flex-col overflow-hidden relative z-10 p-6">
            <InfrastructureView
                gatewayEnabled={gatewayEnabled}
                onNavigateToAdminDisk={(nodeId) => router.push(`/admin/disk?node=${nodeId}`)}
            />
        </main>
    );
}
