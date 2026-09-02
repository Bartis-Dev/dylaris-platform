"use client";

import BandwidthPanel from '@/views/infrastructure/BandwidthPanel';
import TabGuard from '@/views/infrastructure/TabGuard';
import { useInfra } from '@/views/infrastructure/context';

export default function Page() {
    const { gatewayEnabled } = useInfra();
    if (!gatewayEnabled) {
        return <TabGuard reason="Bandwidth is measured by the gateway components, and the gateway is switched off." />;
    }
    return <BandwidthPanel />;
}
