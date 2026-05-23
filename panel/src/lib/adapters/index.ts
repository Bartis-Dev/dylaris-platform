import type { FileBrowserAdapter } from '@dylaris/ui-filebrowser';
import { createPanelAdapter } from './panelFileBrowserAdapter';
import { createWailsBeamAdapter, isWails } from './wailsBeamAdapter';

// createBeamAdapter is the single entry point for file-browsing UI.
// In the browser it returns the HTTP-backed Panel adapter; inside the
// Beam Desktop App it returns the gRPC-via-Wails adapter so transfers
// hit the BeamRelay directly instead of round-tripping through Core.
export function createBeamAdapter(): FileBrowserAdapter {
    return isWails() ? createWailsBeamAdapter() : createPanelAdapter();
}

export { isWails, getWailsApp, syncSessionWithWails, connectWailsToServer, resetWailsConnectionCache } from './wailsBeamAdapter';
