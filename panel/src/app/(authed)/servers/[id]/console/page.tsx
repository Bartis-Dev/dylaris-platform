"use client";

import { useParams } from 'next/navigation';
import { useAppData } from '@/lib/AppDataContext';
import ConsoleView from '@/views/ConsoleView';

export default function ServerConsolePage() {
    const params = useParams();
    const { servers } = useAppData();
    const server = servers.find(s => s.id === Number(params?.id));
    if (!server) return null;
    return <ConsoleView server={server} />;
}
