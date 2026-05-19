// Wails Beam adapter: routes file operations through the Wails-injected
// native bindings (window.go.main.App.*) which in turn talk to the
// BeamRelay over gRPC. Used only when the panel runs inside the Beam
// Desktop App; the regular browser uses panelFileBrowserAdapter.
//
// Detection lives in createBeamAdapter() — components keep their existing
// `const adapter = useMemo(() => createBeamAdapter(...))` call and never
// have to know which transport is active.

import type { FileBrowserAdapter, FileEntry } from '@dylaris/ui-filebrowser';
import { getUserLimits } from '@/lib/api';

// Shape of the bindings exposed by gateway/beam/app/app.go. We type only
// the methods the FileBrowser actually calls; everything else stays
// loosely typed since the rest of the panel doesn't reach into it.
interface WailsAppBindings {
    Login(apiURL: string, username: string, password: string): Promise<{ token: string; username: string; isAdmin: boolean }>;
    SetSession?(apiURL: string, token: string): Promise<void>;
    Logout(): Promise<void>;
    GetBeamConfig(): Promise<{ relay_address: string }>;
    ConnectToServer(serverUUID: string): Promise<void>;
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

// Ensures the Wails-side has the session token + API base for any Core
// calls it needs (initial server list, ticket exchange, etc.). Safe to
// call multiple times; the no-op when bindings don't expose it.
export async function syncSessionWithWails(): Promise<void> {
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

// Connect the Wails relay tunnel to a specific server. Called whenever
// the panel routes to a server's files tab — the Wails side keeps one
// live tunnel and switches it when the active server changes.
let lastConnectedServer = '';
export async function connectWailsToServer(serverUuid: string): Promise<void> {
    const app = getWailsApp();
    if (!app || !serverUuid) return;
    if (serverUuid === lastConnectedServer) return;
    try {
        await app.ConnectToServer(serverUuid);
        lastConnectedServer = serverUuid;
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
        // Uploads still go through the HTTP API for now — Wails-side
        // upload bindings don't exist yet. When they land, swap to native.
        uploadFiles: async () => {
            return { success: false, message: 'Native upload not implemented yet — use the Web Panel for uploads' };
        },
        downloadFile: async (path, serverUuid, isDir) => {
            // Wails opens its own native save dialog, so we don't need
            // browser progress tracking here.
            await app.DownloadFile(path, serverUuid ?? '', !!isDir);
        },
        selectiveDownload: async (basePath, selected, selectAll, serverUuid) => {
            await app.SelectiveDownload(basePath, selected, selectAll, serverUuid ?? '');
        },
        // Limits live with the user, not the transport — reuse the HTTP
        // call so admin-set caps apply consistently in both modes.
        getUserLimits: () => getUserLimits(),
    };
}
