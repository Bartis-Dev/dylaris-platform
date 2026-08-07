"use client";

import React, { createContext, useContext, useState, useEffect, useCallback, ReactNode } from 'react';
import {
    getProfile, getModules, getServers, getFeatureSettings, getRoutingMode, getBeamSettings,
    AppModule, Server, User, RoutingMode, FileAccessMode, BeamSettings, isGatewayRouting,
} from '@/lib/api';
import { listRegions, Region } from '@/lib/api/regions';
import { API_URL, getAuthHeader } from '@/lib/api/core';
import { systemEvents } from '@/lib/systemEvents';
import { getSystemFeatures, FeatureFlags } from '@/lib/api/featureFlags';

interface CoreInfo {
    region: string;
    coreId: string;
    // Origin-isolation for the WS5 tab proxy (spec B5): the browser-facing
    // isolated proxy origin the panel builds proxied-iframe srcs against, and
    // whether isolation is active. Empty origin = same-origin fallback.
    tabProxyOrigin: string;
    tabProxyIsolationActive: boolean;
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
    // platform-wide feature toggles. Loaded on boot from
    // /api/system/features; refreshed when features.changed SSE fires.
    // `modpacks` defaults to true on transport failure so the UI doesn't go
    // dark when the network blips.
    featureFlags: FeatureFlags;
    refreshFeatureFlags: () => Promise<void>;
    // regions + connected-Core. Loaded once at boot and cached so
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
    const [routingMode, setRoutingMode] = useState<RoutingMode>('ip_port');
    const [fileAccessMode, setFileAccessMode] = useState<FileAccessMode>('sftp');
    const [beamSettings, setBeamSettings] = useState<BeamSettings | null>(null);
    const [regions, setRegions] = useState<Region[]>([]);
    const [coreInfo, setCoreInfo] = useState<CoreInfo | null>(null);
    // modpacks defaults to true (legacy: ships enabled, admin opts out).
    // tickets defaults to false so the nav doesn't briefly flash a Tickets
    // module on first paint before /system/features comes back.
    const [featureFlags, setFeatureFlags] = useState<FeatureFlags>({ modpacks: true, tickets: false, autoMove: false, byon: false, store: false, shareLinks: false });
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

    const refreshFeatureFlags = useCallback(async () => {
        const res = await getSystemFeatures();
        if (res.success && res.features) setFeatureFlags(res.features);
    }, []);

    // /api/system/core-info — public on the auth surface, returns the region
    // and ID of whichever Core this request landed on. Used by the topbar
    // chip and any region-aware UX that wants to mark "you're talking to X".
    const refreshCoreInfo = useCallback(async () => {
        try {
            const res = await fetch(`${API_URL}/system/core-info`, { headers: getAuthHeader() });
            if (!res.ok) return;
            const data = await res.json();
            if (data.success) setCoreInfo({
                region: data.region,
                coreId: data.coreId,
                tabProxyOrigin: data.tabProxyOrigin || '',
                tabProxyIsolationActive: !!data.tabProxyIsolationActive,
            });
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
            await Promise.all([refreshModules(), refreshServers(), refreshSettings(), refreshRegions(), refreshCoreInfo(), refreshFeatureFlags()]);
            setReady(true);
        };
        init();
    }, []); // eslint-disable-line react-hooks/exhaustive-deps

    // subscribe to /api/system/events SSE for live config refresh.
    // Replaces the old 5s setInterval(refreshServers) poll: status flips and
    // CRUD mutations on any Core publish servers.changed / regions.changed /
    // modules.changed / features.changed / maintenance.changed, and each
    // listener re-fetches just its own slice. systemEvents reconnects on
    // its own with exponential backoff if the stream drops.
    useEffect(() => {
        if (!ready) return;
        systemEvents.start();
        const unsubs = [
            systemEvents.on('servers.changed', () => { refreshServers(); }),
            systemEvents.on('regions.changed', () => { refreshRegions(); }),
            systemEvents.on('modules.changed', () => { refreshModules(); }),
            systemEvents.on('features.changed', () => { refreshSettings(); refreshFeatureFlags(); }),
            systemEvents.on('modpack_settings.changed', () => { refreshFeatureFlags(); }),
            // maintenance state is consumed by MaintenanceBanner via its own
            // fetcher; we re-broadcast through window event so it (and any
            // other isolated subscriber) can react without threading a
            // refresher through context.
            systemEvents.on('maintenance.changed', () => {
                if (typeof window !== 'undefined') {
                    window.dispatchEvent(new CustomEvent('dylaris:maintenance.changed'));
                }
            }),
        ];
        return () => {
            unsubs.forEach(u => u());
            systemEvents.stop();
        };
    }, [ready, refreshServers, refreshRegions, refreshModules, refreshSettings, refreshFeatureFlags]);

    // The routing mode is the single source of truth for "is the gateway
    // routing traffic": Core gates every gateway write on exactly this
    // (handlers.gatewayEnabled reads routing_mode), so the panel must derive
    // it the same way. Deriving it from a separate feature flag let the two
    // sides disagree in both directions — a visible routes UI whose writes
    // 503, or a live gateway with its whole UI hidden.
    const gatewayEnabled = isGatewayRouting(routingMode);
    const libraryEnabled = modules.some(m => m.name === 'Library' && m.isEnabled);

    const value: AppData = {
        user, modules, servers,
        proxiesEnabled, routingMode, fileAccessMode, beamSettings,
        ready,
        refreshUser, refreshModules, refreshServers, refreshSettings,
        gatewayEnabled, libraryEnabled,
        featureFlags, refreshFeatureFlags,
        regions, coreInfo, refreshRegions,
    };

    return <AppDataContext.Provider value={value}>{children}</AppDataContext.Provider>;
}
