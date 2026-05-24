// Wails Beam adapter: routes file operations through the Wails-injected
// native bindings (window.go.main.App.*) which in turn talk to the
// BeamRelay over gRPC. Used only when the panel runs inside the Beam
// Desktop App; the regular browser uses panelFileBrowserAdapter.
//
// Detection lives in createBeamAdapter() — components keep their existing
// `const adapter = useMemo(() => createBeamAdapter(...))` call and never
// have to know which transport is active.

import type { FileBrowserAdapter, FileEntry } from '@dylaris/ui-filebrowser';
import { uploadFiles as apiUploadFiles, getUserLimits as apiGetUserLimits } from '@/lib/api';
import { devLog } from '@/lib/devLog';
import {
    reportUploadStart,
    reportUploadProgress,
    reportUploadFinish,
    isServerUploadLocked,
} from '@/lib/uploadManager';

// uploadLockedError is the error every mutating Beam op throws when the
// target server is currently being written to by an active upload. The
// FileBrowserAdapter contract turns this into a {success:false, message:…}
// pair that the FileBrowser surfaces inline.
const UPLOAD_LOCK_MSG = 'Upload in progress on this server — file actions are paused until it finishes or is cancelled.';

function guardWrite(serverUuid: string | undefined): { success: false; message: string } | null {
    if (serverUuid && isServerUploadLocked(serverUuid)) {
        return { success: false, message: UPLOAD_LOCK_MSG };
    }
    return null;
}

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
    devLog('beam.session', 'info', 'syncSessionWithWails: start');
    const app = getWailsApp();
    if (!app || typeof app.SetSession !== 'function') {
        devLog('beam.session', 'warn', 'syncSessionWithWails: Wails app or SetSession binding unavailable');
        return;
    }
    const token = localStorage.getItem('authToken') || localStorage.getItem('token');
    if (!token) {
        devLog('beam.session', 'warn', 'syncSessionWithWails: no auth token in localStorage');
        return;
    }
    const apiUrl = process.env.NEXT_PUBLIC_API_URL || window.location.origin + '/api';
    devLog('beam.session', 'info', `SetSession → ${apiUrl}`);
    try {
        await app.SetSession(apiUrl, token);
        devLog('beam.session', 'info', 'SetSession OK');
    } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        devLog('beam.session', 'error', `SetSession failed: ${message}`);
        console.warn('Wails SetSession failed:', err);
    }
}

// ensureWailsConnection points the native relay tunnel at `serverUuid`,
// throwing the real Go-side error on failure so callers can surface it
// (the Wails side lazily resolves the relay address, so this also
// covers first-call bootstrap). Deduped: a server that's already the
// live tunnel is a no-op — unless { force: true } is passed.
//
// Why force exists: the cheap "did we already connect?" check is just a
// string compare, it can't tell whether the underlying gRPC tunnel
// still works. Anything that disturbs the relay (restart, network
// blip, panel-side relay-address change) leaves us thinking we're
// connected when we aren't. Operations that are about to push real
// bytes (uploads especially) opt into a fresh dial so they fail fast
// with the actual reason instead of an Unavailable on a dead tunnel.
let lastConnectedServer = '';
export async function ensureWailsConnection(
    serverUuid: string,
    opts: { force?: boolean } = {},
): Promise<void> {
    devLog('beam.connect', 'info', `ensureWailsConnection: serverUuid=${serverUuid}, force=${opts.force ?? false}`);
    const app = getWailsApp();
    if (!app || !serverUuid) {
        devLog('beam.connect', 'warn', 'ensureWailsConnection: skipped (no Wails app or no serverUuid)');
        return;
    }
    await syncSessionWithWails();
    if (!opts.force && serverUuid === lastConnectedServer) {
        devLog('beam.connect', 'info', 'ensureWailsConnection: cache hit, skipping reconnect');
        return;
    }
    devLog('beam.connect', 'info', `ConnectToServer(${serverUuid}) → calling Wails binding`);
    // Drop the cache before the call: if ConnectToServer throws partway
    // through, the next attempt must re-dial — otherwise we'd treat a
    // failed half-connect as "still good".
    lastConnectedServer = '';
    try {
        await app.ConnectToServer(serverUuid);
        lastConnectedServer = serverUuid;
        devLog('beam.connect', 'info', 'ConnectToServer OK — relay tunnel established');
    } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        devLog('beam.connect', 'error', `ConnectToServer FAILED: ${message}`);
        throw err;
    }
}

// resetWailsConnectionCache invalidates the lastConnectedServer memo so
// the next ensureWailsConnection call re-dials. Used by callers that
// know the tunnel is stale (e.g. logout, server switch, explicit retry).
export function resetWailsConnectionCache(): void {
    lastConnectedServer = '';
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
    const wrap = async (label: string, fn: () => Promise<unknown>): Promise<WrapResult> => {
        try {
            // Wait for the session to reach the Wails side first — the
            // native ops reject with "not logged in" until SetSession
            // has run, which would otherwise flash in the file list.
            await syncSessionWithWails();
            const result = await fn();
            if (result === undefined || result === null) return { success: true };
            if (typeof result === 'object' && 'success' in (result as Record<string, unknown>)) {
                const r = result as WrapResult;
                if (!r.success) {
                    devLog('beam.op', 'error', `${label} returned success=false: ${r.message ?? '(no message)'}`);
                }
                return r;
            }
            return { success: true, ...(result as Record<string, unknown>) };
        } catch (err) {
            const message = err instanceof Error ? err.message : String(err);
            devLog('beam.op', 'error', `${label} threw: ${message}`);
            return { success: false, message };
        }
    };

    return {
        // Reads stay open even while an upload is mid-stream so the user
        // can navigate the FileBrowser to watch progress.
        getFiles: (path, serverUuid) => wrap(`ListFiles ${path}`, () => app.ListFiles(path, serverUuid ?? '')) as ReturnType<FileBrowserAdapter['getFiles']>,
        getFileContent: (path, serverUuid) => wrap(`GetFileContent ${path}`, () => app.GetFileContent(path, serverUuid ?? '')) as ReturnType<FileBrowserAdapter['getFileContent']>,
        // Writes are blocked at the adapter so the FileBrowser surfaces a
        // clean inline error instead of letting an op race a live upload.
        saveFile: async (path, content, serverUuid) => guardWrite(serverUuid) ?? wrap(`SaveFile ${path}`, () => app.SaveFile(path, content, serverUuid ?? '')),
        createFile: async (path, isDir, serverUuid) => guardWrite(serverUuid) ?? wrap(`CreateFile ${path}`, () => app.CreateFile(path, isDir, serverUuid ?? '')),
        deleteFile: async (path, serverUuid) => guardWrite(serverUuid) ?? wrap(`DeleteFile ${path}`, () => app.DeleteFile(path, serverUuid ?? '')),
        renameFile: async (oldPath, newPath, serverUuid) => guardWrite(serverUuid) ?? wrap(`RenameFile ${oldPath}→${newPath}`, () => app.RenameFile(oldPath, newPath, serverUuid ?? '')),
        copyFile: async (srcPath, dstPath, serverUuid) => guardWrite(serverUuid) ?? wrap(`CopyFile ${srcPath}→${dstPath}`, () => app.CopyFile(srcPath, dstPath, serverUuid ?? '')),
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
                devLog('beam.upload', 'warn', 'native bindings missing — falling back to HTTP upload via Core');
                return apiUploadFiles(path, files, onProgress, strategy, mergeConflict, serverUuid);
            }
            if (!files || files.length === 0) {
                devLog('beam.upload', 'info', 'uploadFiles: empty file list, no-op');
                return { success: true };
            }
            // FileBrowser zips folders into a single .zip before calling
            // the adapter, so we always upload exactly one File here.
            const file = files[0];
            const uploadID =
                typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
                    ? crypto.randomUUID()
                    : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
            devLog('beam.upload', 'info', `uploadFiles: file="${file.name}" size=${file.size} path="${path}" serverUuid=${serverUuid ?? '?'} uploadID=${uploadID}`);

            // cancelRequested flips true when the user clicks the cancel
            // button in the UploadManager widget. The chunk loop checks it
            // between writes so a click takes effect on the next chunk
            // boundary instead of having to abort mid-WriteAt. We also
            // call app.BeamUploadCancel to tear the gRPC stream down
            // server-side immediately — that's what actually deletes the
            // temp file. The local flag just suppresses further chunks.
            let cancelRequested = false;
            const cancel = () => {
                cancelRequested = true;
                try { app.BeamUploadCancel?.(uploadID); } catch { /* best-effort */ }
            };

            // Register the job with the global UploadManager so the navbar
            // widget + modal can show it, and so the cancel button on the
            // row can invoke the cancel callback above.
            reportUploadStart({
                id: uploadID,
                filename: file.name,
                size: file.size,
                serverUuid: serverUuid ?? '',
                path,
                cancel,
            });

            try {
                // The native upload has no HTTP fallback — it needs the
                // relay tunnel. Force a fresh ConnectToServer here even
                // if we think we're already connected: a stale tunnel
                // (relay restart, network drop, address change) would
                // otherwise surface as a misleading "could not reach"
                // gRPC error on the upload stream instead of failing
                // upfront with the real reason.
                if (serverUuid) {
                    devLog('beam.upload', 'info', 'forcing fresh ConnectToServer before upload');
                    await ensureWailsConnection(serverUuid, { force: true });
                }
                devLog('beam.upload', 'info', `BeamUploadStart: opening gRPC stream (strategy="${strategy ?? ''}")`);
                await app.BeamUploadStart!(uploadID, path, file.name, strategy ?? '', file.size);
                devLog('beam.upload', 'info', 'BeamUploadStart OK — streaming chunks');
                let offset = 0;
                let chunks = 0;
                while (offset < file.size) {
                    if (cancelRequested) {
                        devLog('beam.upload', 'warn', `upload "${file.name}" cancelled by user at offset ${offset}`);
                        throw new Error('cancelled');
                    }
                    const end = Math.min(offset + UPLOAD_CHUNK_SIZE, file.size);
                    const buf = new Uint8Array(await file.slice(offset, end).arrayBuffer());
                    try {
                        await app.BeamUploadChunk!(uploadID, uint8ToBase64(buf), offset);
                    } catch (chunkErr) {
                        const m = chunkErr instanceof Error ? chunkErr.message : String(chunkErr);
                        devLog('beam.upload', 'error', `BeamUploadChunk FAILED at offset ${offset} (chunk #${chunks}): ${m}`);
                        throw chunkErr;
                    }
                    offset = end;
                    chunks++;
                    onProgress(Math.round((offset / Math.max(file.size, 1)) * 100));
                    reportUploadProgress(uploadID, offset);
                }
                devLog('beam.upload', 'info', `streamed ${chunks} chunks (${file.size} bytes total) — calling BeamUploadFinish`);
                await app.BeamUploadFinish!(uploadID);
                devLog('beam.upload', 'info', `upload "${file.name}" finished successfully`);
                reportUploadFinish(uploadID, 'done');
                return { success: true };
            } catch (err) {
                try { await app.BeamUploadCancel?.(uploadID); } catch { /* best-effort */ }
                const message = err instanceof Error ? err.message : String(err);
                devLog('beam.upload', 'error', `upload "${file.name}" FAILED: ${message}`);
                reportUploadFinish(
                    uploadID,
                    cancelRequested || message === 'cancelled' ? 'cancelled' : 'error',
                    message,
                );
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
