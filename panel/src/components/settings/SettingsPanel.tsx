"use client";

import React, { useState } from 'react';
import { AppModule, User } from '@/lib/api';
import ModulesTab from './ModulesTab';
import UsersTab from './UsersTab';
import NodesTab from './NodesTab';
import LibraryTab from './LibraryTab';
import FileManagerTab from './FileManagerTab';
import FeaturesTab from './FeaturesTab';
import GatewayTab from './GatewayTab';
import ServersTab from './ServersTab';
import BeamTab from './BeamTab';

interface SettingsPanelProps {
    modules: AppModule[];
    onModulesChange: () => void;
    currentUser?: User;
}

const TABS = [
    { id: 'modules', label: 'Modules' },
    { id: 'users', label: 'Users' },
    { id: 'nodes', label: 'Nodes' },
    { id: 'library', label: 'Library' },
    { id: 'filemanager', label: 'File Manager' },
    { id: 'servers', label: 'Servers' },
    { id: 'features', label: 'Features' },
    { id: 'gateway', label: 'Gateway' },
    { id: 'beam', label: 'Beam' },
] as const;

type TabId = typeof TABS[number]['id'];

export default function SettingsPanel({ modules, onModulesChange, currentUser }: SettingsPanelProps) {
    const [activeTab, setActiveTab] = useState<TabId>('modules');

    return (
        <div className="p-6 h-full flex flex-col">
            <h1 className="text-3xl font-display font-bold text-(--accent) mb-6">System Settings</h1>

            {/* Tabs Navigation */}
            <div className="flex gap-4 border-b border-(--base-04) mb-6">
                {TABS.map(tab => (
                    <button
                        key={tab.id}
                        onClick={() => setActiveTab(tab.id)}
                        className={`px-4 py-2 font-mono text-sm font-medium border-b-2 transition-colors ${
                            activeTab === tab.id
                                ? 'border-(--accent) text-(--accent-light)'
                                : 'border-transparent text-(--base-06) hover:text-(--base-09)'
                        }`}
                    >
                        {tab.label}
                    </button>
                ))}
            </div>

            {/* Content Area */}
            <div className="flex-1 overflow-y-auto">
                {activeTab === 'modules' && (
                    <ModulesTab modules={modules} onModulesChange={onModulesChange} />
                )}

                {activeTab === 'users' && (
                    <UsersTab currentUser={currentUser} />
                )}

                {activeTab === 'nodes' && (
                    <NodesTab />
                )}

                {activeTab === 'library' && (
                    <LibraryTab />
                )}

                {activeTab === 'filemanager' && (
                    <FileManagerTab />
                )}

                {activeTab === 'servers' && (
                    <ServersTab />
                )}

                {activeTab === 'features' && (
                    <FeaturesTab />
                )}

                {activeTab === 'gateway' && (
                    <GatewayTab />
                )}

                {activeTab === 'beam' && (
                    <BeamTab />
                )}
            </div>
        </div>
    );
}
