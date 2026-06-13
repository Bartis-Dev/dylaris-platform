"use client";

import React, { useEffect } from 'react';
import { useParams, useRouter } from 'next/navigation';

// /config has no own UI — redirect to /config/properties so old /servers/{id}/config links keep working.
export default function ServerConfigPage() {
    const params = useParams();
    const router = useRouter();
    useEffect(() => {
        router.replace(`/servers/${params?.id}/config/properties`);
    }, [router, params?.id]);
    return null;
}
