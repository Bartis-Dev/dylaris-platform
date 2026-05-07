"use client";

import { useRouter } from 'next/navigation';
import InfrastructureView from '@/views/InfrastructureView';
import { useAppData } from '@/lib/AppDataContext';

export default function InfrastructurePage() {
    const router = useRouter();
    const { gatewayEnabled } = useAppData();

    return (
        <main className="flex-1 flex flex-col overflow-hidden relative z-10 p-6">
            <InfrastructureView
                gatewayEnabled={gatewayEnabled}
                onNavigateToAdminDisk={(nodeId) => router.push(`/admin/disk?node=${nodeId}`)}
            />
        </main>
    );
}
