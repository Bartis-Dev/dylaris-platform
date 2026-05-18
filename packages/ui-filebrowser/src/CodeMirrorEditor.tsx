"use client";

import { useEffect, useRef, useState, useMemo } from 'react';
import CodeMirror from '@uiw/react-codemirror';
import type { Extension } from '@codemirror/state';
import { json } from '@codemirror/lang-json';
import { yaml } from '@codemirror/lang-yaml';
import { javascript } from '@codemirror/lang-javascript';
import { xml } from '@codemirror/lang-xml';
import { EditorView, type ViewUpdate } from '@codemirror/view';
import { dylarisTheme, dylarisHighlight } from './codemirror-theme';
import { propertiesLanguage } from './codemirror-properties';

export type FileLanguage = 'json' | 'yaml' | 'properties' | 'javascript' | 'xml' | 'plain';

export function detectLanguage(filename: string): FileLanguage {
  const lower = filename.toLowerCase();
  const ext = lower.includes('.') ? lower.substring(lower.lastIndexOf('.')) : '';
  switch (ext) {
    case '.json':
    case '.json5':
      return 'json';
    case '.yml':
    case '.yaml':
      return 'yaml';
    case '.properties':
    case '.cfg':
    case '.conf':
    case '.config':
    case '.ini':
      return 'properties';
    case '.js':
    case '.mjs':
    case '.cjs':
    case '.ts':
      return 'javascript';
    case '.xml':
    case '.html':
    case '.htm':
      return 'xml';
    default:
      return 'plain';
  }
}

function getLanguageExtension(lang: FileLanguage): Extension[] {
  switch (lang) {
    case 'json':
      return [json()];
    case 'yaml':
      return [yaml()];
    case 'properties':
      return [propertiesLanguage()];
    case 'javascript':
      return [javascript({ typescript: false })];
    case 'xml':
      return [xml()];
    case 'plain':
    default:
      return [];
  }
}

interface CodeMirrorEditorProps {
  value: string;
  onChange: (next: string) => void;
  filename: string;
  readOnly?: boolean;
  searchTerm?: string;
  className?: string;
  onCursorChange?: (line: number, col: number) => void;
}

export default function CodeMirrorEditor({
  value,
  onChange,
  filename,
  readOnly,
  searchTerm,
  className,
  onCursorChange,
}: CodeMirrorEditorProps) {
  const language = useMemo(() => detectLanguage(filename), [filename]);
  const [extensions, setExtensions] = useState<Extension[]>([]);
  const viewRef = useRef<EditorView | null>(null);

  useEffect(() => {
    setExtensions([
      ...getLanguageExtension(language),
      dylarisTheme,
      dylarisHighlight,
      EditorView.lineWrapping,
      EditorView.updateListener.of((update: ViewUpdate) => {
        if (onCursorChange && update.selectionSet) {
          const head = update.state.selection.main.head;
          const line = update.state.doc.lineAt(head);
          onCursorChange(line.number, head - line.from + 1);
        }
      }),
    ]);
  }, [language, onCursorChange]);

  // Imperative search: jump to first match when searchTerm changes.
  useEffect(() => {
    const view = viewRef.current;
    if (!view || !searchTerm) return;
    const docText = view.state.doc.toString();
    const idx = docText.toLowerCase().indexOf(searchTerm.toLowerCase());
    if (idx < 0) return;
    view.dispatch({
      selection: { anchor: idx, head: idx + searchTerm.length },
      scrollIntoView: true,
    });
  }, [searchTerm]);

  return (
    <div className={className} style={{ height: '100%' }}>
      <CodeMirror
        value={value}
        onChange={onChange}
        extensions={extensions}
        readOnly={readOnly}
        theme="dark"
        basicSetup={{
          lineNumbers: true,
          foldGutter: true,
          highlightActiveLine: true,
          highlightActiveLineGutter: true,
          bracketMatching: true,
          closeBrackets: true,
          autocompletion: language === 'json' || language === 'yaml',
          searchKeymap: true,
        }}
        height="100%"
        onCreateEditor={(view: EditorView) => {
          viewRef.current = view;
        }}
      />
    </div>
  );
}
