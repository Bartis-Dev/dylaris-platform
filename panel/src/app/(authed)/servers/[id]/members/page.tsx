"use client";

import { useParams } from 'next/navigation';
import { useAppData } from '@/lib/AppDataContext';
import MembersView from '@/views/MembersView';

export default function ServerMembersPage() {
    const params = useParams();
    const { servers } = useAppData();
    const server = servers.find(s => s.id === Number(params?.id));
    if (!server) return null;
    return <MembersView server={server} />;
}
