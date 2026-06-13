import React from 'react';
import { Folder, File as FileIcon, Download, X, ChevronRight, ChevronDown } from 'lucide-react';
import type { FileEntry } from './types';
import { formatBytes } from './utils';

export interface SelectiveDownloadModalProps {
  target: FileEntry;
  tree: Record<string, FileEntry[]>;
  expanded: Set<string>;
  checked: Set<string>;
  selectAll: boolean;
  loading: boolean;
  downloading: boolean;
  onClose: () => void;
  onToggleSelectAll: () => void;
  onToggleExpand: (relativePath: string) => void;
  onToggleCheck: (relativePath: string) => void;
  onDownload: () => void;
}

// Controlled tree-picker modal for downloading a subset of a folder as a zip.
// All state (tree contents, expansion, checks, select-all) is owned by the
// parent; this component renders it and emits callbacks.
const SelectiveDownloadModal: React.FC<SelectiveDownloadModalProps> = ({
  target,
  tree,
  expanded,
  checked,
  selectAll,
  loading,
  downloading,
  onClose,
  onToggleSelectAll,
  onToggleExpand,
  onToggleCheck,
  onDownload,
}) => {
  const renderTree = (parentPath: string, depth: number): React.ReactNode => {
    const entries = tree[parentPath];
    if (!entries) return null;

    return entries.map(entry => {
      const entryPath = parentPath ? `${parentPath}/${entry.name}` : entry.name;
      const isExpanded = expanded.has(entryPath);
      const isChecked = selectAll || checked.has(entryPath);

      return (
        <div key={entryPath}>
          <div className="flex items-center gap-1.5 py-1 px-2 hover:bg-(--base-02) rounded-sm" style={{ paddingLeft: `${depth * 20 + 8}px` }}>
            {entry.is_dir ? (
              <button onClick={() => onToggleExpand(entryPath)} className="p-0.5 text-(--base-06) hover:text-(--base-09)">
                {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
              </button>
            ) : (
              <span className="w-[18px]" />
            )}
            <input
              type="checkbox"
              checked={isChecked}
              onChange={() => onToggleCheck(entryPath)}
              className="accent-(--accent) w-3.5 h-3.5"
            />
            {entry.is_dir ? <Folder size={14} className="text-(--primary-light) shrink-0" /> : <FileIcon size={14} className="text-(--base-06) shrink-0" />}
            <span className="text-sm text-(--base-09) truncate">{entry.name}</span>
            <span className="text-xs text-(--base-06) ml-auto shrink-0">{formatBytes(entry.size)}</span>
          </div>
          {entry.is_dir && isExpanded && renderTree(entryPath, depth + 1)}
        </div>
      );
    });
  };

  return (
    <div className="modal-overlay animate-fade-in">
      <div className="modal-panel w-full max-w-lg">
        <div className="modal-header">
          <h2 className="modal-title">Download: {target.name}/</h2>
          <button onClick={onClose} className="text-(--base-06) hover:text-(--base-09)"><X size={18} /></button>
        </div>
        <div className="modal-body">
          <div className="flex items-center gap-2 mb-3 pb-2 border-b border-(--base-03)">
            <input
              type="checkbox"
              checked={selectAll}
              onChange={onToggleSelectAll}
              className="accent-(--accent) w-3.5 h-3.5"
            />
            <span className="text-sm font-medium text-(--base-09)">Select All</span>
          </div>
          <div className="max-h-80 overflow-y-auto">
            {loading ? (
              <div className="flex items-center justify-center py-8 text-(--base-06)">Loading...</div>
            ) : (
              renderTree('', 0)
            )}
          </div>
        </div>
        <div className="flex justify-end gap-2 p-4 border-t border-(--base-03)">
          <button onClick={onClose} className="btn btn-secondary px-4 py-2 text-sm">Cancel</button>
          <button
            onClick={onDownload}
            disabled={downloading || (!selectAll && checked.size === 0)}
            className="btn btn-primary px-4 py-2 text-sm disabled:opacity-50"
          >
            <Download size={14} />
            {downloading ? 'Downloading...' : 'Download .zip'}
          </button>
        </div>
      </div>
    </div>
  );
};

export default SelectiveDownloadModal;
