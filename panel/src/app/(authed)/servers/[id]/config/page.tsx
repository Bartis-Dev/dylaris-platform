"use client";

import React, { useEffect } from 'react';
import { useParams, useRouter } from 'next/navigation';

// /config — landing page redirects to /config/properties. Keeps inbound links
// to /servers/{id}/config working post Phase 8 sub-tab split.
export default function ServerConfigPage() {
    const params = useParams();
    const router = useRouter();
    useEffect(() => {
        router.replace(`/servers/${params?.id}/config/properties`);
    }, [router, params?.id]);
    return null;
}
