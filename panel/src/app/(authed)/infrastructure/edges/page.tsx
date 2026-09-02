"use client";

import EdgesPanel from '@/views/infrastructure/EdgesPanel';
import TabGuard from '@/views/infrastructure/TabGuard';
import { useInfra } from '@/views/infrastructure/context';

export default function Page() {
    const { gatewayDeployed } = useInfra();
    if (!gatewayDeployed) {
        return <TabGuard reason="No gateway is deployed, so there are no edges to show. Enable gateway routing and deploy an edge first." />;
    }
    return <EdgesPanel />;
}
