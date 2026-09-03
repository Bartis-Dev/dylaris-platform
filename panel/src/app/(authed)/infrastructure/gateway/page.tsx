"use client";

import GatewayPanel from '@/views/infrastructure/GatewayPanel';
import TabGuard from '@/views/infrastructure/TabGuard';
import { useInfra } from '@/views/infrastructure/context';

export default function Page() {
    const { gatewayDeployed } = useInfra();
    if (!gatewayDeployed) {
        return <TabGuard reason="No gateway is deployed, so there is nothing to show here. Enable gateway routing and deploy an edge first." />;
    }
    return <GatewayPanel />;
}
