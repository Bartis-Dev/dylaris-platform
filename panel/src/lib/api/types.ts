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
    accessRole: 'all' | 'admin';
}
export const setModuleAccessRole = (id: number, role: 'all' | 'admin') =>
    fetchAPI(`/modules/${id}/role`, { method: 'PATCH', body: JSON.stringify({ role }) });

export interface User {
    id: string;
    username: string;
    password?: string; // FIX: Allows setting passwords in the form
    email?: string;
    minecraftUsername?: string;
    isAdmin: boolean;
    is2FAEnabled?: boolean;
    createdAt?: string;
    lastUsernameChange?: string;
    // Region access. allRegionsAccess=true overrides the
    // explicit regions list — user sees all current and future regions.
    allRegionsAccess?: boolean;
    regions?: string[];
    // Lifecycle (surfaced for UI badges).
    emailVerifiedAt?: string;
    lastLoginAt?: string;
    deletionStatus?: string;
    // When status === 'pending_deletion', this is the date the
    // auto-delete job will execute. Surfaced in the admin UsersTab so admins
    // can see how urgent a rescue is.
    deletionScheduledAt?: string;
    deletionWarningSentAt?: string;
    // Roles + granular capability flags.
    role?: 'user' | 'support' | 'admin';
    canDeleteServers?: boolean;
    canChangeResources?: boolean;
    supportTeam?: string;
    // Per-user modpack authoring gate. Optional for forward-compat
    // with older API responses; the current backend always populates it.
    canCreateModpacks?: boolean;
}

export interface Node {
    id: number;
    name: string;
    address: string;
    token: string;
    status: string;
    isLocal: boolean;
    tags?: string;
    region?: string;
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
    // Adoption state. configured=true once an admin set name/region/tags in the
    // panel (DB then wins over the heartbeat env). needsConfiguration=true when
    // the node has no region yet (booted with only a CLUSTER_SECRET).
    configured?: boolean;
    needsConfiguration?: boolean;
    // Optional, non-unique human label. Defaults to the node's hostname on
    // enroll; editable via configureNode independently of the unique `name`.
    displayName?: string;
}

// P0b-5 node admission + enroll/recovery tokens.
export interface AdmissionCIDR {
    id: string;
    cidr: string;
    label: string;
    createdAt: string;
}

export interface NodeEnrollToken {
    id: string;
    userId: string;
    label: string;
    createdAt: string;
    expiresAt?: string;
    consumedAt?: string;
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
    backups: boolean;
    inherit: boolean;
}

export interface ServerInvite {
    id: number;
    serverId: number;
    userId: string;
    username: string;
    email: string;
    permissions: TabPermissions;
    invitedBy: string;
    inviterName: string;
    createdAt: string;
}

export interface Server {
    id: number;
    uuid: string;
    name: string;
    nodeId: number;
    nodeName?: string;
    ownerId: string;
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
    cpuPinningMode?: 'shared' | 'auto' | 'manual';
    cpuset?: string;
    nodeAddress?: string;
    serverType?: 'game' | 'proxy';
    proxyId?: number | null;
    createdAt?: string;
    role?: 'owner' | 'invited' | 'admin' | 'inherited' | 'demo';
    permissions?: TabPermissions;
    region?: string;
    // Read-only showcase server (admin-flagged). Non-owner viewers may only read;
    // the panel suppresses write affordances when this is set.
    isDemo?: boolean;
}

export interface SftpCredentials {
    host: string;
    port: number;
    username: string;
    path: string;
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
    // Some Go handlers send `http.Error(...)` which is text/plain. Trying to
    // parse that as JSON throws an unhelpful SyntaxError and callers lose the
    // server's actual message. Read the body once, parse it if it's JSON, and
    // otherwise wrap the text in a typed-shape envelope so callers can read
    // res.error / res.message uniformly.
    const text = await res.text();
    let parsed: any = null;
    try {
        parsed = text ? JSON.parse(text) : null;
    } catch {
        parsed = null;
    }
    if (parsed !== null) return parsed;
    if (!res.ok) {
        const msg = text.trim() || `Request failed with status ${res.status}`;
        return { success: false, error: msg, message: msg };
    }
    return {};
}

// --- PLATFORM HEALTH (admin Status page) ---
export type HealthStatus = 'up' | 'degraded' | 'down' | 'disabled';

export interface HealthItem {
    name: string;
    status: HealthStatus;
    detail?: string;
}

export interface HealthComponent {
    key: string;
    name: string;
    status: HealthStatus;
    detail?: string;
    reason?: string;
    items?: HealthItem[];
    /**
     * Machine-readable failure class, where the component has one. `status`
     * cannot carry this: two components can both be `down` for reasons that
     * need entirely different fixes, and a free-text `reason` is not something
     * the UI can branch on. Only the Redis component sets it today
     * (core/database/rediserror.go RedisFailure.Slug). Absent means the
     * component reports no classification and the card shows detail and reason
     * alone.
     */
    cause?: string;
}

export interface SystemHealth {
    overall: 'healthy' | 'degraded' | 'down';
    components: HealthComponent[];
    checkedAt: string;
}

export const getSystemHealth = (): Promise<{ success: boolean; health: SystemHealth }> =>
    fetchAPI('/admin/health');

// --- MODULES ---
export const getModules = () => fetchAPI('/modules');
export const createModule = (data: Partial<AppModule>) => fetchAPI('/modules', { method: 'POST', body: JSON.stringify(data) });
export const deleteModule = (id: number) => fetchAPI(`/modules/${id}`, { method: 'DELETE' });
export const toggleModule = (id: number, isEnabled: boolean) => fetchAPI(`/modules/${id}/toggle`, { method: 'PATCH', body: JSON.stringify({ isEnabled }) });
export const updateModulePosition = (id: number, position: number) => fetchAPI(`/modules/${id}/position`, { method: 'PATCH', body: JSON.stringify({ position }) });

// --- USERS ---
export const getUsers = () => fetchAPI('/users');
export const createUser = (data: Partial<User> & { allRegions?: boolean; regionsExplicit?: string[] }) => fetchAPI('/users', { method: 'POST', body: JSON.stringify(data) });
export const deleteUser = (id: string) => fetchAPI(`/users/${id}`, { method: 'DELETE' });
export const cancelUserDeletion = (id: string) => fetchAPI(`/admin/users/${id}/cancel-deletion`, { method: 'POST' });
export const setUserRole = (id: string, role: 'user' | 'support' | 'admin') =>
    fetchAPI(`/admin/users/${id}/role`, { method: 'PUT', body: JSON.stringify({ role }) });
export const setUserPermissions = (
    id: string,
    data: { canDeleteServers: boolean; canChangeResources: boolean; supportTeam?: string },
) => fetchAPI(`/admin/users/${id}/permissions`, { method: 'PUT', body: JSON.stringify(data) });

// Maintenance API
export interface MaintenanceState {
    active: boolean;
    title: string;
    message: string;
    expectedEnd: string;
    blockLevel: 'off' | 'banner_only' | 'block_writes' | 'block_all';
}
export const getMaintenance = () => fetchAPI('/maintenance');
export const saveMaintenance = (state: MaintenanceState) =>
    fetchAPI('/admin/maintenance', { method: 'PUT', body: JSON.stringify(state) });
export const resetUserPassword = (id: string, password: string) => fetchAPI(`/users/${id}/password`, { method: 'PUT', body: JSON.stringify({ password }) });
export const getUserRouteLimit = (id: string) => fetchAPI(`/users/${id}/route-limit`);
export const setUserRouteLimit = (id: string, data: { mode: string; maxRoutes: number }) => fetchAPI(`/users/${id}/route-limit`, { method: 'PUT', body: JSON.stringify(data) });

// --- NODES ---
export interface CpuCore { id: number; type: 'P' | 'E' | 'standard'; sibling: number; }
export interface NodeCpuTopology { logicalCount: number; physicalCount: number; hybrid: boolean; cores: CpuCore[]; scannedAt: number; }
// Returns { success, topology: NodeCpuTopology | null, load: { [coreId]: number } }.
export const getNodeCpu = (nodeId: number) => fetchAPI(`/nodes/${nodeId}/cpu`);
// Returns { success, storage: StorageInfo[] } (path/total_bytes/free_bytes/used_bytes/server_count).
export const getNodeStorage = (nodeId: number) => fetchAPI(`/nodes/${nodeId}/storage`);
// Returns { success, nodeId, grpcTlsFingerprint, linkSecret, linkDiscoveryProof }.
// The secret-free BYON deploy bundle for an already-enrolled node.
export const getNodeDeployBundle = (nodeId: number) => fetchAPI(`/nodes/${nodeId}/deploy-bundle`);

export const getNodes = () => fetchAPI('/nodes');
export const createNode = (data: Partial<Node>) => fetchAPI('/nodes', { method: 'POST', body: JSON.stringify(data) });
export const getNodeServers = (id: number) => fetchAPI(`/nodes/${id}/servers`);
export const forceDeleteNode = (id: number) => fetchAPI(`/nodes/${id}/force`, { method: 'DELETE' });
// Adopt an auto-discovered node: persist name/region/tags to the DB. After this
// the heartbeat env no longer overwrites them.
export const configureNode = (id: number, data: { name?: string; region: string; tags?: string; displayName?: string }) =>
    fetchAPI(`/nodes/${id}/config`, { method: 'PATCH', body: JSON.stringify(data) });
// Set the node's container CPU pool (which host cores its containers may use).
// "" clears the restriction (all cores allowed).
export const updateNodeCpuset = (id: number, cpusetCpus: string) =>
    fetchAPI(`/nodes/${id}`, { method: 'PUT', body: JSON.stringify({ cpusetCpus }) });

// --- SERVERS ---
export const getServers = () => fetchAPI('/servers');
export const createServer = (data: any) => fetchAPI('/servers', { method: 'POST', body: JSON.stringify(data) });
export const deleteServer = (id: number) => fetchAPI(`/servers/${id}`, { method: 'DELETE' });
export const deleteSubServer = (id: number, subServerName: string) => fetchAPI(`/servers/${id}/sub-servers/${encodeURIComponent(subServerName)}`, { method: 'DELETE' });
export const serverPower = (id: number, action: string) => fetchAPI(`/servers/${id}/power`, { method: 'POST', body: JSON.stringify({ action }) });
// Seconds remaining on the post-install cooldown. Reads the same Redis TTL
// the backend's PowerAction handler enforces, so the UI shows the truth.
export const getInstallCooldown = (id: number): Promise<{ success: boolean; seconds: number }> =>
    fetchAPI(`/servers/${id}/install-cooldown`);
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
    cpusetCpus?: string,
    // CPU pinning. Omit to leave pinning unchanged. For mode 'manual' the
    // cpuset is sent; for 'auto'/'shared' the backend ignores it.
    pinning?: { mode: 'shared' | 'auto' | 'manual'; cpuset?: string },
) => fetchAPI(`/servers/${id}/resources`, {
    method: 'PATCH',
    body: JSON.stringify({
        ram, cpuLimit, diskLimit,
        ...ports,
        ...(cpusetCpus !== undefined ? { cpusetCpus } : {}),
        ...(pinning !== undefined ? { cpuPinningMode: pinning.mode } : {}),
        ...(pinning !== undefined && pinning.cpuset !== undefined ? { cpuset: pinning.cpuset } : {}),
    }),
});

export const sendConsoleCommand = (id: number, command: string) =>
    fetchAPI(`/servers/${id}/console/command`, { method: 'POST', body: JSON.stringify({ command }) });

// --- PROXY LINKING ---
export const linkServerToProxy = (serverId: number, proxyId: number) =>
    fetchAPI(`/servers/${serverId}/proxy`, { method: 'PUT', body: JSON.stringify({ proxyId }) });
export const unlinkServerFromProxy = (serverId: number) =>
    fetchAPI(`/servers/${serverId}/proxy`, { method: 'DELETE' });

// --- BACKUPS ---
export type BackupProvider = 'local' | 's3' | 'node-local' | 'shared';

export interface BackupStorage {
    id: number;
    name: string;
    provider: BackupProvider;
    config: Record<string, unknown>;
    isDefault: boolean;
    createdAt?: string;
    /**
     * True when an s3 secret is stored. The secret itself is never returned by
     * the API (redacted server-side), so the edit form shows the field empty
     * and leaving it blank keeps the stored secret.
     */
    secretSet?: boolean;
}

// Global backup-mode + quota config (persisted server-side under
// backup.mode / backup.quota_per_server_gb / backup.share_quota_with_server).
// The mode picks which provider the panel exposes as creatable; per-instance
// credentials still live in the BackupStorage rows above.
export interface BackupConfig {
    mode: 'shared' | 's3' | 'node-local';
    quotaPerServerGb: number;        // 0 = unlimited
    shareQuotaWithServer: boolean;
}

export const getBackupConfig = (): Promise<{ success: boolean; settings?: BackupConfig }> =>
    fetchAPI('/settings/backup');
export const saveBackupConfig = (cfg: BackupConfig): Promise<{ success: boolean }> =>
    fetchAPI('/settings/backup', { method: 'POST', body: JSON.stringify(cfg) });

// Per-server backup-folder usage (bytes on disk, archive count). Used by
// the Overview tab to render a separate or combined storage bar when the
// global backup.mode is "node-local". `degraded` is true when the Node
// couldn't be reached — the UI uses that to suppress the row instead of
// rendering misleading zeros.
export interface BackupUsage {
    success: boolean;
    usedBytes: number;
    count: number;
    degraded?: boolean;
}
export const getBackupUsage = (serverId: number): Promise<BackupUsage> =>
    fetchAPI(`/servers/${serverId}/backup-usage`);
export interface BackupJob {
    id: number;
    serverId: number;
    subServer?: string | null;
    name: string;
    schedule: string;             // "manual" | "every Nh" | "every Nd"
    includePatterns: string[];
    excludePatterns: string[];
    retentionCount: number;
    storageId?: number | null;
    enabled: boolean;
    lastRunAt?: string | null;
    nextRunAt?: string | null;
    createdAt?: string;
}
export interface BackupRun {
    id: number;
    jobId: number;
    startedAt: string;
    completedAt?: string | null;
    status: 'running' | 'success' | 'failed';
    sizeBytes: number;
    storageKey: string;
    errorMessage: string;
}

export const listBackupStorages = (): Promise<{ success: boolean; storages?: BackupStorage[] }> =>
    fetchAPI('/backup-storages');
export const createBackupStorage = (s: Partial<BackupStorage>): Promise<{ success: boolean; id?: number }> =>
    fetchAPI('/backup-storages', { method: 'POST', body: JSON.stringify(s) });
export const updateBackupStorage = (id: number, s: Partial<BackupStorage>): Promise<{ success: boolean }> =>
    fetchAPI(`/backup-storages/${id}`, { method: 'PATCH', body: JSON.stringify(s) });
export const deleteBackupStorage = (id: number): Promise<{ success: boolean }> =>
    fetchAPI(`/backup-storages/${id}`, { method: 'DELETE' });
export const testBackupStorage = (id: number): Promise<{ success: boolean; message?: string; warning?: string }> =>
    fetchAPI(`/backup-storages/${id}/test`, { method: 'POST' });

// --- STORAGE CONNECTIONS ---
// Named, reusable storage backends (currently s3) that any feature can
// reference instead of re-entering credentials. The secret access key lives
// encrypted server-side in its own column and is never returned by the API.

export interface StorageConnectionConfig {
    endpoint?: string;
    region?: string;
    bucket?: string;
    forcePathStyle?: boolean;
    prefix?: string;
}

export interface StorageConnection {
    id: number;
    name: string;
    provider: 's3';
    config: StorageConnectionConfig;
    accessKey: string;
    createdAt?: string;
    updatedAt?: string;
    /**
     * True when a secret is stored. The secret itself is never returned by the
     * API, so the edit form shows the field empty; leaving it blank keeps the
     * stored secret.
     */
    secretSet?: boolean;
}

// Write payload: the secret is sent separately (write-only) and omitted on an
// edit that keeps the existing one.
export interface StorageConnectionInput {
    name: string;
    provider: 's3';
    config: StorageConnectionConfig;
    accessKey: string;
    secretAccessKey?: string;
}

export const listStorageConnections = (): Promise<{ success: boolean; connections?: StorageConnection[] }> =>
    fetchAPI('/storage-connections');
export const createStorageConnection = (c: StorageConnectionInput): Promise<{ success: boolean; id?: number }> =>
    fetchAPI('/storage-connections', { method: 'POST', body: JSON.stringify(c) });
export const updateStorageConnection = (id: number, c: StorageConnectionInput): Promise<{ success: boolean }> =>
    fetchAPI(`/storage-connections/${id}`, { method: 'PATCH', body: JSON.stringify(c) });
export const deleteStorageConnection = (id: number): Promise<{ success: boolean }> =>
    fetchAPI(`/storage-connections/${id}`, { method: 'DELETE' });
export const testStorageConnection = (id: number): Promise<{ success: boolean; ok?: boolean; message?: string }> =>
    fetchAPI(`/storage-connections/${id}/test`, { method: 'POST' });

export const listBackupJobs = (serverId: number): Promise<{ success: boolean; jobs?: BackupJob[] }> =>
    fetchAPI(`/servers/${serverId}/backup-jobs`);
export const createBackupJob = (serverId: number, j: Partial<BackupJob>): Promise<{ success: boolean; id?: number }> =>
    fetchAPI(`/servers/${serverId}/backup-jobs`, { method: 'POST', body: JSON.stringify(j) });
export const updateBackupJob = (jobId: number, j: Partial<BackupJob>): Promise<{ success: boolean }> =>
    fetchAPI(`/backup-jobs/${jobId}`, { method: 'PATCH', body: JSON.stringify(j) });
export const deleteBackupJob = (jobId: number): Promise<{ success: boolean }> =>
    fetchAPI(`/backup-jobs/${jobId}`, { method: 'DELETE' });
export const triggerBackupJob = (jobId: number): Promise<{ success: boolean; runId?: number }> =>
    fetchAPI(`/backup-jobs/${jobId}/trigger`, { method: 'POST' });
export const listBackupRuns = (jobId: number): Promise<{ success: boolean; runs?: BackupRun[] }> =>
    fetchAPI(`/backup-jobs/${jobId}/runs`);
export const deleteBackupRun = (runId: number): Promise<{ success: boolean }> =>
    fetchAPI(`/backup-runs/${runId}`, { method: 'DELETE' });
export const restoreBackupRun = (runId: number): Promise<{ success: boolean; message?: string; restoreId?: number }> =>
    fetchAPI(`/backup-runs/${runId}/restore`, { method: 'POST' });

export interface BackupRestore {
    id: number;
    runId: number;
    serverId: number;
    requestedBy?: string | null;
    requestedAt: string;
    completedAt?: string | null;
    status: 'queued' | 'running' | 'success' | 'failed';
    errorMessage: string;
}
export const listBackupRestores = (serverId: number): Promise<{ success: boolean; restores?: BackupRestore[] }> =>
    fetchAPI(`/servers/${serverId}/backup-restores`);
export const backupDownloadUrl = (runId: number) => {
    return `${API_BASE}/backup-runs/${runId}/download`;
};

export interface ProxyEndpoint {
    serverId: number;
    serverName: string;
    proxyId: number;
    proxyUuid: string;
    ip: string;
    hostname: string;
}
export const getProxyEndpoint = (serverId: number): Promise<{ success: boolean; endpoints?: ProxyEndpoint[] }> =>
    fetchAPI(`/servers/${serverId}/proxy-endpoint`);


// --- LIBRARY ---
export const getLibraryFiles = (path?: string) => fetchAPI(`/library${path ? `?path=${encodeURIComponent(path)}` : ''}`);
export const deleteLibraryPath = (path: string) => fetchAPI('/library/delete', { method: 'POST', body: JSON.stringify({ path }) });
export const createLibraryDir = (path: string) => fetchAPI('/library/mkdir', { method: 'POST', body: JSON.stringify({ path }) });
export const toggleLibraryPath = (path: string, enabled: boolean) =>
    fetchAPI('/library/toggle', { method: 'POST', body: JSON.stringify({ path, enabled }) });
// Library storage backend (path/s3) config now lives at Settings -> Core
// Storage (@/lib/api/coreStorage) - the old /settings/library CRUD + test
// endpoints were dead (Library reads its provider from the shared Core file
// storage config) and have been removed on both sides.

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
    // Post-GC live JVM heap size in MB. Populated by the log-shipper's
    // GC log parser; absent on Java 8 (which can't emit -Xlog), or for
    // the first few seconds of a fresh boot before the JVM has GC'd.
    // When present this is the "real" memory usage that fluctuates with
    // GC cycles; memUsed is the container-level metric that always
    // reads near Xmx because the heap is pre-committed.
    javaHeapUsed?: number;
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
    // Master switch for the Gateway routing feature (edges, links, routes).
    // When false the Edges + Routes sub-tabs in Infrastructure are hidden and
    // route creation is blocked. Lives here instead of as a module because
    // the standalone Gateway nav was retired.
    gatewayEnabled: boolean;
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

export interface EdgeStats {
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

export interface GatewayEdge {
    edge_id: string;
    name: string;
    ip: string;
    private_ip: string;
    service_port: string;
    splice_port: string;
    status: string;
    region?: string;
    // splice_version = the RUNNING splice sidecar on this edge; splice_version_latest
    // = the LATEST available splice (baked into the edge's rolling :latest image).
    // Compared per region to flag a pending, deliberately-scheduled splice bump.
    // Empty when a pre-versioning edge/splice is deployed.
    splice_version?: string;
    splice_version_latest?: string;
    stats?: EdgeStats;
}

export interface GatewayRoute {
    // domain is the unique identity — routes live in Redis as
    // route:{domain} and have no integer ID. Use it as the React key
    // and as the URL segment when deleting.
    domain: string;
    target_ip: string;
    target_port: number;
    link_id?: number;
    link?: GatewayLink;
    server_uuid?: string;
    server_id?: number;
    owner_id?: string;
    owner_name?: string;
    server_name?: string;
    link_name?: string;
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
    // Reserved leftmost labels users may not register as a route (e.g. admin, dylaris).
    blockedRoutePrefixes: string[];
}

export interface GatewayRouteOptions {
    success: boolean;
    hosterDomains: HosterDomain[];
    customDomainsEnabled: boolean;
    cnameTarget: string;
}

// Gateway Admin API (read-only for links/edges — they auto-register via Redis)
export const getGatewayRoutes = () => fetchAPI('/gateway/routes');
export const deleteGatewayRoute = (domain: string) => fetchAPI(`/gateway/routes/${encodeURIComponent(domain)}`, { method: 'DELETE' });

// Bulk route operations (admin)
export interface RouteSuffix {
    suffix: string;
    count: number;
    depth: number; // 1 = apex (one dot), 2 = parent (two dots)
}
export const getRouteSuffixes = (): Promise<{ success: boolean; suffixes: RouteSuffix[] }> =>
    fetchAPI('/gateway/routes/suffixes');
export const bulkDeleteRoutesBySuffix = (suffix: string): Promise<{ success: boolean; deleted: number; failed: number; suffix: string; message?: string }> =>
    fetchAPI('/gateway/routes/bulk-delete', { method: 'POST', body: JSON.stringify({ suffix }) });
export const triggerGatewaySync = () => fetchAPI('/gateway/sync', { method: 'POST' });

export const getGatewaySettings = () => fetchAPI('/settings/gateway');
export const saveGatewaySettings = (data: GatewaySettings) => fetchAPI('/settings/gateway', { method: 'POST', body: JSON.stringify(data) });

// --- PLACEMENT / SCHEDULING ---
export interface PlacementSettings {
    cpuOvercommitDefault: number;
    ramOvercommitDefault: number;
    diskBufferGb: number;
    rebalanceEnabled: boolean;
    rebalanceThreshold: number;
    portMode: 'sequential' | 'random';
    containerPort: number;
    // Per-container process/thread cap (cgroup pids). 0 = unlimited.
    pidsLimit: number;
    // Per-container blkio relative weight (10–1000). 0 = off. Scheduler-dependent.
    ioWeight: number;
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
export const getAvailableTags = (region?: string): Promise<{ success: boolean; tags: string[] }> =>
    fetchAPI(`/placement/tags${region ? `?region=${encodeURIComponent(region)}` : ''}`);
export const getAvailableRegions = (): Promise<{ success: boolean; regions: string[] }> =>
    fetchAPI('/placement/regions');
export const pickNode = (data: { region?: string; tags?: string[]; tag?: string; nodeId?: number; ramMb: number; cpuCores: number; diskGb: number }): Promise<PickNodeResponse> =>
    fetchAPI('/placement/pick', { method: 'POST', body: JSON.stringify(data) });
export const setNodePlacement = (nodeId: number, data: { cpuOvercommitRatio: number; ramOvercommitRatio: number }) =>
    fetchAPI(`/nodes/${nodeId}/placement`, { method: 'PUT', body: JSON.stringify(data) });
export const setServerAutoMove = (serverId: number, enabled: boolean) =>
    fetchAPI(`/servers/${serverId}/automove`, { method: 'PATCH', body: JSON.stringify({ enabled }) });

// Manual node-to-node migration (admin). Async — the Core enqueues onto the
// orchestrator and returns 202; progress is polled via getMigrationStatus.
export const moveServer = (serverId: number, targetNodeId: number) =>
    fetchAPI(`/admin/servers/${serverId}/move`, { method: 'POST', body: JSON.stringify({ targetNodeId }) });

// Tenant-facing transfer (BYON). Same orchestrator + status as moveServer, but
// the caller only needs to own the server and be allowed to place on the target
// node. Async — returns 202; progress is polled via getMigrationStatus.
export const transferServer = (serverId: number, targetNodeId: number) =>
    fetchAPI(`/servers/${serverId}/transfer`, { method: 'POST', body: JSON.stringify({ targetNodeId }) });

// Mark/unmark a server as a public read-only demo (admin). Non-owner users with
// no server of their own then see it read-only in their sidebar.
export const setServerDemo = (serverId: number, enabled: boolean) =>
    fetchAPI(`/admin/servers/${serverId}/demo`, { method: 'PATCH', body: JSON.stringify({ enabled }) });

// Orchestrator progress record. `phase` is "none" when no migration is active.
// Terminal phases: done, failed, failed_post_cutover, aborted_players, none.
export interface MigrationStatus {
    phase: string;
    error?: string;
    sourceNodeID?: number;
    targetNodeID?: number;
    reason?: string;
    startedAt?: number;
    updatedAt?: number;
    // True only while the migration is still pre-cutover and in-flight, so an
    // admin cancel would actually take effect (the panel hides the cancel button
    // otherwise). Computed server-side in GetMigrationStatus.
    cancellable?: boolean;
}
export const getMigrationStatus = (serverId: number): Promise<{ success: boolean; status?: MigrationStatus; message?: string }> =>
    fetchAPI(`/servers/${serverId}/migration-status`);

// Cancel an in-flight migration (admin). Rolls the server back to its current
// node - only honored pre-cutover; returns 409 once the move has cut over.
export const cancelMigration = (serverId: number) =>
    fetchAPI(`/admin/servers/${serverId}/migration/cancel`, { method: 'POST' });

// Per-server edge transitional-MOTD config (what players see via the gateway
// edge while the server is down/starting/migrating).
export type EdgeMotdMode = 'auto' | 'custom' | 'off';
export const getEdgeMotd = (serverId: number): Promise<{ success: boolean; mode?: EdgeMotdMode; customText?: string; message?: string }> =>
    fetchAPI(`/servers/${serverId}/edge-motd`);
export const setEdgeMotd = (serverId: number, mode: EdgeMotdMode, customText: string) =>
    fetchAPI(`/servers/${serverId}/edge-motd`, { method: 'PATCH', body: JSON.stringify({ mode, customText }) });

// Infrastructure
export const getInfrastructureOverview = () => fetchAPI('/infrastructure/overview');
export const getRoutingMigrationStatus = () => fetchAPI('/infrastructure/routing-migration');

// Routing Mode
export type RoutingMode = 'ip_port' | 'both' | 'gateway';
export type FileAccessMode = 'sftp' | 'both' | 'beam';
export const getRoutingMode = () => fetchAPI('/settings/routing-mode');
export const saveRoutingMode = (data: { mode: RoutingMode; fileMode: FileAccessMode }) =>
    fetchAPI('/settings/routing-mode', { method: 'POST', body: JSON.stringify(data) });

// --- Warp (external/home node WireGuard bridge, multi-hub) ---
// A region owns one WG identity (subnet + key); its leaders are interchangeable
// endpoints. Clients fail over between a region's leaders without changing IP.
export interface WarpLeaderView {
    leaderId: string;
    endpoint: string;
    enabled: boolean;
    alive: boolean;
}
export interface WarpRegionView {
    region: string;
    subnet: string;
    enabled: boolean;
    peerCount: number;
    leaders: WarpLeaderView[] | null;
}
export interface MintWarpKeyInput {
    name: string;
    policy: 'fixed' | 'general';
    max_conns: number;
    on_new_conn: 'kill_old' | 'block';
    region?: string; // "" = auto-assign at enroll (least-loaded live region)
}
export const getWarpRegions = () => fetchAPI('/warp/regions');
export const upsertWarpRegion = (data: { region: string; subnet: string; enabled: boolean }) =>
    fetchAPI('/warp/regions', { method: 'POST', body: JSON.stringify(data) });
export const deleteWarpRegion = (region: string) =>
    fetchAPI(`/warp/regions/${encodeURIComponent(region)}`, { method: 'DELETE' });
export const upsertWarpLeader = (data: { leaderId: string; region: string; endpoint: string; enabled: boolean }) =>
    fetchAPI('/warp/leaders', { method: 'POST', body: JSON.stringify(data) });
export const deleteWarpLeader = (leaderId: string) =>
    fetchAPI(`/warp/leaders/${encodeURIComponent(leaderId)}`, { method: 'DELETE' });
export const mintWarpKey = (data: MintWarpKeyInput) =>
    fetchAPI('/admin/warp/keys', { method: 'POST', body: JSON.stringify(data) });

// Warp overlay segmentation: the admin-configurable spoke destination-port
// allowlist the region leaders enforce (comma-separated TCP ports).
export interface WarpFirewallSettings {
    allowedPorts: string;
}
export const getWarpFirewallSettings = (): Promise<{ success: boolean; settings: WarpFirewallSettings }> =>
    fetchAPI('/settings/warp-firewall');
export const saveWarpFirewallSettings = (
    data: WarpFirewallSettings,
): Promise<{ success: boolean; settings?: WarpFirewallSettings; error?: string; message?: string }> =>
    fetchAPI('/settings/warp-firewall', { method: 'POST', body: JSON.stringify(data) });

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
// Backend identifies routes by full domain (URL: /servers/{id}/routes/{domain:.+}).
export const deleteServerRoute = (serverId: number, domain: string) =>
    fetchAPI(`/servers/${serverId}/routes/${encodeURIComponent(domain)}`, { method: 'DELETE' });
export const getGatewayRouteOptions = (): Promise<GatewayRouteOptions> => fetchAPI('/gateway/route-options');

// Route-only ("via Link") routes: a protected address pointed at a server the
// user runs on their OWN machine, reached through their own outbound Link tunnel
// (no managed node, no exposed origin). Owner-scoped.
export interface LinkRoute {
    domain: string;
    target_ip: string;
    target_port: number;
    tunnel_id?: string;
    core_owned?: boolean;
    owner_id?: string;
}
export interface CreateLinkRouteRequest extends CreateRouteRequest {
    linkId: string;    // the Link kit's id (warp key node_id)
    targetHost: string; // the LOCAL target the Link dials (LAN / loopback allowed)
}
export const getLinkRoutes = (): Promise<LinkRoute[]> => fetchAPI('/gateway/link-routes');
export const createLinkRoute = (data: CreateLinkRouteRequest) =>
    fetchAPI('/gateway/link-routes', { method: 'POST', body: JSON.stringify(data) });
export const deleteLinkRoute = (domain: string) =>
    fetchAPI(`/gateway/link-routes/${encodeURIComponent(domain)}`, { method: 'DELETE' });

// Link kits: a warp key + auto link identity bound to the account. The customer
// runs warp + link with the kit to expose a LOCAL server through the gateway.
// Mint returns the secrets ONCE; the list is metadata-only.
export interface LinkKit {
    id: number;
    name: string;
    link_id: string;
    created_at: string;
}
export interface MintedLinkKit {
    success: boolean;
    warp_key: string;
    link_id: string;
    note: string;
}
export const listLinkKits = (): Promise<{ success: boolean; kits: LinkKit[]; used?: number; limit?: number }> =>
    fetchAPI('/warp/link-kits');
export const mintLinkKit = (name: string): Promise<MintedLinkKit> =>
    fetchAPI('/warp/link-kits', { method: 'POST', body: JSON.stringify({ name }) });
export const revokeLinkKit = (linkId: string): Promise<{ success: boolean }> =>
    fetchAPI(`/warp/link-kits/${encodeURIComponent(linkId)}`, { method: 'DELETE' });

// Live availability check for the route-create form. Accepts the same
// three input shapes the create endpoint does and answers `{available}`
// without leaking who owns a taken domain.
export interface DomainCheckRequest {
    domain?: string;
    subdomain?: string;
    hosterDomain?: string;
    customDomain?: string;
}
export interface DomainCheckResponse {
    available: boolean;
    domain?: string;
    reason?: string;
}
export const checkDomainAvailability = (req: DomainCheckRequest): Promise<DomainCheckResponse> => {
    const params = new URLSearchParams();
    if (req.domain) params.set('domain', req.domain);
    if (req.subdomain) params.set('subdomain', req.subdomain);
    if (req.hosterDomain) params.set('hosterDomain', req.hosterDomain);
    if (req.customDomain) params.set('customDomain', req.customDomain);
    return fetchAPI(`/gateway/check-domain?${params.toString()}`);
};

// --- BEAM SETTINGS ---
export interface BeamRelayInfo {
    beam_id: string;
    ip: string;
    private_ip?: string;
    public_host?: string;       // operator-configured (e.g. beam.dylaris.com)
    service_port: string;
    client_port?: string;
    download_port?: string;
    timestamp: number;
}
export interface BeamSettings {
    relayAddress: string;                // Effective (discovered or manual override)
    manualOverride?: string;             // Admin-configured override (empty = auto)
    discoveredRelays?: BeamRelayInfo[];  // Read-only list from Redis discovery
    bwLimit: number;
    enabled: boolean;
    downloadLink?: string;               // Optional CDN override
    minVersion?: string;                 // Force-update floor (empty = gating off)
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
    createdAt?: string;
    serverType?: 'game' | 'proxy';
    memory?: number;
    diskLimit?: number;
    cpuLimit?: number;
    memberCount?: number;
    proxyId?: number | null;
    region?: string;
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
export const updateServerOwner = (serverId: number, userId: string | null) => fetchAPI(`/admin/servers/${serverId}/owner`, { method: 'PATCH', body: JSON.stringify({ userId }) });
export const deleteOrphanedFolder = (nodeId: number, uuid: string) => fetchAPI(`/admin/nodes/${nodeId}/orphan?uuid=${encodeURIComponent(uuid)}`, { method: 'DELETE' });