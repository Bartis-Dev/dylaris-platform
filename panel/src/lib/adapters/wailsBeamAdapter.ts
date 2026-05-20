// Wails Beam adapter: routes file operations through the Wails-injected
// native bindings (window.go.main.App.*) which in turn talk to the
// BeamRelay over gRPC. Used only when the panel runs inside the Beam
// Desktop App; the regular browser uses panelFileBrowserAdapter.
//
// Detection lives in createBeamAdapter() — components keep their existing
// `const adapter = useMemo(() => createBeamAdapter(...))` call and never
// have to know which transport is active.

import type { FileBrowserAdapter, FileEntry } from '@dylaris/ui-filebrowser';
import { uploadFiles as apiUploadFiles, getUserLimits as apiGetUserLimits, API_URL } from '@/lib/api';

// Chunk size for the JS → Go upload bridge. Smaller = more IPC calls
// (overhead-bound on the JS side); larger = bigger base64 strings in
// each IPC payload (memory-bound on both ends). 512 KB is the sweet
// spot in practice: ~2k calls per GB, ~700 KB JSON per call.
const UPLOAD_CHUNK_SIZE = 512 * 1024;

// btoa() chokes on String.fromCharCode.apply() with very large arrays
// (call-stack overflow above ~64K args). Chunk the conversion.
function uint8ToBase64(bytes: Uint8Array): string {
    let binary = '';
    const STEP = 0x8000; // 32 KB at a time
    for (let i = 0; i < bytes.length; i += STEP) {
        const sub = bytes.subarray(i, i + STEP);
        binary += String.fromCharCode.apply(null, sub as unknown as number[]);
    }
    return btoa(binary);
}

// Shape of the bindings exposed by gateway/beam/app/app.go. We type only
// the methods the FileBrowser actually calls; everything else stays
// loosely typed since the rest of the panel doesn't reach into it.
interface WailsAppBindings {
    Login(apiURL: string, username: string, password: string): Promise<{ token: string; username: string; isAdmin: boolean }>;
    SetSession?(apiURL: string, token: string): Promise<void>;
    Logout(): Promise<void>;
    GetBeamConfig(): Promise<{ relay_address: string }>;
    ConnectToServer(serverUUID: string): Promise<void>;
    // Connect using a ticket the Panel already minted from Core. The
    // Panel's session reaches Core reliably; the Beam app's Go HTTP
    // client gets WAF/CDN HTML back on POST /beam/ticket. Optional —
    // older app builds only have ConnectToServer.
    ConnectToServerWithTicket?(serverUUID: string, ticket: string): Promise<void>;
    ListFiles(path: string, serverUUID: string): Promise<{ success: boolean; files?: FileEntry[]; message?: string }>;
    GetFileContent(path: string, serverUUID: string): Promise<{ success: boolean; content?: string; message?: string }>;
    SaveFile(path: string, content: string, serverUUID: string): Promise<void>;
    CreateFile(path: string, isDir: boolean, serverUUID: string): Promise<void>;
    DeleteFile(path: string, serverUUID: string): Promise<void>;
    RenameFile(oldPath: string, newPath: string, serverUUID: string): Promise<void>;
    CopyFile(srcPath: string, dstPath: string, serverUUID: string): Promise<void>;
    DownloadFile(path: string, serverUUID: string, isDir: boolean): Promise<void>;
    SelectiveDownload(basePath: string, selected: string[], selectAll: boolean, serverUUID: string): Promise<void>;
    RevealInExplorer?(localPath: string): Promise<void>;
    // Chunked upload: open a stream, send N chunks (base64-encoded
    // bytes), close. Each call references an opaque uploadID minted
    // by the JS side. Optional on the typing because older app builds
    // don't expose them — the adapter feature-detects at call time.
    BeamUploadStart?(uploadID: string, path: string, filename: string, strategy: string, totalSize: number): Promise<void>;
    BeamUploadChunk?(uploadID: string, dataB64: string, offset: number): Promise<void>;
    BeamUploadFinish?(uploadID: string): Promise<void>;
    BeamUploadCancel?(uploadID: string): Promise<void>;
}

declare global {
    interface Window {
        go?: { main?: { App?: WailsAppBindings } };
    }
}

export function getWailsApp(): WailsAppBindings | null {
    if (typeof window === 'undefined') return null;
    return window.go?.main?.App ?? null;
}

export function isWails(): boolean {
    return getWailsApp() !== null;
}

// syncSessionWithWails pushes the panel's session token + API base to
// the Wails side so its Core client (used for the relay-address lookup)
// is authenticated. Promise-cached: SetSession tears down any live
// relay tunnel, so it must run exactly once — every caller awaits the
// same cached promise instead of re-invoking it.
let sessionSyncPromise: Promise<void> | null = null;
export function syncSessionWithWails(): Promise<void> {
    if (!sessionSyncPromise) {
        sessionSyncPromise = doSyncSession();
    }
    return sessionSyncPromise;
}

async function doSyncSession(): Promise<void> {
    const app = getWailsApp();
    if (!app || typeof app.SetSession !== 'function') return;
    const token = localStorage.getItem('authToken') || localStorage.getItem('token');
    if (!token) return;
    const apiUrl = process.env.NEXT_PUBLIC_API_URL || window.location.origin + '/api';
    try {
        await app.SetSession(apiUrl, token);
    } catch (err) {
        console.warn('Wails SetSession failed:', err);
    }
}

// fetchBeamTicket mints a relay ticket through the panel's own browser
// fetch. The Beam app's Go HTTP client gets HTML back from a CDN/WAF on
// POST /beam/ticket, but the panel session — the same one that logs in
// fine — goes straight through. The Wails side then opens the tunnel
// with the ticket we hand it.
async function fetchBeamTicket(serverUuid: string): Promise<string> {
    const token = localStorage.getItem('authToken') || localStorage.getItem('token') || '';
    const res = await fetch(`${API_URL}/beam/ticket`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ server_uuid: serverUuid }),
    });
    let data: { success?: boolean; ticket?: string; message?: string } = {};
    try { data = await res.json(); } catch { /* non-JSON error body */ }
    if (!res.ok || !data.success || !data.ticket) {
        throw new Error(data.message || `ticket request failed (HTTP ${res.status})`);
    }
    return data.ticket;
}

// ensureWailsConnection points the native relay tunnel at `serverUuid`,
// throwing the real Go-side error on failure so callers can surface it
// (the Wails side lazily resolves the relay address, so this also
// covers first-call bootstrap). Deduped: a server that's already the
// live tunnel is a no-op.
let lastConnectedServer = '';
export async function ensureWailsConnection(serverUuid: string): Promise<void> {
    const app = getWailsApp();
    if (!app || !serverUuid) return;
    await syncSessionWithWails();
    if (serverUuid === lastConnectedServer) return;
    if (typeof app.ConnectToServerWithTicket === 'function') {
        // Mint the ticket panel-side, then hand it to Wails.
        const ticket = await fetchBeamTicket(serverUuid);
        await app.ConnectToServerWithTicket(serverUuid, ticket);
    } else {
        // Older app build — let the Wails side fetch the ticket itself.
        await app.ConnectToServer(serverUuid);
    }
    lastConnectedServer = serverUuid;
}

// connectWailsToServer is the best-effort pre-warm used on navigation:
// it opens the tunnel ahead of time but only logs failures — the actual
// file op (e.g. upload) re-checks via ensureWailsConnection and surfaces
// the real error there.
export async function connectWailsToServer(serverUuid: string): Promise<void> {
    try {
        await ensureWailsConnection(serverUuid);
    } catch (err) {
        console.warn('Wails ConnectToServer failed:', err);
    }
}

export function createWailsBeamAdapter(): FileBrowserAdapter {
    const app = getWailsApp();
    if (!app) {
        throw new Error('Wails bindings are not available — createWailsBeamAdapter called outside Beam Desktop');
    }

    // wrap turns native Wails-binding calls into the {success, message?}
    // shape the FileBrowserAdapter contract expects. Return type is
    // intentionally loose — TS can't validate the gRPC payload shape
    // any tighter, and the FileBrowser code accesses fields structurally.
    type WrapResult = { success: boolean; message?: string } & Record<string, unknown>;
    const wrap = async (fn: () => Promise<unknown>): Promise<WrapResult> => {
        try {
            // Wait for the session to reach the Wails side first — the
            // native ops reject with "not logged in" until SetSession
            // has run, which would otherwise flash in the file list.
            await syncSessionWithWails();
            const result = await fn();
            if (result === undefined || result === null) return { success: true };
            if (typeof result === 'object' && 'success' in (result as Record<string, unknown>)) {
                return result as WrapResult;
            }
            return { success: true, ...(result as Record<string, unknown>) };
        } catch (err) {
            const message = err instanceof Error ? err.message : String(err);
            return { success: false, message };
        }
    };

    return {
        getFiles: (path, serverUuid) => wrap(() => app.ListFiles(path, serverUuid ?? '')) as ReturnType<FileBrowserAdapter['getFiles']>,
        getFileContent: (path, serverUuid) => wrap(() => app.GetFileContent(path, serverUuid ?? '')) as ReturnType<FileBrowserAdapter['getFileContent']>,
        saveFile: (path, content, serverUuid) => wrap(() => app.SaveFile(path, content, serverUuid ?? '')),
        createFile: (path, isDir, serverUuid) => wrap(() => app.CreateFile(path, isDir, serverUuid ?? '')),
        deleteFile: (path, serverUuid) => wrap(() => app.DeleteFile(path, serverUuid ?? '')),
        renameFile: (oldPath, newPath, serverUuid) => wrap(() => app.RenameFile(oldPath, newPath, serverUuid ?? '')),
        copyFile: (srcPath, dstPath, serverUuid) => wrap(() => app.CopyFile(srcPath, dstPath, serverUuid ?? '')),
        // Native chunked upload: bytes flow JS → Wails IPC → gRPC stream
        // → Relay tunnel → Node's temp file → atomic rename. Core never
        // sees the payload, so its body-size limit and the user's admin
        // upload cap don't apply here. If the build is missing the
        // bindings (older app), we fall back to HTTP through Core.
        uploadFiles: async (path, files, onProgress, strategy, mergeConflict, serverUuid) => {
            const hasNative =
                typeof app.BeamUploadStart === 'function' &&
                typeof app.BeamUploadChunk === 'function' &&
                typeof app.BeamUploadFinish === 'function';
            if (!hasNative) {
                return apiUploadFiles(path, files, onProgress, strategy, mergeConflict, serverUuid);
            }
            if (!files || files.length === 0) return { success: true };
            // FileBrowser zips folders into a single .zip before calling
            // the adapter, so we always upload exactly one File here.
            const file = files[0];
            const uploadID =
                typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
                    ? crypto.randomUUID()
                    : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
            try {
                // The native upload has no HTTP fallback — it needs the
                // relay tunnel. Make sure it's up (and surface the real
                // reason if it isn't) before opening the upload stream.
                if (serverUuid) await ensureWailsConnection(serverUuid);
                await app.BeamUploadStart!(uploadID, path, file.name, strategy ?? '', file.size);
                let offset = 0;
                while (offset < file.size) {
                    const end = Math.min(offset + UPLOAD_CHUNK_SIZE, file.size);
                    const buf = new Uint8Array(await file.slice(offset, end).arrayBuffer());
                    await app.BeamUploadChunk!(uploadID, uint8ToBase64(buf), offset);
                    offset = end;
                    onProgress(Math.round((offset / Math.max(file.size, 1)) * 100));
                }
                await app.BeamUploadFinish!(uploadID);
                return { success: true };
            } catch (err) {
                try { await app.BeamUploadCancel?.(uploadID); } catch { /* best-effort */ }
                const message = err instanceof Error ? err.message : String(err);
                return { success: false, message };
            }
        },
        downloadFile: async (path, serverUuid, isDir) => {
            // Wails opens its own native save dialog, so we don't need
            // browser progress tracking here.
            await app.DownloadFile(path, serverUuid ?? '', !!isDir);
        },
        selectiveDownload: async (basePath, selected, selectAll, serverUuid) => {
            await app.SelectiveDownload(basePath, selected, selectAll, serverUuid ?? '');
        },
        // Limits in Wails mode follow the active upload transport:
        //   * native gRPC available → unlimited (Node throttles itself)
        //   * HTTP fallback         → admin cap from Core (don't blow
        //     the body limit and overload shared infra)
        getUserLimits: async () => {
            if (typeof app.BeamUploadStart === 'function') {
                return { success: true, uploadLimit: 0, downloadLimit: 0 };
            }
            return apiGetUserLimits();
        },
    };
}
