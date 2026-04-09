"use client";

import React, { useState, useEffect } from 'react';
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

const ALL_TABS = [
    { id: 'modules', label: 'Modules', always: true },
    { id: 'users', label: 'Users', always: true },
    { id: 'nodes', label: 'Nodes', always: true },
    { id: 'library', label: 'Library', always: true },
    { id: 'filemanager', label: 'File Manager', always: true },
    { id: 'servers', label: 'Servers', always: true },
    { id: 'features', label: 'Features', always: true },
    { id: 'gateway', label: 'Gateway', always: false, module: 'Gateway' },
    { id: 'beam', label: 'Beam', always: true },
] as const;

type TabId = typeof ALL_TABS[number]['id'];

export default function SettingsPanel({ modules, onModulesChange, currentUser }: SettingsPanelProps) {
    const [activeTab, setActiveTab] = useState<TabId>('modules');

    const visibleTabs = ALL_TABS.filter(tab => {
        if (tab.always) return true;
        if ('module' in tab) return modules.some(m => m.name === tab.module && m.isEnabled);
        return true;
    });

    // If currently active tab becomes hidden, fall back to modules
    useEffect(() => {
        const isVisible = visibleTabs.some(t => t.id === activeTab);
        if (!isVisible) setActiveTab('modules');
    }, [modules]);

    return (
        <div className="p-6 h-full flex flex-col">
            <h1 className="text-3xl font-display font-bold text-(--accent) mb-6">System Settings</h1>

            {/* Tabs Navigation */}
            <div className="flex gap-4 border-b border-(--base-04) mb-6">
                {visibleTabs.map(tab => (
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
