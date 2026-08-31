"use client";

import React, { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { useRouteId } from '@/lib/routeParams';

// /config has no own UI — redirect to /config/properties so old /servers/{id}/config links keep working.
export default function ServerConfigPage() {
    const paramId = useRouteId('servers');
    const router = useRouter();
    useEffect(() => {
        router.replace(`/servers/${paramId}/config/properties`);
    }, [router, paramId]);
    return null;
}
