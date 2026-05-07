"use client";

import { useParams } from 'next/navigation';
import { useAppData } from '@/lib/AppDataContext';
import OverviewView from '@/views/OverviewView';

export default function ServerOverviewPage() {
    const params = useParams();
    const { servers } = useAppData();
    const server = servers.find(s => s.id === Number(params?.id));
    if (!server) return null;
    return <OverviewView server={server} />;
}
