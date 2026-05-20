"use client";

import React, { useState, useEffect, useMemo, useRef, useCallback, lazy, Suspense } from 'react';
import { Folder, FileText, File as FileIcon, Search, Upload, Plus, CornerDownLeft, ExternalLink, FilePen, Pencil, Copy, Download, Trash2, Check, X, ArrowUp, ArrowDown, ChevronRight, ChevronDown } from 'lucide-react';
import type { FileEntry, FileBrowserAdapter, FileBrowserProps } from './types';
import { formatBytes, validFilenameRegex, editableExtensions, getCopyName } from './utils';

// Lazy-load the CodeMirror bundle — only pulled in when an edit modal opens.
const CodeMirrorEditor = lazy(() => import('./CodeMirrorEditor'));

// We load JSZip from a CDN, so no import is needed here.
declare const JSZip: any;

type PopupMode = 'create' | 'copy' | 'rename' | null;
type UploadPopupView = 'select' | 'progress' | 'conflict';

// useDelayedFlag mirrors `active`, but only flips on once it has stayed
// true for `delayMs`. A fast operation (folder switch that resolves in
// well under the delay) never trips it — so the "Loading…" line doesn't
// flash on every quick navigation, only on genuinely slow loads.
function useDelayedFlag(active: boolean, delayMs: number): boolean {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (!active) {
      setShown(false);
      return;
    }
    const t = setTimeout(() => setShown(true), delayMs);
    return () => clearTimeout(t);
  }, [active, delayMs]);
  return shown;
}

const FileBrowser: React.FC<FileBrowserProps> = ({ currentServerPath, serverUuid, adapter }) => {
  const [files, setFiles] = useState<FileEntry[]>([]);
  const [currentPath, setCurrentPath] = useState(currentServerPath);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  // Only surface "Loading…" if the op runs longer than this — fast
  // folder switches stay silent instead of flashing the indicator.
  const showLoading = useDelayedFlag(loading, 350);
  const [toastMessage, setToastMessage] = useState<{ message: string; type: 'error' | 'info' } | null>(null);
  
  const [globalSearchTerm, setGlobalSearchTerm] = useState('');
  const [isSearching, setIsSearching] = useState(false);
  const [searchResults, setSearchResults] = useState<FileEntry[]>([]);

  const [popupMode, setPopupMode] = useState<PopupMode>(null);
  const [actionTarget, setActionTarget] = useState<FileEntry | null>(null);
  const [newName, setNewName] = useState('');
  const [newFileType, setNewFileType] = useState<'file' | 'folder'>('file');
  const [popupError, setPopupError] = useState('');

  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [fileToDelete, setFileToDelete] = useState<FileEntry | null>(null);

  const [showEditPopup, setShowEditPopup] = useState(false);
  const [editingFile, setEditingFile] = useState('');
  const [fileContent, setFileContent] = useState('');
  const [originalFileContent, setOriginalFileContent] = useState('');
  const [editorPopupStyle] = useState({ width: '80vw', maxWidth: '1400px', minWidth: '700px' });
  const [isEditorLoading, setIsEditorLoading] = useState(false);
  const [isEditingEnabled, setIsEditingEnabled] = useState(false);
  const [isRenaming, setIsRenaming] = useState(false);
  const [tempFileName, setTempFileName] = useState('');
  const renameInputRef = useRef<HTMLInputElement>(null);
  const [searchTerm, setSearchTerm] = useState('');
  const [searchMatches, setSearchMatches] = useState<number[]>([]);
  const [currentMatchIndex, setCurrentMatchIndex] = useState(-1);


  const [showUploadPopup, setShowUploadPopup] = useState(false);
  const [filesToUpload, setFilesToUpload] = useState<any[]>([]);
  const [isDragging, setIsDragging] = useState(false);
  const [isWindowDragging, setIsWindowDragging] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const folderInputRef = useRef<HTMLInputElement>(null);
  const [uploadProgress, setUploadProgress] = useState<number | null>(null);
  const [uploadStatus, setUploadStatus] = useState('');

  // States for the upload flow
  const [uploadPopupView, setUploadPopupView] = useState<UploadPopupView>('select');
  const [uploadItems, setUploadItems] = useState<{ items: any[], isFolder: boolean, name: string } | null>(null);
  const [conflictFile, setConflictFile] = useState<{file: File, newName: string, isFolderConflict: boolean} | null>(null);

  // Transfer limits
  const [uploadLimit, setUploadLimit] = useState<number>(0);
  const [downloadLimit, setDownloadLimit] = useState<number>(0);

  // Download progress
  const [downloadProgress, setDownloadProgress] = useState<{ filename: string; loaded: number; total: number } | null>(null);

  // Selective download popup
  const [selectiveDownloadTarget, setSelectiveDownloadTarget] = useState<FileEntry | null>(null);
  const [selectiveTree, setSelectiveTree] = useState<Record<string, FileEntry[]>>({});
  const [selectiveExpanded, setSelectiveExpanded] = useState<Set<string>>(new Set());
  const [selectiveChecked, setSelectiveChecked] = useState<Set<string>>(new Set());
  const [selectiveAll, setSelectiveAll] = useState(true);
  const [selectiveLoading, setSelectiveLoading] = useState(false);
  const [selectiveDownloading, setSelectiveDownloading] = useState(false);

  // JSZip-Bibliothek laden + Transfer Limits abrufen
  useEffect(() => {
    const jszipScript = document.createElement('script');
    jszipScript.src = "https://cdnjs.cloudflare.com/ajax/libs/jszip/3.10.1/jszip.min.js";
    jszipScript.async = true;
    document.body.appendChild(jszipScript);

    adapter.getUserLimits().then(res => {
      if (res.success) {
        setUploadLimit(res.uploadLimit);
        setDownloadLimit(res.downloadLimit);
      }
    });

    return () => {
        document.body.removeChild(jszipScript);
    }
  }, []);


  const fetchFiles = async (path: string) => {
    setLoading(true);
    setError('');
    const result = await adapter.getFiles(path, serverUuid);
    if (result.success) {
      setFiles(result.files);
      setCurrentPath(path);
    } else {
      setError(result.message || 'Unknown error');
    }
    setLoading(false);
  };
  
  // --- Selective Download Helpers ---
  const openSelectiveDownload = async (folder: FileEntry) => {
    setSelectiveDownloadTarget(folder);
    setSelectiveAll(true);
    setSelectiveChecked(new Set());
    setSelectiveExpanded(new Set());
    setSelectiveTree({});
    setSelectiveDownloading(false);
    // Load first level
    const folderPath = currentPath ? `${currentPath}/${folder.name}` : folder.name;
    setSelectiveLoading(true);
    const result = await adapter.getFiles(folderPath, serverUuid);
    if (result.success) {
      setSelectiveTree({ '': result.files });
    }
    setSelectiveLoading(false);
  };

  const toggleSelectiveExpand = async (relativePath: string) => {
    const newExpanded = new Set(selectiveExpanded);
    if (newExpanded.has(relativePath)) {
      newExpanded.delete(relativePath);
    } else {
      newExpanded.add(relativePath);
      // Lazy load if not yet loaded
      if (!selectiveTree[relativePath]) {
        const folderName = selectiveDownloadTarget?.name || '';
        const basePath = currentPath ? `${currentPath}/${folderName}` : folderName;
        const fullPath = relativePath ? `${basePath}/${relativePath}` : basePath;
        const result = await adapter.getFiles(fullPath, serverUuid);
        if (result.success) {
          setSelectiveTree(prev => ({ ...prev, [relativePath]: result.files }));
        }
      }
    }
    setSelectiveExpanded(newExpanded);
  };

  const toggleSelectiveCheck = (relativePath: string) => {
    setSelectiveAll(false);
    const newChecked = new Set(selectiveChecked);
    if (newChecked.has(relativePath)) {
      newChecked.delete(relativePath);
    } else {
      newChecked.add(relativePath);
    }
    setSelectiveChecked(newChecked);
  };

  const handleSelectiveDownload = async () => {
    if (!selectiveDownloadTarget) return;
    setSelectiveDownloading(true);
    setSelectiveDownloadTarget(null);
    const folderName = selectiveDownloadTarget.name;
    const basePath = currentPath ? `${currentPath}/${folderName}` : folderName;
    const filename = `${folderName}.zip`;
    setDownloadProgress({ filename, loaded: 0, total: 0 });
    try {
      const onProgress = (loaded: number, total: number) =>
        setDownloadProgress(prev => prev ? { ...prev, loaded, total: total || prev.total } : null);
      if (selectiveAll) {
        await adapter.selectiveDownload(basePath, [], true, serverUuid, onProgress);
      } else {
        await adapter.selectiveDownload(basePath, Array.from(selectiveChecked), false, serverUuid, onProgress);
      }
    } catch (err) {
      console.error('Selective download failed:', err);
      setToastMessage({ message: 'Download failed.', type: 'error' });
    }
    setDownloadProgress(null);
    setSelectiveDownloading(false);
  };

  const renderSelectiveTree = (parentPath: string, depth: number): React.ReactNode => {
    const entries = selectiveTree[parentPath];
    if (!entries) return null;

    return entries.map(entry => {
      const entryPath = parentPath ? `${parentPath}/${entry.name}` : entry.name;
      const isExpanded = selectiveExpanded.has(entryPath);
      const isChecked = selectiveAll || selectiveChecked.has(entryPath);

      return (
        <div key={entryPath}>
          <div className="flex items-center gap-1.5 py-1 px-2 hover:bg-(--base-02) rounded-sm" style={{ paddingLeft: `${depth * 20 + 8}px` }}>
            {entry.is_dir ? (
              <button onClick={() => toggleSelectiveExpand(entryPath)} className="p-0.5 text-(--base-06) hover:text-(--base-09)">
                {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
              </button>
            ) : (
              <span className="w-[18px]" />
            )}
            <input
              type="checkbox"
              checked={isChecked}
              onChange={() => toggleSelectiveCheck(entryPath)}
              className="accent-(--accent) w-3.5 h-3.5"
            />
            {entry.is_dir ? <Folder size={14} className="text-(--primary-light) shrink-0" /> : <FileIcon size={14} className="text-(--base-06) shrink-0" />}
            <span className="text-sm text-(--base-09) truncate">{entry.name}</span>
            <span className="text-xs text-(--base-06) ml-auto shrink-0">{formatBytes(entry.size)}</span>
          </div>
          {entry.is_dir && isExpanded && renderSelectiveTree(entryPath, depth + 1)}
        </div>
      );
    });
  };

  useEffect(() => {
    fetchFiles(currentServerPath);
  }, [currentServerPath]);

  useEffect(() => {
    if (showEditPopup && editingFile) {
      const loadContent = async () => {
        setIsEditorLoading(true);
        setFileContent('');
        const fullPath = currentPath ? `${currentPath}/${editingFile}` : editingFile;
        try {
          const result = await adapter.getFileContent(fullPath, serverUuid);
          if (result.success) {
            setFileContent(result.content);
            setOriginalFileContent(result.content);
          } else {
            setError(result.message || 'Unknown error');
            setShowEditPopup(false);
          }
        } catch {
          setError('Failed to load file content');
          setShowEditPopup(false);
        } finally {
          setIsEditorLoading(false);
        }
      };
      loadContent();
    }
  }, [showEditPopup, editingFile, currentPath]);
  
  useEffect(() => {
    if(showEditPopup) {
      document.body.style.overflow = 'hidden';
    } else {
      document.body.style.overflow = 'unset';
    }
    return () => {
      document.body.style.overflow = 'unset';
    };
  }, [showEditPopup]);
  
   useEffect(() => {
    const handleDragEnter = (e: DragEvent) => {
        e.preventDefault();
        setIsWindowDragging(true);
    };

    const handleDragLeave = (e: DragEvent) => {
        e.preventDefault();
        if (e.relatedTarget === null) {
            setIsWindowDragging(false);
        }
    };

    const handleDrop = (e: DragEvent) => {
        e.preventDefault();
        setIsWindowDragging(false);
    };

    window.addEventListener('dragenter', handleDragEnter);
    window.addEventListener('dragleave', handleDragLeave);
    window.addEventListener('drop', handleDrop);
    window.addEventListener('dragover', (e) => e.preventDefault());


    return () => {
        window.removeEventListener('dragenter', handleDragEnter);
        window.removeEventListener('dragleave', handleDragLeave);
        window.removeEventListener('drop', handleDrop);
    };
  }, []);
  
  const closeAllPopups = useCallback(() => {
    setShowEditPopup(false);
    setShowUploadPopup(false);
    setShowDeleteConfirm(false);
    setPopupMode(null);
  }, []);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
        if (e.key === 'Escape') {
            closeAllPopups();
        }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [closeAllPopups]);
  
    const handleRecursiveSearch = useCallback(async (path: string, term: string) => {
        let results: FileEntry[] = [];
        const response = await adapter.getFiles(path, serverUuid);

        if (response.success) {
            for (const file of response.files) {
                const fullPath = path ? `${path}/${file.name}` : file.name;
                if (file.name.toLowerCase().includes(term.toLowerCase())) {
                    results.push({ ...file, path: fullPath });
                }
                if (file.is_dir) {
                    const subResults = await handleRecursiveSearch(fullPath, term);
                    results = results.concat(subResults);
                }
            }
        }
        return results;
    }, []);

    useEffect(() => {
        const search = async () => {
            if (globalSearchTerm) {
                setIsSearching(true);
                const results = await handleRecursiveSearch(currentPath, globalSearchTerm);
                setSearchResults(results);
                setIsSearching(false);
            } else {
                setSearchResults([]);
            }
        };
        const timer = setTimeout(() => {
          search();
        }, 300); // Debounce search
        
        return () => clearTimeout(timer);
    }, [globalSearchTerm, currentPath, handleRecursiveSearch]);

  const handleRenameClick = () => {
    setTempFileName(editingFile);
    setIsRenaming(true);
    setTimeout(() => renameInputRef.current?.focus(), 0);
  };

  const handleRenameSubmit = async () => {
    if (!validFilenameRegex.test(tempFileName)) {
      showToast("Invalid filename.", 'error');
      return;
    }
    const oldPath = currentPath ? `${currentPath}/${editingFile}` : editingFile;
    const newPath = currentPath ? `${currentPath}/${tempFileName}` : tempFileName;
    
    const result = await adapter.renameFile(oldPath, newPath, serverUuid);
    if(result.success){
        setEditingFile(tempFileName);
        setIsRenaming(false);
        fetchFiles(currentPath);
    } else {
        showToast(result.message || 'Operation failed', 'error');
    }
  };


  const showToast = (message: string, type: 'error' | 'info' = 'info') => {
    setToastMessage({ message, type });
    setTimeout(() => {
      setToastMessage(null);
    }, 3000); 
  };

  const handleFileClick = (name: string, isDir: boolean, path?: string) => {
     if(path && path !== currentPath) {
        const parentPath = path.substring(0, path.lastIndexOf('/'));
        setGlobalSearchTerm('');
        fetchFiles(parentPath);
        return;
    }
    if (isDir) {
      const newPath = currentPath ? `${currentPath}/${name}` : name;
      fetchFiles(newPath);
    } else {
      const isEditable = editableExtensions.some(ext => name.endsWith(ext));
      if (isEditable) {
        setIsEditorLoading(true);
        setEditingFile(name);
        setShowEditPopup(true);
      } else {
        showToast("This file type cannot be opened.", 'error');
      }
    }
  };

  const handleGoUp = () => {
    if (currentPath) {
      const pathParts = currentPath.split('/');
      pathParts.pop();
      const parentPath = pathParts.join('/');
      fetchFiles(parentPath);
    }
  };
  
  const closeEditPopup = () => {
      setShowEditPopup(false);
      setIsEditingEnabled(false);
      setSearchTerm('');
      setIsRenaming(false);
  }

  const handleSaveFile = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    const fullPath = currentPath ? `${currentPath}/${editingFile}` : editingFile;
    const result = await adapter.saveFile(fullPath, fileContent, serverUuid);
    if (result.success) {
      closeEditPopup();
      setError('');
    } else {
      showToast(result.message || 'Operation failed', 'error');
    }
    setLoading(false);
  };
  
  const handleSearchChange = (e: React.ChangeEvent<HTMLInputElement>) => {
      setSearchTerm(e.target.value);
  }

  useEffect(() => {
      if (searchTerm) {
          const regex = new RegExp(searchTerm, 'gi');
          const matches = [...fileContent.matchAll(regex)].map(match => match.index as number);
          setSearchMatches(matches);
          setCurrentMatchIndex(matches.length > 0 ? 0 : -1);
      } else {
          setSearchMatches([]);
          setCurrentMatchIndex(-1);
      }
  }, [searchTerm, fileContent]);
  
  const goToNextMatch = () => {
    if (searchMatches.length > 0) {
      setCurrentMatchIndex((prevIndex) => (prevIndex + 1) % searchMatches.length);
    }
  };

  const goToPrevMatch = () => {
    if (searchMatches.length > 0) {
      setCurrentMatchIndex((prevIndex) => (prevIndex - 1 + searchMatches.length) % searchMatches.length);
    }
  };
  
  // Editor scroll + jump-to-match is owned by CodeMirror now; we only
  // recompute the match counter for the search UI badge.

  const handleDeleteClick = (e: React.MouseEvent, file: FileEntry) => {
    e.stopPropagation();
    setFileToDelete(file);
    setShowDeleteConfirm(true);
  };

  const handleConfirmDelete = async () => {
    if (!fileToDelete) return;
    setLoading(true);
    const fullPath = currentPath ? `${currentPath}/${fileToDelete.name}` : fileToDelete.name;
    const result = await adapter.deleteFile(fullPath, serverUuid);
    setShowDeleteConfirm(false);
    setFileToDelete(null);
    if (result.success) {
      fetchFiles(currentPath);
      setError('');
    } else {
      setError(result.message || 'Unknown error');
    }
    setLoading(false);
  };

  const closePopup = () => {
    setPopupMode(null);
    setActionTarget(null);
    setNewName('');
    setPopupError('');
  };

  const handlePopupSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!validFilenameRegex.test(newName)) {
      setPopupError("Invalid name. Use letters, numbers, dot, underscore, hyphen.");
      return;
    }
    setLoading(true);
    setPopupError('');
    let result;
    const oldPath = actionTarget ? (actionTarget.path || (currentPath ? `${currentPath}/${actionTarget.name}` : actionTarget.name)) : '';
    const newPath = oldPath.substring(0, oldPath.lastIndexOf('/') + 1) + newName;

    switch (popupMode) {
      case 'create': result = await adapter.createFile(newPath, newFileType === 'folder', serverUuid); break;
      case 'copy': result = await adapter.copyFile(oldPath, newPath, serverUuid); break;
      case 'rename': result = await adapter.renameFile(oldPath, newPath, serverUuid); break;
    }
    if (result && result.success) {
      closePopup();
      fetchFiles(currentPath);
    } else if (result) {
      setPopupError(result.message || 'Operation failed');
    }
    setLoading(false);
  };

  const handleDragOver = (e: React.DragEvent<HTMLDivElement>) => { e.preventDefault(); setIsDragging(true); };
  const handleDragLeave = (e: React.DragEvent<HTMLDivElement>) => { e.preventDefault(); setIsDragging(false); };
  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setIsDragging(false);
    if(e.dataTransfer.items) {
      setFilesToUpload(Array.from(e.dataTransfer.items)); 
    }
  };
  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    if(e.target.files) {
      setFilesToUpload(Array.from(e.target.files));
    }
  };
  
  const startUploadProcess = async () => {
    if (!filesToUpload || filesToUpload.length === 0) {
      setPopupError("Please select files or a folder.");
      return;
    }
    setPopupError('');
    const firstItem = filesToUpload[0];
    const isFolder = firstItem.webkitGetAsEntry?.()?.isDirectory || (firstItem.webkitRelativePath && firstItem.webkitRelativePath.includes('/'));
    
    let originalName = '';
    if (isFolder) {
      originalName = firstItem.webkitRelativePath ? firstItem.webkitRelativePath.split('/')[0] : firstItem.webkitGetAsEntry().name;
    } else {
       const fileObjects = await Promise.all(
          Array.from(filesToUpload).map(async (item: any) => item.kind === 'file' ? item.getAsFile() : item)
        );
      if(fileObjects.length > 1) {
        setPopupError("For simplicity, please upload multiple files as a single zip archive.");
        return;
      }
      originalName = fileObjects[0].name;
    }
    
    const finalName = originalName.replace(/[^a-zA-Z0-9._-]/g, '_');

    // Check upload size limit
    if (uploadLimit > 0) {
      const totalSize = Array.from(filesToUpload).reduce((sum: number, f: any) => sum + (f.size || 0), 0);
      if (totalSize > uploadLimit) {
        const limitStr = uploadLimit >= 1024 * 1024 * 1024
          ? `${(uploadLimit / (1024 * 1024 * 1024)).toFixed(1)} GB`
          : `${Math.round(uploadLimit / (1024 * 1024))} MB`;
        setPopupError(`File size exceeds upload limit of ${limitStr}.`);
        return;
      }
    }

    // Save items for potential retries from conflict resolution
    setUploadItems({ items: filesToUpload, isFolder, name: finalName });

    executeUpload(finalName, filesToUpload, isFolder);
  };

  const executeUpload = async (finalName: string, items: any[], isFolder: boolean, strategy: 'check' | 'replace' | 'merge' = 'check', mergeConflictStrategy? : 'replace' | 'ignore') => {
    if (!validFilenameRegex.test(finalName)) {
      setPopupError("Invalid filename. Only letters, numbers, dot, underscore, and hyphen are allowed.");
      setUploadPopupView('select');
      return;
    }

    if (strategy === 'check') {
        const conflictObject = isFolder ? files.find(f => f.name === finalName && f.is_dir) : files.find(f => f.name === finalName && !f.is_dir);
        if (conflictObject) {
            setConflictFile({ file: new File([], finalName), newName: `new_${finalName}`, isFolderConflict: isFolder });
            setUploadPopupView('conflict');
            return;
        }
    }

    setLoading(true);
    setPopupError('');
    setUploadPopupView('progress');
    setUploadStatus('Preparing upload...');
    setUploadProgress(0);
    
    const filesToFileList = (files: File[]): FileList => {
      const dataTransfer = new DataTransfer();
      files.forEach(file => dataTransfer.items.add(file));
      return dataTransfer.files;
    };
    
    try {
      if (typeof JSZip === 'undefined') throw new Error('Zip library not loaded.');
      let filesToActuallyUpload: File[];

      if (isFolder) {
        setUploadStatus(`Zipping ${finalName}...`);
        const zip = new JSZip();
        
        const traverseFileTree = async (entry: any, currentZipFolder: any): Promise<void> => {
          if (entry.isFile) {
            const file = await new Promise<File>(resolve => entry.file(resolve));
            if (file.name === ".DS_Store") return;
            currentZipFolder.file(file.name, file);
          } else if (entry.isDirectory) {
            const newFolder = currentZipFolder.folder(entry.name);
            const dirReader = entry.createReader();
            let directoryEntries: any[] = [];
            let readEntries: any[] = await new Promise(resolve => dirReader.readEntries(resolve));
            while (readEntries.length > 0) {
              directoryEntries.push(...readEntries);
              readEntries = await new Promise(resolve => dirReader.readEntries(resolve));
            }
            await Promise.all(directoryEntries.map((dirEntry: any) => traverseFileTree(dirEntry, newFolder)));
          }
        };
        
        if (items[0].webkitRelativePath) { 
            const topLevelFolder = items[0].webkitRelativePath.split('/')[0];
            const rootZipFolder = (strategy === 'merge') ? zip : zip.folder(finalName);

            for (const file of items) {
                if (file.name === ".DS_Store") continue;
                const pathInZip = (strategy === 'merge')
                    ? file.webkitRelativePath.substring(topLevelFolder.length + 1)
                    : file.webkitRelativePath.substring(topLevelFolder.length + 1); 
                
                if (pathInZip) {
                    rootZipFolder!.file(pathInZip, file);
                }
            }
        } else {
            const entry = items[0].webkitGetAsEntry();
            const rootZipFolder = (strategy === 'merge') ? zip : zip.folder(finalName);
            
            const dirReader = entry.createReader();
            let allEntries: any[] = [];
            let readResults: any[] = await new Promise(res => dirReader.readEntries(res));
            while(readResults.length > 0){
                allEntries.push(...readResults);
                readResults = await new Promise(res => dirReader.readEntries(res));
            }
            await Promise.all(allEntries.map((dirEntry: any) => traverseFileTree(dirEntry, rootZipFolder)));
        }
        
        const content = await zip.generateAsync({ type: "blob" }, (metadata: { percent: number }) => {
          setUploadProgress(Math.round(metadata.percent));
        });
        filesToActuallyUpload = [new File([content], `${finalName}.zip`)];
      } else {
        filesToActuallyUpload = await Promise.all(items.map(async (item: any) => {
             const file = item.kind === 'file' ? await item.getAsFile() : item;
             return new File([file], finalName, {type: file.type});
        }));
      }

      setUploadStatus('Uploading...');
      const apiStrategy = strategy === 'check' ? undefined : strategy;
      const result = await adapter.uploadFiles(currentPath, filesToFileList(filesToActuallyUpload), setUploadProgress, apiStrategy, mergeConflictStrategy, serverUuid);
      if (result.success) {
        closeUploadPopup();
        fetchFiles(currentPath);
      } else {
        setPopupError(result.message || 'Operation failed');
        setUploadPopupView('select');
      }
    } catch (err: any) {
      setPopupError(err.message || 'An unexpected error occurred.');
      setUploadPopupView('select');
    } finally {
      setLoading(false);
      setUploadProgress(null);
    }
  };


  const closeUploadPopup = () => {
    setShowUploadPopup(false);
    setFilesToUpload([]);
    setPopupError('');
    setUploadProgress(null);
    setUploadStatus('');
    setUploadItems(null);
    setConflictFile(null);
    setUploadPopupView('select');
  };

  const handleNameChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const sanitized = e.target.value.replace(/[^a-zA-Z0-9._-]/g, '');
    setNewName(sanitized);
  };


  const renderBreadcrumbs = () => {
    const pathSegments = currentPath.split('/').filter((p: string) => p);
    const breadcrumbs = [{ name: 'servers', path: '' }];
    let cumulativePath = '';
    for (const segment of pathSegments) {
      cumulativePath = cumulativePath ? `${cumulativePath}/${segment}` : segment;
      breadcrumbs.push({ name: segment, path: cumulativePath });
    }
    return (
      <div className="flex items-center text-xl text-(--base-09) flex-wrap">
        {breadcrumbs.map((crumb, index) => (
          <React.Fragment key={crumb.path}>
            {index > 0 && <span className="mx-2 text-(--base-07)">/</span>}
            {index < breadcrumbs.length - 1 ? (
              <button onClick={() => fetchFiles(crumb.path)} className="hover:text-(--primary-light) transition-colors">{crumb.name}</button>
            ) : (
              <span className="text-(--base-07)">{crumb.name}</span>
            )}
          </React.Fragment>
        ))}
      </div>
    );
  };

  const getPopupTitle = () => {
    switch (popupMode) {
      case 'create': return 'Create New';
      case 'copy': return `Copy ${actionTarget?.name}`;
      case 'rename': return `Rename ${actionTarget?.name}`;
      default: return '';
    }
  };
  
  const sortedFiles = useMemo(() => {
    const filesToDisplay = globalSearchTerm ? searchResults : files;
    
    return [...filesToDisplay].sort((a, b) => {
        if (a.is_dir && !b.is_dir) return -1;
        if (!a.is_dir && b.is_dir) return 1;
        return a.name.localeCompare(b.name);
    });
  }, [files, globalSearchTerm, searchResults]);
  
  const renderFileRepresentation = (file: FileEntry) => {
    if (file.is_dir) {
      return <Folder size={36} className="mr-3 text-(--primary-light)" />;
    }

    const isEditable = editableExtensions.some(ext => file.name.endsWith(ext));
    if (isEditable) {
        return <FileText size={36} className="mr-3 text-(--primary-light)" />;
    }

    return <FileIcon size={36} className="mr-3 text-(--primary-light)" />;
  }
  
  const getTruncatedPath = (fullPath: string) => {
    if(!fullPath) return '';
    const parts = fullPath.split('/');
    if (parts.length <= 3) {
      return `/${fullPath}`;
    }
    return `/${parts[0]}/.../${parts[parts.length - 1]}`;
  }


  return (
    <div className="p-6 card">
      <style>{`
        @keyframes slide-in-and-fade-out {
            0% { transform: translate(-50%, 100%); opacity: 0; }
            10%, 90% { transform: translate(-50%, 0); opacity: 1; }
            100% { transform: translate(-50%, 100%); opacity: 0; }
        }
        .animate-toast { animation: slide-in-and-fade-out 3s ease-in-out forwards; }
      `}</style>
      <div className="flex items-center gap-3 mb-4">
        <div className="flex-1 min-w-0">{renderBreadcrumbs()}</div>
        <label className="flex items-center gap-2 bg-(--base-03) border border-(--base-04) rounded-md px-3 w-56 h-[37px] shrink-0 cursor-text transition-[border-color,box-shadow] focus-within:border-(--accent) focus-within:shadow-[0_0_0_3px_rgba(112,72,200,0.15)]">
            <Search size={16} className="text-(--base-07) shrink-0" />
            <input
                type="text"
                placeholder="Search all files..."
                value={globalSearchTerm}
                onChange={(e) => setGlobalSearchTerm(e.target.value)}
                className="flex-1 min-w-0 bg-transparent outline-none text-sm text-(--base-09) placeholder:text-(--base-05)"
            />
        </label>
        <div className="flex gap-1.5 shrink-0">
          <button title="Upload" onClick={() => setShowUploadPopup(true)} className="btn btn-secondary p-2">
            <Upload size={20} />
          </button>
          <button title="New File/Folder" onClick={() => { setPopupMode('create'); setNewName(''); setPopupError(''); }} className="btn btn-secondary p-2">
            <Plus size={20} />
          </button>
        </div>
      </div>
      
      {isSearching && <p className="text-center text-xl text-(--warning)">Searching...</p>}
      {showLoading && !showUploadPopup && <p className="text-center text-xl text-(--warning)">Loading...</p>}
      {error && <p className="text-(--error) text-center text-xl mb-4">{error}</p>}
      <ul className="space-y-2">
        {currentPath && !globalSearchTerm && (
          <li onClick={handleGoUp} className="server-row justify-between">
            <span className="flex flex-1 items-center min-w-0 truncate pr-4 text-(--base-09)">
              <CornerDownLeft size={36} className="mr-3 text-(--primary-light)" />
               <span className="text-lg">..</span>
            </span>
          </li>
        )}
        {sortedFiles.map((file) => (
          <li key={file.path || file.name} onClick={() => handleFileClick(file.name, file.is_dir, file.path)} className="server-row justify-between">
            <span className="flex items-center flex-1 min-w-0 truncate pr-4 text-(--base-09)">
              {renderFileRepresentation(file)}
              <div>
                  <span className="text-lg">{file.name}</span>
                  {/* CHANGED: Fixed typo from getTruncATEDPath to getTruncatedPath */}
                  {file.path && file.path !== file.name && <span className="text-xs text-(--base-07) block">{getTruncatedPath(file.path)}</span>}
              </div>
            </span>
            <div className="flex items-center space-x-1 text-sm shrink-0">
              <span className="text-(--base-09) hidden sm:inline mr-2">{formatBytes(file.size)}</span>
              {file.path && file.path !== file.name && (
                <button title="Go to folder" onClick={(e) => { e.stopPropagation(); handleFileClick(file.name, true, file.path) }} className="p-2 flex items-center justify-center text-(--primary-light) rounded-md transition-colors hover:bg-(--accent) hover:text-white">
                    <ExternalLink size={18} />
                </button>
              )}
              <button title="Rename" onClick={(e) => { e.stopPropagation(); setActionTarget(file); setNewName(file.name); setPopupMode('rename'); }} className="p-2 flex items-center justify-center text-(--primary-light) rounded-md transition-colors hover:bg-(--accent) hover:text-white">
                  <FilePen size={18} />
              </button>
              <button title="Copy" onClick={(e) => { e.stopPropagation(); setActionTarget(file); setNewName(getCopyName(file.name, file.is_dir, files)); setPopupMode('copy'); setPopupError(''); }} className="p-2 flex items-center justify-center text-(--primary-light) rounded-md transition-colors hover:bg-(--accent) hover:text-white">
                <Copy size={18} />
              </button>
              <button title="Download" onClick={async (e) => {
                e.stopPropagation();
                if (downloadLimit > 0 && file.size > downloadLimit) {
                  const limitStr = downloadLimit >= 1024 * 1024 * 1024 ? `${(downloadLimit / (1024 * 1024 * 1024)).toFixed(1)} GB` : `${Math.round(downloadLimit / (1024 * 1024))} MB`;
                  setToastMessage({ message: `File size exceeds download limit of ${limitStr}.`, type: 'error' }); return;
                }
                if (file.is_dir) {
                  openSelectiveDownload(file);
                } else {
                  const filePath = `${currentPath ? `${currentPath}/` : ''}${file.name}`;
                  setDownloadProgress({ filename: file.name, loaded: 0, total: file.size });
                  try {
                    await adapter.downloadFile(filePath, serverUuid, false, (loaded, total) =>
                      setDownloadProgress(prev => prev ? { ...prev, loaded, total: total || prev.total } : null)
                    );
                  } catch (err) {
                    console.error('Download failed:', err);
                    setToastMessage({ message: 'Download failed.', type: 'error' });
                  }
                  setDownloadProgress(null);
                }
              }} className="p-2 flex items-center justify-center text-(--primary-light) rounded-md transition-colors hover:bg-(--accent) hover:text-white">
                <Download size={18} />
              </button>
              <button title="Delete" onClick={(e) => handleDeleteClick(e, file)} className="p-2 flex items-center justify-center text-(--error) rounded-md transition-colors hover:bg-(--error) hover:text-white">
                <Trash2 size={18} />
              </button>
            </div>
          </li>
        ))}
      </ul>

      {popupMode && (
        <div className="modal-overlay animate-fade-in">
          <div className="modal-panel w-full max-w-sm">
            <div className="modal-header">
              <h2 className="modal-title">{getPopupTitle()}</h2>
            </div>
            <div className="modal-body">
              {popupError && <p className="text-(--error-light) text-sm mb-3">{popupError}</p>}
              <form onSubmit={handlePopupSubmit}>
                <div className="flex flex-col gap-[5px] mb-4">
                  <label className="input-label">Name</label>
                  <input type="text" value={newName} onChange={handleNameChange} className="input-field w-full" />
                </div>
                {popupMode === 'create' && (
                  <div className="flex flex-col gap-[5px] mb-4">
                    <label className="input-label">Type</label>
                    <select value={newFileType} onChange={e => setNewFileType(e.target.value as 'file' | 'folder')} className="input-field w-full">
                      <option value="file">File</option>
                      <option value="folder">Folder</option>
                    </select>
                  </div>
                )}
                <div className="flex justify-end gap-2 mt-4">
                  <button type="button" onClick={closePopup} className="btn btn-secondary px-4 py-2 text-sm">Cancel</button>
                  <button type="submit" className="btn btn-primary px-4 py-2 text-sm">Confirm</button>
                </div>
              </form>
            </div>
          </div>
        </div>
      )}

      {showDeleteConfirm && (
        <div className="modal-overlay animate-fade-in">
          <div className="modal-panel w-full max-w-md">
            <div className="modal-header">
              <h2 className="modal-title text-(--error-light)">Confirm Deletion</h2>
            </div>
            <div className="modal-body">
              <p className="text-sm text-(--base-08)">Are you sure you want to delete <span className="font-medium text-(--warning-light)">{fileToDelete?.name}</span>? This action cannot be undone.</p>
            </div>
            <div className="modal-footer">
              <button onClick={() => setShowDeleteConfirm(false)} className="btn btn-secondary px-5 py-2 text-sm">Cancel</button>
              <button onClick={handleConfirmDelete} className="btn btn-danger px-5 py-2 text-sm">Delete</button>
            </div>
          </div>
        </div>
      )}

      {showUploadPopup && (
        <div className="modal-overlay animate-fade-in">
          <div className="modal-panel w-full max-w-2xl">
            <div className="modal-header">
              <h2 className="modal-title">Upload</h2>
            </div>
            <div className="modal-body">
            
            {uploadPopupView === 'select' && (
              <>
                <p className="text-sm mb-4 text-(--base-08)">Upload to: <span className="font-mono text-(--accent-light)">/{currentPath || 'servers'}</span></p>
                <div 
                  onDragOver={handleDragOver} 
                  onDragLeave={handleDragLeave} 
                  onDrop={handleDrop} 
                  className={`p-10 border-2 border-dashed rounded-xl transition-all duration-300 bg-(--base-03) ${isDragging ? 'border-(--accent)' : 'border-(--base-05)'} ${isWindowDragging ? 'scale-105 border-(--accent)' : ''}`}
                >
                    <input type="file" multiple ref={fileInputRef} onChange={handleFileSelect} className="hidden" />
                    {/* @ts-ignore */}
                    <input type="file" ref={folderInputRef} onChange={handleFileSelect} className="hidden" multiple webkitdirectory="" directory="" />
                    
                    <div className="text-center">
                        <p className="text-sm text-(--base-07) mb-4">Drag & Drop files or a folder here</p>
                        <div className="flex justify-center gap-3">
                            <button onClick={() => fileInputRef.current?.click()} className="btn btn-secondary px-5 py-2 text-sm">Select Files</button>
                            <button onClick={() => folderInputRef.current?.click()} className="btn btn-secondary px-5 py-2 text-sm">Select Folder</button>
                        </div>
                    </div>
                </div>

                {filesToUpload && filesToUpload.length > 0 && (
                  <div className="mt-4">
                    <h3 className="input-label">Selected:</h3>
                     <div className="max-h-32 overflow-y-auto bg-(--base-03)/50 p-2 rounded-md">
                        {filesToUpload[0].webkitGetAsEntry?.().isDirectory || (filesToUpload[0].webkitRelativePath && filesToUpload[0].webkitRelativePath.includes('/')) ?
                            <span>Folder: {filesToUpload[0].webkitRelativePath ? filesToUpload[0].webkitRelativePath.split('/')[0] : filesToUpload[0].name} ({filesToUpload.length} items)</span> :
                             <ul className="list-disc list-inside">{
                                Array.from(filesToUpload).map((item: any, index) => {
                                    const fileName = item.kind === 'file' ? item.getAsFile()?.name : item.name;
                                    return <li key={index} className="truncate">{fileName}</li>
                                })
                            }</ul>
                        }
                    </div>
                  </div>
                )}
                
                {popupError && <p className="text-(--error-light) text-sm mt-3">{popupError}</p>}

                <div className="flex justify-end gap-2 mt-6">
                  <button onClick={closeUploadPopup} className="btn btn-secondary px-5 py-2 text-sm">Cancel</button>
                  <button onClick={startUploadProcess} disabled={!filesToUpload || filesToUpload.length === 0} className="btn btn-primary px-5 py-2 text-sm">
                    Upload
                  </button>
                </div>
              </>
            )}
            
            {uploadPopupView === 'progress' && (
              <div className="mt-4">
                 <p className="text-center text-sm text-(--base-08) mb-2">{uploadStatus}</p>
                 <div className="w-full bg-(--base-04) rounded-full h-2.5 overflow-hidden">
                    <div className="bg-(--accent) h-full rounded-full transition-all duration-300" style={{ width: `${uploadProgress || 0}%` }} />
                </div>
                <p className="text-center text-xs text-(--base-06) mt-1">{uploadProgress || 0}%</p>
              </div>
            )}

            {uploadPopupView === 'conflict' && conflictFile && uploadItems && (
                 <div>
                    <p className="text-sm text-(--base-08) text-center mb-4">The file or folder <span className="font-medium text-(--warning-light)">{conflictFile.file.name}</span> already exists.</p>

                    <div className="mb-5 space-y-2">
                        <label className="input-label block">1. Rename (Recommended)</label>
                        <div className="flex items-center gap-2">
                            <input type="text" value={conflictFile.newName} onChange={(e) => setConflictFile({...conflictFile, newName: e.target.value.replace(/[^a-zA-Z0-9._-]/g, '')})} className="input-field w-full" />
                            <button onClick={() => {
                                executeUpload(conflictFile.newName, uploadItems.items, uploadItems.isFolder, 'replace');
                            }} className="btn btn-primary px-4 py-2 text-sm whitespace-nowrap">Upload with New Name</button>
                        </div>
                    </div>

                    <div className="mb-5 space-y-2">
                      {conflictFile.isFolderConflict ? (
                        <>
                          <label className="input-label block">2. Merge Options</label>
                          <div className="flex gap-2">
                              <button onClick={() => executeUpload(conflictFile.file.name, uploadItems.items, true, 'merge', 'replace')} className="btn btn-secondary w-full py-2 text-sm">Merge & Overwrite Files</button>
                              <button onClick={() => executeUpload(conflictFile.file.name, uploadItems.items, true, 'merge', 'ignore')} className="btn btn-secondary w-full py-2 text-sm">Merge & Keep Files</button>
                          </div>
                        </>
                      ) : (
                         <label className="input-label block">2. Overwrite</label>
                      )}
                    </div>

                    <div className="flex justify-between items-center mt-6 pt-4 border-t border-(--base-03)">
                       <button onClick={() => executeUpload(conflictFile.file.name, uploadItems.items, uploadItems.isFolder, 'replace')} className="btn btn-danger px-5 py-2 text-sm">
                         {conflictFile.isFolderConflict ? 'Replace Entire Folder' : 'Overwrite File'}
                       </button>
                       <button onClick={() => {setUploadPopupView('select'); setConflictFile(null);}} className="btn btn-secondary px-5 py-2 text-sm">Cancel</button>
                    </div>
                </div>
            )}
            
            </div>
          </div>
        </div>
      )}

      {showEditPopup && (
        <div className="modal-overlay animate-fade-in">
          <div style={editorPopupStyle} className="modal-panel relative h-[85vh] flex flex-col p-0 overflow-hidden">
            {/* Header */}
            <div className="modal-header flex items-center justify-between shrink-0">
              <div className="flex items-center gap-2 flex-1 min-w-0">
                 {isRenaming ? (
                   <>
                    <input
                        ref={renameInputRef}
                        type="text"
                        value={tempFileName}
                        onChange={(e) => setTempFileName(e.target.value)}
                        onKeyDown={(e) => e.key === 'Enter' && handleRenameSubmit()}
                        className="input-field text-base"
                    />
                    <button onClick={handleRenameSubmit} className="p-2 flex items-center justify-center rounded-md text-(--success) hover:bg-(--base-04)"><Check size={20} /></button>
                    <button onClick={() => setIsRenaming(false)} className="p-2 flex items-center justify-center rounded-md text-(--error) hover:bg-(--base-04)"><X size={20} /></button>
                   </>
                 ) : (
                    <h2 className="modal-title truncate">{editingFile}</h2>
                 )}
                {!isRenaming && <button onClick={handleRenameClick} className="p-1 rounded-md hover:bg-(--base-04) text-(--base-06)"><Pencil size={18} /></button>}
              </div>
              <div className="flex items-center gap-1.5 shrink-0">
                   <div className="flex items-center gap-1 bg-(--base-03) p-1 rounded-md">
                       <input
                          type="text"
                          placeholder="Search..."
                          value={searchTerm}
                          onChange={handleSearchChange}
                          className="input-field h-7 px-2 text-xs"
                       />
                       <button onClick={goToPrevMatch} disabled={searchMatches.length === 0} className="w-7 h-7 flex items-center justify-center rounded-md hover:bg-(--base-04) disabled:opacity-40 text-(--base-07)"><ArrowUp size={16} /></button>
                       <button onClick={goToNextMatch} disabled={searchMatches.length === 0} className="w-7 h-7 flex items-center justify-center rounded-md hover:bg-(--base-04) disabled:opacity-40 text-(--base-07)"><ArrowDown size={16} /></button>
                       <span className="text-xs text-(--base-06) px-1.5 font-mono">{searchMatches.length > 0 ? `${currentMatchIndex + 1}/${searchMatches.length}` : '0/0'}</span>
                   </div>
                   <button onClick={closeEditPopup} className="w-8 h-8 flex items-center justify-center bg-(--base-03) hover:bg-(--base-04) transition-colors rounded-md text-(--base-07)">
                     <X size={18} />
                   </button>
              </div>
            </div>
            {isEditorLoading ? (
              <div className="grow flex items-center justify-center text-sm text-(--base-07)">
                Loading content...
              </div>
            ) : (
                <div className="relative grow mx-4 mb-2 rounded-md bg-(--base-01) border border-(--base-03) focus-within:border-(--accent) overflow-hidden">
                    <Suspense fallback={<div className="h-full flex items-center justify-center text-sm text-(--base-07)">Loading editor...</div>}>
                        <CodeMirrorEditor
                            value={fileContent}
                            onChange={setFileContent}
                            filename={editingFile || ''}
                            readOnly={!isEditingEnabled}
                            searchTerm={searchTerm}
                            className="h-full"
                        />
                    </Suspense>
                </div>
            )}
            <div className="modal-footer">
              {!isEditingEnabled ? (
                 <button onClick={() => setIsEditingEnabled(true)} className="btn btn-secondary px-4 py-2 text-sm">Edit</button>
              ) : (
                <>
                  <button onClick={() => {
                      setIsEditingEnabled(false);
                      setFileContent(originalFileContent);
                  }} className="btn btn-secondary px-4 py-2 text-sm">Cancel</button>
                  <button onClick={handleSaveFile} className="btn btn-primary px-4 py-2 text-sm">Save</button>
                </>
              )}
            </div>
          </div>
        </div>
      )}
      
      {/* Selective Download Popup */}
      {selectiveDownloadTarget && (
        <div className="modal-overlay animate-fade-in">
          <div className="modal-panel w-full max-w-lg">
            <div className="modal-header">
              <h2 className="modal-title">Download: {selectiveDownloadTarget.name}/</h2>
              <button onClick={() => setSelectiveDownloadTarget(null)} className="text-(--base-06) hover:text-(--base-09)"><X size={18} /></button>
            </div>
            <div className="modal-body">
              <div className="flex items-center gap-2 mb-3 pb-2 border-b border-(--base-03)">
                <input
                  type="checkbox"
                  checked={selectiveAll}
                  onChange={() => {
                    setSelectiveAll(!selectiveAll);
                    if (!selectiveAll) setSelectiveChecked(new Set());
                  }}
                  className="accent-(--accent) w-3.5 h-3.5"
                />
                <span className="text-sm font-medium text-(--base-09)">Select All</span>
              </div>
              <div className="max-h-80 overflow-y-auto">
                {selectiveLoading ? (
                  <div className="flex items-center justify-center py-8 text-(--base-06)">Loading...</div>
                ) : (
                  renderSelectiveTree('', 0)
                )}
              </div>
            </div>
            <div className="flex justify-end gap-2 p-4 border-t border-(--base-03)">
              <button onClick={() => setSelectiveDownloadTarget(null)} className="btn btn-secondary px-4 py-2 text-sm">Cancel</button>
              <button
                onClick={handleSelectiveDownload}
                disabled={selectiveDownloading || (!selectiveAll && selectiveChecked.size === 0)}
                className="btn btn-primary px-4 py-2 text-sm disabled:opacity-50"
              >
                <Download size={14} />
                {selectiveDownloading ? 'Downloading...' : 'Download .zip'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Download Progress */}
      {downloadProgress && (
        <div className="fixed bottom-4 right-4 z-50 w-68 bg-(--base-01) border border-(--base-04) rounded-lg shadow-xl p-3 animate-fade-in">
          <div className="flex items-center gap-2 mb-2">
            <Download size={13} className="text-(--accent) shrink-0" />
            <span className="text-xs text-(--base-08) truncate font-mono flex-1">{downloadProgress.filename}</span>
            <span className="text-[10px] text-(--base-05) font-mono shrink-0">
              {downloadProgress.total > 0
                ? `${Math.round((downloadProgress.loaded / downloadProgress.total) * 100)}%`
                : formatBytes(downloadProgress.loaded)}
            </span>
          </div>
          <div className="w-full h-1 bg-(--base-03) rounded-full overflow-hidden">
            {downloadProgress.total > 0 ? (
              <div
                className="h-full bg-(--accent) rounded-full transition-all duration-100"
                style={{ width: `${Math.min(100, (downloadProgress.loaded / downloadProgress.total) * 100)}%` }}
              />
            ) : (
              <div className="h-full bg-(--accent) rounded-full w-1/3 animate-pulse" />
            )}
          </div>
          {downloadProgress.total > 0 && (
            <div className="flex justify-between text-[10px] text-(--base-05) font-mono mt-1.5">
              <span>{formatBytes(downloadProgress.loaded)}</span>
              <span>{formatBytes(downloadProgress.total)}</span>
            </div>
          )}
        </div>
      )}

      {toastMessage && (
        <div className="toast-container" style={{ left: '50%', right: 'auto', transform: 'translateX(-50%)' }}>
          <div className="toast animate-toast">
            <div className={`toast-bar ${toastMessage.type === 'error' ? 'bg-(--error)' : 'bg-(--accent)'}`}></div>
            <span className="text-sm text-(--base-09)">{toastMessage.message}</span>
          </div>
        </div>
      )}
    </div>
  );
};

export default FileBrowser;
