"use client";

import React, { createContext, useContext, useState, useEffect, useCallback, ReactNode } from 'react';
import {
    getProfile, getModules, getServers, getFeatureSettings, getRoutingMode, getBeamSettings,
    AppModule, Server, User, RoutingMode, FileAccessMode, BeamSettings,
} from '@/lib/api';
import { listRegions, Region } from '@/lib/api/regions';
import { API_URL, getAuthHeader } from '@/lib/api/core';

interface CoreInfo {
    region: string;
    coreId: string;
}

interface AppData {
    user: User | null;
    modules: AppModule[];
    servers: Server[];
    proxiesEnabled: boolean;
    routingMode: RoutingMode;
    fileAccessMode: FileAccessMode;
    beamSettings: BeamSettings | null;
    ready: boolean;
    refreshUser: () => Promise<void>;
    refreshModules: () => Promise<void>;
    refreshServers: () => Promise<void>;
    refreshSettings: () => Promise<void>;
    gatewayEnabled: boolean;
    libraryEnabled: boolean;
    // Phase 6 — regions + connected-Core. Loaded once at boot and cached so
    // child components can call useAppData() without each issuing their own
    // fetch. Refreshes happen lazily on visible state changes (Settings →
    // Regions tab triggers refresh after save).
    regions: Region[];
    coreInfo: CoreInfo | null;
    refreshRegions: () => Promise<void>;
}

const AppDataContext = createContext<AppData | null>(null);

export function useAppData(): AppData {
    const ctx = useContext(AppDataContext);
    if (!ctx) throw new Error('useAppData must be used within AppDataProvider');
    return ctx;
}

interface AppDataProviderProps {
    children: ReactNode;
    onUnauthenticated: () => void;
}

export function AppDataProvider({ children, onUnauthenticated }: AppDataProviderProps) {
    const [user, setUser] = useState<User | null>(null);
    const [modules, setModules] = useState<AppModule[]>([]);
    const [servers, setServers] = useState<Server[]>([]);
    const [proxiesEnabled, setProxiesEnabled] = useState(true);
    const [gatewayFeatureEnabled, setGatewayFeatureEnabled] = useState(true);
    const [routingMode, setRoutingMode] = useState<RoutingMode>('ip_port');
    const [fileAccessMode, setFileAccessMode] = useState<FileAccessMode>('sftp');
    const [beamSettings, setBeamSettings] = useState<BeamSettings | null>(null);
    const [regions, setRegions] = useState<Region[]>([]);
    const [coreInfo, setCoreInfo] = useState<CoreInfo | null>(null);
    const [ready, setReady] = useState(false);

    const refreshUser = useCallback(async () => {
        const profile = await getProfile();
        if (profile) setUser(profile);
        else onUnauthenticated();
    }, [onUnauthenticated]);

    const refreshModules = useCallback(async () => {
        const res = await getModules();
        if (res.success && res.modules) setModules(res.modules);
    }, []);

    const refreshServers = useCallback(async () => {
        const res = await getServers();
        if (res.success && res.servers) setServers(res.servers);
    }, []);

    const refreshRegions = useCallback(async () => {
        const res = await listRegions();
        if (res.success && Array.isArray(res.regions)) setRegions(res.regions);
    }, []);

    // /api/system/core-info — public on the auth surface, returns the region
    // and ID of whichever Core this request landed on. Used by the topbar
    // chip and any region-aware UX that wants to mark "you're talking to X".
    const refreshCoreInfo = useCallback(async () => {
        try {
            const res = await fetch(`${API_URL}/system/core-info`, { headers: getAuthHeader() });
            if (!res.ok) return;
            const data = await res.json();
            if (data.success) setCoreInfo({ region: data.region, coreId: data.coreId });
        } catch { /* network blip — keep last known state */ }
    }, []);

    const refreshSettings = useCallback(async () => {
        const [features, routing, beam] = await Promise.all([
            getFeatureSettings(),
            getRoutingMode(),
            getBeamSettings(),
        ]);
        if (features.success && features.settings) {
            setProxiesEnabled(features.settings.proxyEnabled);
            setGatewayFeatureEnabled(features.settings.gatewayEnabled ?? true);
        }
        if (routing.success) {
            setRoutingMode(routing.mode || 'ip_port');
            setFileAccessMode(routing.fileMode || 'sftp');
        }
        if (beam.success && beam.settings) setBeamSettings(beam.settings);
    }, []);

    // Initial load
    useEffect(() => {
        const init = async () => {
            const profile = await getProfile();
            if (!profile) { onUnauthenticated(); return; }
            setUser(profile);
            await Promise.all([refreshModules(), refreshServers(), refreshSettings(), refreshRegions(), refreshCoreInfo()]);
            setReady(true);
        };
        init();
    }, []); // eslint-disable-line react-hooks/exhaustive-deps

    // Poll server list every 5s for status updates (parity with old Dashboard)
    useEffect(() => {
        if (!ready) return;
        const interval = setInterval(refreshServers, 5000);
        return () => clearInterval(interval);
    }, [ready, refreshServers]);

    // Gateway is no longer a standalone module — its on/off lives in the
    // Features tab as a dedicated `gatewayEnabled` flag (separate from the
    // MC-proxy `proxyEnabled` flag).
    const gatewayEnabled = gatewayFeatureEnabled;
    const libraryEnabled = modules.some(m => m.name === 'Library' && m.isEnabled);

    const value: AppData = {
        user, modules, servers,
        proxiesEnabled, routingMode, fileAccessMode, beamSettings,
        ready,
        refreshUser, refreshModules, refreshServers, refreshSettings,
        gatewayEnabled, libraryEnabled,
        regions, coreInfo, refreshRegions,
    };

    return <AppDataContext.Provider value={value}>{children}</AppDataContext.Provider>;
}
