"use client";

import React, { useMemo } from 'react';
import { FileBrowser } from '@dylaris/ui-filebrowser';
import { createPanelAdapter } from '@/lib/adapters/panelFileBrowserAdapter';

interface FileBrowserViewProps {
  currentServerPath: string;
  serverUuid?: string;
}

const FileBrowserView: React.FC<FileBrowserViewProps> = ({ currentServerPath, serverUuid }) => {
  const adapter = useMemo(() => createPanelAdapter(), []);

  return (
    <FileBrowser
      currentServerPath={currentServerPath}
      serverUuid={serverUuid}
      adapter={adapter}
    />
  );
};

export default FileBrowserView;
