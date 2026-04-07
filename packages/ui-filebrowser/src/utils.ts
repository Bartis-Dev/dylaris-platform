import type { FileEntry } from './types';

export const formatBytes = (bytes: number, decimals = 2): string => {
  if (bytes === 0) return '0 Bytes';
  const k = 1024;
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  const dm = i < 3 ? 0 : decimals;
  return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
};

export const validFilenameRegex = /^[a-zA-Z0-9._-]+$/;

export const editableExtensions = ['.txt', '.yml', '.properties', '.config', '.log', '.json', '.yaml', '.cfg'];

export function getCopyName(name: string, isDir: boolean, existingFiles: FileEntry[]): string {
  const existingNames = new Set(existingFiles.map(f => f.name));
  const lastDot = isDir ? -1 : name.lastIndexOf('.');
  const base = lastDot > 0 ? name.substring(0, lastDot) : name;
  const ext = lastDot > 0 ? name.substring(lastDot + 1) : '';
  const makeName = (suffix: string) => ext ? `${base}${suffix}.${ext}` : `${base}${suffix}`;

  let candidate = makeName('_copy');
  if (!existingNames.has(candidate)) return candidate;

  for (let i = 1; ; i++) {
    candidate = makeName(`_copy-${i}`);
    if (!existingNames.has(candidate)) return candidate;
  }
}
