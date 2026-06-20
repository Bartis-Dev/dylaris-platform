export interface FileEntry {
  name: string;
  is_dir: boolean;
  size: number;
  path?: string;
}

export interface FileBrowserAdapter {
  getFiles(path: string, serverUuid?: string): Promise<{ success: boolean; files: FileEntry[]; message?: string }>;
  getFileContent(path: string, serverUuid?: string): Promise<{ success: boolean; content: string; message?: string }>;
  saveFile(path: string, content: string, serverUuid?: string): Promise<{ success: boolean; message?: string }>;
  createFile(path: string, isDir: boolean, serverUuid?: string): Promise<{ success: boolean; message?: string }>;
  deleteFile(path: string, serverUuid?: string): Promise<{ success: boolean; message?: string }>;
  renameFile(oldPath: string, newPath: string, serverUuid?: string): Promise<{ success: boolean; message?: string }>;
  copyFile(srcPath: string, dstPath: string, serverUuid?: string): Promise<{ success: boolean; message?: string }>;
  uploadFiles(
    path: string,
    files: FileList,
    onProgress: (progress: number) => void,
    strategy?: string,
    mergeConflict?: string,
    serverUuid?: string
  ): Promise<{ success: boolean; message?: string }>;
  downloadFile(path: string, serverUuid?: string, isDir?: boolean, onProgress?: (loaded: number, total: number) => void): Promise<void> | void;
  selectiveDownload(basePath: string, selected: string[], selectAll: boolean, serverUuid?: string, onProgress?: (loaded: number, total: number) => void): Promise<void> | void;
  getUserLimits(): Promise<{ success: boolean; uploadLimit: number; downloadLimit: number }>;
}

export interface FileBrowserProps {
  currentServerPath: string;
  serverUuid?: string;
  adapter: FileBrowserAdapter;
  // Read-only mode (e.g. the public demo server). All action buttons stay
  // VISIBLE so users see the full UI, but every mutating action is inert:
  // download/delete/rename/copy/create/upload show a toast instead, and the
  // editor opens for reading with no Save. The backend 403s these anyway
  // (default-deny); this is purely the client-side UX.
  readOnly?: boolean;
}
