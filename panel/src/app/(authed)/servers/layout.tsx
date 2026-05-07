"use client";

import React, { useState } from 'react';
import Sidebar from '@/components/Sidebar';
import CreateServerWizard from '@/components/CreateServerWizard';
import { useAppData } from '@/lib/AppDataContext';

export default function ServersLayout({ children }: { children: React.ReactNode }) {
    const { proxiesEnabled, refreshServers } = useAppData();
    const [showCreate, setShowCreate] = useState(false);

    return (
        <>
            <Sidebar onNewServer={() => setShowCreate(true)} />
            <main className="flex-1 flex flex-col overflow-hidden relative z-10">
                {children}
            </main>
            {showCreate && (
                <CreateServerWizard
                    isOpen={showCreate}
                    onClose={() => { setShowCreate(false); refreshServers(); }}
                    proxiesEnabled={proxiesEnabled}
                />
            )}
        </>
    );
}
