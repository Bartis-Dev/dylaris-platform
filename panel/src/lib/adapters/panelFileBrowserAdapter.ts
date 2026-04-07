import type { FileBrowserAdapter } from '@dylaris/ui-filebrowser';
import { getFiles, getFileContent, saveFile, createFile, deleteFile, renameFile, copyFile, uploadFiles, downloadFile, selectiveDownload, getUserLimits } from '@/lib/api';

export function createPanelAdapter(): FileBrowserAdapter {
  return {
    getFiles: (path, serverUuid) => getFiles(path, serverUuid),
    getFileContent: (path, serverUuid) => getFileContent(path, serverUuid),
    saveFile: (path, content, serverUuid) => saveFile(path, content, serverUuid),
    createFile: (path, isDir, serverUuid) => createFile(path, isDir, serverUuid),
    deleteFile: (path, serverUuid) => deleteFile(path, serverUuid),
    renameFile: (oldPath, newPath, serverUuid) => renameFile(oldPath, newPath, serverUuid),
    copyFile: (srcPath, dstPath, serverUuid) => copyFile(srcPath, dstPath, serverUuid),
    uploadFiles: (path, files, onProgress, strategy, mergeConflict, serverUuid) =>
      uploadFiles(path, files, onProgress, strategy, mergeConflict, serverUuid),
    downloadFile: (path, serverUuid, isDir, onProgress) => downloadFile(path, serverUuid, isDir, onProgress),
    selectiveDownload: (basePath, selected, selectAll, serverUuid, onProgress) =>
      selectiveDownload(basePath, selected, selectAll, serverUuid, onProgress),
    getUserLimits: () => getUserLimits(),
  };
}
