import type { FileBrowserAdapter } from '@dylaris/ui-filebrowser';
import { getFiles, getFileContent, saveFile, createFile, deleteFile, renameFile, copyFile, uploadFiles, downloadFile, selectiveDownload, getUserLimits } from '@/lib/api';
import { reportUploadStart, reportUploadProgress, reportUploadFinish } from '@/lib/uploadManager';

export function createPanelAdapter(): FileBrowserAdapter {
  return {
    getFiles: (path, serverUuid) => getFiles(path, serverUuid),
    getFileContent: (path, serverUuid) => getFileContent(path, serverUuid),
    saveFile: (path, content, serverUuid) => saveFile(path, content, serverUuid),
    createFile: (path, isDir, serverUuid) => createFile(path, isDir, serverUuid),
    deleteFile: (path, serverUuid) => deleteFile(path, serverUuid),
    renameFile: (oldPath, newPath, serverUuid) => renameFile(oldPath, newPath, serverUuid),
    copyFile: (srcPath, dstPath, serverUuid) => copyFile(srcPath, dstPath, serverUuid),
    // Browser-side HTTP upload: same FileBrowser contract as Wails so the
    // FileBrowser code stays unaware of which transport is active. We tap
    // the onProgress to mirror progress into the global UploadManager so
    // the navbar widget tracks browser uploads just like native ones.
    // Cancel-from-widget isn't wired here because the underlying api.ts
    // uploadFiles doesn't expose an AbortSignal — Phase 2 work to add it.
    uploadFiles: async (path, files, onProgress, strategy, mergeConflict, serverUuid) => {
      const file = files?.[0];
      const uploadID =
        typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
          ? crypto.randomUUID()
          : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
      if (file) {
        reportUploadStart({
          id: uploadID,
          filename: file.name,
          size: file.size,
          serverUuid: serverUuid ?? '',
          path,
        });
      }
      try {
        const result = await uploadFiles(
          path,
          files,
          (pct) => {
            onProgress(pct);
            if (file) {
              const sent = Math.round((pct / 100) * file.size);
              reportUploadProgress(uploadID, sent);
            }
          },
          strategy,
          mergeConflict,
          serverUuid,
        );
        if (result && (result as { success?: boolean }).success !== false) {
          reportUploadFinish(uploadID, 'done');
        } else {
          const msg = (result as { message?: string })?.message ?? 'upload failed';
          reportUploadFinish(uploadID, 'error', msg);
        }
        return result;
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        reportUploadFinish(uploadID, 'error', message);
        throw err;
      }
    },
    downloadFile: (path, serverUuid, isDir, onProgress) => downloadFile(path, serverUuid, isDir, onProgress),
    selectiveDownload: (basePath, selected, selectAll, serverUuid, onProgress) =>
      selectiveDownload(basePath, selected, selectAll, serverUuid, onProgress),
    getUserLimits: () => getUserLimits(),
  };
}
