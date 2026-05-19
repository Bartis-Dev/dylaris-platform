"use client";

import React, { useEffect, useMemo } from 'react';
import { FileBrowser } from '@dylaris/ui-filebrowser';
import { createBeamAdapter, isWails, syncSessionWithWails, connectWailsToServer } from '@/lib/adapters';

interface FileBrowserViewProps {
  currentServerPath: string;
  serverUuid?: string;
}

const FileBrowserView: React.FC<FileBrowserViewProps> = ({ currentServerPath, serverUuid }) => {
  // The adapter is fixed per environment — browser stays on HTTP, Wails
  // stays on gRPC. We don't memoize on isWails() because window.go is
  // available before render in Wails (no race).
  const adapter = useMemo(() => createBeamAdapter(), []);

  // Inside Beam Desktop, point the native relay tunnel at whatever server
  // the user is browsing. Re-runs whenever the URL changes server.
  useEffect(() => {
    if (!isWails()) return;
    syncSessionWithWails();
    if (serverUuid) connectWailsToServer(serverUuid);
  }, [serverUuid]);

  return (
    <FileBrowser
      currentServerPath={currentServerPath}
      serverUuid={serverUuid}
      adapter={adapter}
    />
  );
};

export default FileBrowserView;
