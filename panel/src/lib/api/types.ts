import { API_URL as API_BASE } from './core';

export interface AppModule {
    id: number;
    name: string;
    type: string;
    icon: string;
    url?: string;
    isEnabled: boolean;
    isSystem: boolean;
    position: number;
}

export interface User {
    id: number;
    username: string;
    password?: string; // FIX: Allows setting passwords in the form
    email?: string;
    minecraftUsername?: string;
    isAdmin: boolean;
    is2FAEnabled?: boolean;
    publicId?: string; // FIX: Public ID
    public_id?: string;
    createdAt?: string;
}

export interface Node {
    id: number;
    name: string;
    address: string;
    token: string;
    status: string;
    isLocal: boolean;
    tags?: string;
    linkEnabled?: boolean;
    linkInstances?: number;
    cpusetCpus?: string;
    publicIp?: string;
    privateIps?: string[];
    serverCount?: number;
    // Placement (persisted)
    cpuOvercommitRatio?: number;
    ramOvercommitRatio?: number;
    totalCpu?: number;
    totalRamMb?: number;
    // Live (from heartbeat, set by infrastructure overview)
    cpuUsage?: number;
    ramFree?: number;
    ramTotal?: number;
}

export interface TabPermissions {
    console: boolean;
    files: boolean;
    config: boolean;
    setup: boolean;
    overview: boolean;
    power: boolean;
    members: boolean;
    network: boolean;
    inherit: boolean;
}

export interface ServerInvite {
    id: number;
    serverId: number;
    userId: number;
    username: string;
    email: string;
    permissions: TabPermissions;
    invitedBy: number;
    inviterName: string;
    createdAt: string;
}

export interface Server {
    id: number;
    uuid: string;
    name: string;
    nodeId: number;
    nodeName?: string;
    ownerId: number;
    ownerName?: string;
    owner?: string;
    node?: string;
    image?: string;
    port: number;
    memory: number;
    cpuLimit?: number;
    startCommand?: string;
    status: string;
    activeSubServer?: string;
    extraJvmFlags?: string;
    installerType?: string;
    minecraftVersion?: string;
    buildNumber?: string;
    diskLimit?: number;
    hostPort?: number;
    containerPort?: number;
    cpusetCpus?: string;
    nodeAddress?: string;
    serverType?: 'game' | 'proxy';
    proxyId?: number | null;
    createdAt?: string;
    role?: 'owner' | 'invited' | 'admin' | 'inherited';
    permissions?: TabPermissions;
}

export interface SftpCredentials {
    host: string;
    port: number;
    username: string;
    path: string;
}

export interface LibrarySettings {
    type: string;
    path: string;
    s3Endpoint?: string;
    s3Bucket?: string;
    s3Region?: string;
    s3AccessKey?: string;
}

async function fetchAPI(endpoint: string, options: RequestInit = {}) {
    const token = localStorage.getItem('token');
    const headers = {
        'Content-Type': 'application/json',
        ...(token && { 'Authorization': `Bearer ${token}` }),
        ...options.headers,
    };

    const res = await fetch(`${API_BASE}${endpoint}`, { ...options, headers });
    if (res.status === 401) {
        localStorage.removeItem('token');
        if (typeof window !== 'undefined') window.location.href = '/login';
    }
    return res.json();
}

// --- MODULES ---
export const getModules = () => fetchAPI('/modules');
export const createModule = (data: Partial<AppModule>) => fetchAPI('/modules', { method: 'POST', body: JSON.stringify(data) });
export const deleteModule = (id: number) => fetchAPI(`/modules/${id}`, { method: 'DELETE' });
export const toggleModule = (id: number, isEnabled: boolean) => fetchAPI(`/modules/${id}/toggle`, { method: 'PATCH', body: JSON.stringify({ isEnabled }) });
export const updateModulePosition = (id: number, position: number) => fetchAPI(`/modules/${id}/position`, { method: 'PATCH', body: JSON.stringify({ position }) });

// --- USERS ---
export const getUsers = () => fetchAPI('/users');
export const createUser = (data: Partial<User>) => fetchAPI('/users', { method: 'POST', body: JSON.stringify(data) });
export const deleteUser = (id: number) => fetchAPI(`/users/${id}`, { method: 'DELETE' });
export const resetUserPassword = (id: number, password: string) => fetchAPI(`/users/${id}/password`, { method: 'PUT', body: JSON.stringify({ password }) });
export const getUserRouteLimit = (id: number) => fetchAPI(`/users/${id}/route-limit`);
export const setUserRouteLimit = (id: number, data: { mode: string; maxRoutes: number }) => fetchAPI(`/users/${id}/route-limit`, { method: 'PUT', body: JSON.stringify(data) });

// --- NODES ---
export const getNodes = () => fetchAPI('/nodes');
export const createNode = (data: Partial<Node>) => fetchAPI('/nodes', { method: 'POST', body: JSON.stringify(data) });
export const updateNode = (id: number, data: Partial<Node>) => fetchAPI(`/nodes/${id}`, { method: 'PUT', body: JSON.stringify(data) });
export const deleteNode = (id: number) => fetchAPI(`/nodes/${id}`, { method: 'DELETE' });
export const getNodeServers = (id: number) => fetchAPI(`/nodes/${id}/servers`);
export const forceDeleteNode = (id: number) => fetchAPI(`/nodes/${id}/force`, { method: 'DELETE' });

// --- SERVERS ---
export const getServers = () => fetchAPI('/servers');
export const createServer = (data: any) => fetchAPI('/servers', { method: 'POST', body: JSON.stringify(data) });
export const deleteServer = (id: number) => fetchAPI(`/servers/${id}`, { method: 'DELETE' });
export const deleteSubServer = (id: number, subServerName: string) => fetchAPI(`/servers/${id}/sub-servers/${encodeURIComponent(subServerName)}`, { method: 'DELETE' });
export const serverPower = (id: number, action: string) => fetchAPI(`/servers/${id}/power`, { method: 'POST', body: JSON.stringify({ action }) });
export const setupServer = (id: number, data: any) => fetchAPI(`/servers/${id}/setup`, { method: 'POST', body: JSON.stringify(data) });
export const switchSubServer = (id: number, subServerName: string) => fetchAPI(`/servers/${id}/switch`, { method: 'POST', body: JSON.stringify({ subServerName }) });
export const getSftpCredentials = (id: number) => fetchAPI(`/servers/${id}/sftp-credentials`);

// --- STORAGE MIGRATION ---
export interface StoragePathInfo {
    path: string;
    total_bytes: number;
    free_bytes: number;
    used_bytes: number;
    server_count: number;
}

export const getServerStoragePath = (id: number) => fetchAPI(`/servers/${id}/storage-path`);
export const migrateServerStorage = (id: number, targetPath: string) =>
    fetchAPI(`/servers/${id}/migrate-storage`, { method: 'POST', body: JSON.stringify({ targetPath }) });

// --- SERVER NAME / RESOURCES ---
export const updateServerName = (id: number, name: string) =>
    fetchAPI(`/servers/${id}/name`, { method: 'PATCH', body: JSON.stringify({ name }) });

export const updateServerResources = (
    id: number,
    ram: number,
    cpuLimit: number,
    diskLimit: number,
    ports?: { hostPort?: number; containerPort?: number },
    cpusetCpus?: string
) => fetchAPI(`/servers/${id}/resources`, {
    method: 'PATCH',
    body: JSON.stringify({ ram, cpuLimit, diskLimit, ...ports, ...(cpusetCpus !== undefined ? { cpusetCpus } : {}) }),
});

export const sendConsoleCommand = (id: number, command: string) =>
    fetchAPI(`/servers/${id}/console/command`, { method: 'POST', body: JSON.stringify({ command }) });

// --- PROXY LINKING ---
export const linkServerToProxy = (serverId: number, proxyId: number) =>
    fetchAPI(`/servers/${serverId}/proxy`, { method: 'PUT', body: JSON.stringify({ proxyId }) });
export const unlinkServerFromProxy = (serverId: number) =>
    fetchAPI(`/servers/${serverId}/proxy`, { method: 'DELETE' });


// --- MEMBERS (Invites) ---
export const getServerMembers = (serverId: number) => fetchAPI(`/servers/${serverId}/members`);
export const getInheritedMembers = (serverId: number) => fetchAPI(`/servers/${serverId}/members/inherited`);
export const inviteServerMember = (serverId: number, username: string, permissions?: Partial<TabPermissions>) =>
    fetchAPI(`/servers/${serverId}/members`, { method: 'POST', body: JSON.stringify({ username, permissions }) });
export const updateMemberPermissions = (serverId: number, userId: number, permissions: TabPermissions) =>
    fetchAPI(`/servers/${serverId}/members/${userId}`, { method: 'PATCH', body: JSON.stringify({ permissions }) });
export const removeServerMember = (serverId: number, userId: number) =>
    fetchAPI(`/servers/${serverId}/members/${userId}`, { method: 'DELETE' });

// --- LIBRARY ---
export const getLibraryFiles = (path?: string) => fetchAPI(`/library${path ? `?path=${encodeURIComponent(path)}` : ''}`);
export const deleteLibraryPath = (path: string) => fetchAPI('/library/delete', { method: 'POST', body: JSON.stringify({ path }) });
export const createLibraryDir = (path: string) => fetchAPI('/library/mkdir', { method: 'POST', body: JSON.stringify({ path }) });
export const toggleLibraryPath = (path: string, enabled: boolean) =>
    fetchAPI('/library/toggle', { method: 'POST', body: JSON.stringify({ path, enabled }) });
export const getLibrarySettings = () => fetchAPI('/settings/library');
export const saveLibrarySettings = (data: LibrarySettings) => fetchAPI('/settings/library', { method: 'POST', body: JSON.stringify(data) });
export const testLibraryConnection = () => fetchAPI('/settings/library/test');

// --- FILE MANAGER SETTINGS ---
export interface FileManagerSettings {
    adminUploadLimit: number;
    adminDownloadLimit: number;
    userUploadLimit: number;
    userDownloadLimit: number;
}
export const getFileManagerSettings = () => fetchAPI('/settings/filemanager');
export const saveFileManagerSettings = (data: FileManagerSettings) => fetchAPI('/settings/filemanager', { method: 'POST', body: JSON.stringify(data) });
export const getUserLimits = () => fetchAPI('/settings/filemanager/limits');

// --- STATS ---
export interface ServerStats {
    ts: number;
    cpu: number;
    cpuLimit: number;
    memUsed: number;
    memLimit: number;
    players: number;
    maxPlayers: number;
    motd: string;
}

export interface DiskUsage {
    total: number;
    limit: number;
    subServers: Record<string, number>;
    warning?: '' | '80' | '90' | 'full';
}

export const getStatsHistory = (id: number, range?: string) =>
    fetchAPI(`/servers/${id}/stats/history${range ? `?range=${range}` : ''}`);

export const getDiskUsage = (id: number) => fetchAPI(`/servers/${id}/stats/disk`);

// --- SERVER SETTINGS ---
export interface ServerLimitSettings {
    maxSubServers: number;
}
export const getServerSettings = () => fetchAPI('/settings/servers');
export const saveServerSettings = (data: ServerLimitSettings) => fetchAPI('/settings/servers', { method: 'POST', body: JSON.stringify(data) });

// --- FEATURE SETTINGS ---
export interface FeatureSettings {
    proxyEnabled: boolean;
}
export const getFeatureSettings = () => fetchAPI('/settings/features');
export const saveFeatureSettings = (data: FeatureSettings) => fetchAPI('/settings/features', { method: 'POST', body: JSON.stringify(data) });

// --- GATEWAY ---
export interface GatewayLink {
    ID: number;
    name: string;
    token: string;
    enabled: boolean;
    is_system: boolean;
    node_id?: string;
    online: boolean;
    active_tunnels: number;
    redis_connected: boolean;
    active_mode: string;
}

export interface GateStats {
    cpu: number;
    ram_used: number;
    ram_total: number;
    ram_pct: number;
    rx_speed: number;
    tx_speed: number;
    active_tunnels: number;
    active_tokens: number;
    active_mc_streams: number;
    xdp_enabled: boolean;
    xdp_passed: number;
    xdp_dropped_blocked: number;
    xdp_dropped_ratelimit: number;
    xdp_blocked_ips: number;
}

export interface GatewayGate {
    gate_id: string;
    name: string;
    ip: string;
    private_ip: string;
    service_port: string;
    health_port: string;
    status: string;
    stats?: GateStats;
}

export interface GatewayRoute {
    ID: number;
    domain: string;
    target_ip: string;
    target_port: number;
    link_id?: number;
    link?: GatewayLink;
    server_uuid?: string;
    server_id?: number;
    owner_id?: number;
    owner_name?: string;
    server_name?: string;
    link_name?: string;
}

export interface GatewayStats {
    links: number;
    linksOnline: number;
    gates: number;
    gatesOnline: number;
    routes: number;
}

export interface GatewayLog {
    id: number;
    timestamp: string;
    level: string;
    source: string;
    message: string;
}

export interface ServiceError {
    timestamp: string;
    level: string;
    source: string;
    message: string;
}

export interface GatewayLimits {
    global: number;
    userDefault: number;
    perServer: number;
    portMc: number;
    portMcEnabled: boolean;
    portHttps: number;
    portHttpsEnabled: boolean;
    portHttp: number;
    portHttpEnabled: boolean;
}

export type HosterValidation = 'letters' | 'alphanumeric' | 'dns';

export interface HosterDomain {
    domain: string;
    validation: HosterValidation;
}

export interface GatewaySettings {
    limits: GatewayLimits;
    hosterDomains: HosterDomain[];
    customDomainsEnabled: boolean;
    cnameTarget: string;
}

export interface GatewayRouteOptions {
    success: boolean;
    hosterDomains: HosterDomain[];
    customDomainsEnabled: boolean;
    cnameTarget: string;
}

// Gateway Admin API (read-only for links/gates — they auto-register via Redis)
export const getGatewayLinks = () => fetchAPI('/gateway/links');
export const getGatewayGates = () => fetchAPI('/gateway/gates');
export const getGatewayRoutes = () => fetchAPI('/gateway/routes');
export const deleteGatewayRoute = (id: number) => fetchAPI(`/gateway/routes/${id}`, { method: 'DELETE' });
export const getGatewayLogs = () => fetchAPI('/gateway/logs');
export const getGatewayStats = (): Promise<GatewayStats> => fetchAPI('/gateway/stats');
export const triggerGatewaySync = () => fetchAPI('/gateway/sync', { method: 'POST' });
export const getGatewayErrors = (service?: string) => fetchAPI(`/gateway/errors${service ? `?service=${service}` : ''}`);

export const getGatewaySettings = () => fetchAPI('/settings/gateway');
export const saveGatewaySettings = (data: GatewaySettings) => fetchAPI('/settings/gateway', { method: 'POST', body: JSON.stringify(data) });

// --- PLACEMENT / SCHEDULING ---
export interface PlacementSettings {
    cpuOvercommitDefault: number;
    ramOvercommitDefault: number;
    diskBufferGb: number;
    rebalanceEnabled: boolean;
    rebalanceThreshold: number;
}
export interface NodeCandidate {
    nodeId: number;
    nodeName: string;
    available: boolean;
    reason: string;
    score: number;
    allocRamMb: number;
    allocCpu: number;
    totalRamMb: number;
    totalCpu: number;
    overcommitRam: number;
    overcommitCpu: number;
    serverCount: number;
}
export interface PickNodeResponse {
    success: boolean;
    picked?: NodeCandidate;
    candidates: NodeCandidate[];
    reason: string;
}
export const getPlacementSettings = () => fetchAPI('/settings/placement');
export const savePlacementSettings = (data: PlacementSettings) =>
    fetchAPI('/settings/placement', { method: 'POST', body: JSON.stringify(data) });
export const getAvailableTags = (): Promise<{ success: boolean; tags: string[] }> =>
    fetchAPI('/placement/tags');
export const pickNode = (data: { tag?: string; nodeId?: number; ramMb: number; cpuCores: number; diskGb: number }): Promise<PickNodeResponse> =>
    fetchAPI('/placement/pick', { method: 'POST', body: JSON.stringify(data) });
export const setNodePlacement = (nodeId: number, data: { cpuOvercommitRatio: number; ramOvercommitRatio: number }) =>
    fetchAPI(`/nodes/${nodeId}/placement`, { method: 'PUT', body: JSON.stringify(data) });
export const setServerAutoMove = (serverId: number, enabled: boolean) =>
    fetchAPI(`/servers/${serverId}/automove`, { method: 'PATCH', body: JSON.stringify({ enabled }) });

// Infrastructure
export const getInfrastructureOverview = () => fetchAPI('/infrastructure/overview');
export const getRoutingMigrationStatus = () => fetchAPI('/infrastructure/routing-migration');

// Routing Mode
export type RoutingMode = 'ip_port' | 'both' | 'gateway';
export type FileAccessMode = 'sftp' | 'both' | 'beam';
export const getRoutingMode = () => fetchAPI('/settings/routing-mode');
export const saveRoutingMode = (data: { mode: RoutingMode; fileMode: FileAccessMode }) =>
    fetchAPI('/settings/routing-mode', { method: 'POST', body: JSON.stringify(data) });

// Gateway User API (per-server routes)
export const getServerRoutes = (serverId: number) => fetchAPI(`/servers/${serverId}/routes`);
export interface CreateRouteRequest {
    targetPort: number;
    // Either a hoster-picker pair, a custom-domain string, or a raw domain (legacy/admin).
    subdomain?: string;
    hosterDomain?: string;
    customDomain?: string;
    domain?: string;
}
export const createServerRoute = (serverId: number, data: CreateRouteRequest) =>
    fetchAPI(`/servers/${serverId}/routes`, { method: 'POST', body: JSON.stringify(data) });
export const deleteServerRoute = (serverId: number, routeId: number) => fetchAPI(`/servers/${serverId}/routes/${routeId}`, { method: 'DELETE' });
export const getGatewayRouteOptions = (): Promise<GatewayRouteOptions> => fetchAPI('/gateway/route-options');

// --- BEAM SETTINGS ---
export interface BeamSettings {
    relayAddress: string;
    bwLimit: number;
    enabled: boolean;
    downloadLink?: string;
}
export const getBeamSettings = (): Promise<{ success: boolean; settings?: BeamSettings }> => fetchAPI('/settings/beam');
export const saveBeamSettings = (data: BeamSettings) => fetchAPI('/settings/beam', { method: 'POST', body: JSON.stringify(data) });

// --- ADMIN API ---
export interface AdminServer {
    id: number;
    uuid: string;
    name: string;
    owner?: string;    // json:"owner" from models.Server.OwnerName
    node?: string;     // json:"node" from models.Server.NodeName
    nodeId?: number;
    status: string;
    activeSubServer?: string;
}
export interface DiskAnalysis {
    nodeOnline?: boolean;
    matched: { uuid: string; serverName: string; ownerName: string; status: string }[];
    orphaned: { uuid: string }[];
    missing: { id: number; uuid: string; serverName: string }[];
}
export const getAdminServers = (params?: { search?: string; orphaned?: boolean }) => {
    const q = new URLSearchParams();
    if (params?.search) q.set('search', params.search);
    if (params?.orphaned) q.set('orphaned', 'true');
    return fetchAPI(`/admin/servers${q.toString() ? '?' + q.toString() : ''}`);
};
export const getAdminDiskAnalysis = (nodeId: number): Promise<{ success: boolean } & DiskAnalysis> => fetchAPI(`/admin/nodes/${nodeId}/disk-analysis`);
export const updateServerOwner = (serverId: number, userId: number | null) => fetchAPI(`/admin/servers/${serverId}/owner`, { method: 'PATCH', body: JSON.stringify({ userId }) });
export const getAdminFiles = (nodeId: number, uuid: string, path?: string) => fetchAPI(`/admin/files?nodeId=${nodeId}&uuid=${encodeURIComponent(uuid)}${path ? `&path=${encodeURIComponent(path)}` : ''}`);
export const deleteOrphanedFolder = (nodeId: number, uuid: string) => fetchAPI(`/admin/nodes/${nodeId}/orphan?uuid=${encodeURIComponent(uuid)}`, { method: 'DELETE' });