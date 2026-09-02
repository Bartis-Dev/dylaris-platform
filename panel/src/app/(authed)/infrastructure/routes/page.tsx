"use client";

import RoutesPanel from '@/views/infrastructure/RoutesPanel';
import TabGuard from '@/views/infrastructure/TabGuard';
import { useInfra } from '@/views/infrastructure/context';

export default function Page() {
    const { gatewayDeployed, onlineEdgesList } = useInfra();
    if (!gatewayDeployed) {
        return <TabGuard reason="Routes are served by the gateway, and none is deployed. Enable gateway routing and deploy an edge first." />;
    }
    return <RoutesPanel onlineEdges={onlineEdgesList} />;
}
