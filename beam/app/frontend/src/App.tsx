// Beam Desktop settings page: the one piece of UI the app ships itself.
// Reachable at /__beam/ (linked from the Panel-unreachable error page). Everything else
// the window shows is the remote Dylaris Panel, reverse-proxied through
// the Wails asset server — see proxy.go. This page lets the user point
// the app at a different Panel (dev / staging / self-hosted) and is the
// fallback shown when the configured Panel can't be reached.

import { useEffect, useState } from 'react';

type UpdateInfo = {
  current: string;
  latest: string;
  updateAvailable: boolean;
  downloadUrl: string;
};

type UpdateGate = {
  blocked: boolean;
  current: string;
  minVersion: string;
};

type WailsBindings = {
  GetPanelURL?: () => Promise<string>;
  GetDefaultPanelURL?: () => Promise<string>;
  SavePanelURL?: (url: string) => Promise<void>;
  GetUpdateInfo?: () => Promise<UpdateInfo>;
  GetUpdateGate?: () => Promise<UpdateGate>;
  OpenUpdateDownload?: () => void;
};

declare global {
  interface Window {
    go?: { main?: { App?: WailsBindings } };
  }
}

function getBindings(): WailsBindings | undefined {
  return window.go?.main?.App;
}

export default function App() {
  const [defaultUrl, setDefaultUrl] = useState('https://panel.dylaris.com');
  const [inputUrl, setInputUrl] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [savingError, setSavingError] = useState<string | null>(null);
  const [update, setUpdate] = useState<UpdateInfo | null>(null);
  const [gate, setGate] = useState<UpdateGate | null>(null);

  // Pull the current + default Panel URL on mount.
  useEffect(() => {
    const load = async () => {
      try {
        const bindings = getBindings();
        const fallback = (await bindings?.GetDefaultPanelURL?.()) || 'https://panel.dylaris.com';
        setDefaultUrl(fallback);
        const url = (await bindings?.GetPanelURL?.()) || fallback;
        setInputUrl(url);
        bindings?.GetUpdateInfo?.().then(u => setUpdate(u)).catch(() => {});
        bindings?.GetUpdateGate?.().then(g => setGate(g)).catch(() => {});
      } catch (err) {
        console.warn('Panel URL resolve failed:', err);
        setInputUrl('https://panel.dylaris.com');
      } finally {
        setLoaded(true);
      }
    };
    load();
  }, []);

  // Persist the URL, then hand the window back to the Panel. The proxy
  // re-reads the saved URL on the next request, so '/' now resolves to
  // the freshly chosen Panel.
  const handleSave = async () => {
    setSavingError(null);
    let candidate = inputUrl.trim();
    if (!candidate) {
      setSavingError('URL is required');
      return;
    }
    if (!/^https?:\/\//i.test(candidate)) {
      candidate = 'https://' + candidate;
    }
    try {
      await getBindings()?.SavePanelURL?.(candidate);
    } catch (err) {
      setSavingError(err instanceof Error ? err.message : String(err));
      return;
    }
    window.location.href = '/';
  };

  // MANDATORY (blocking) tier: the running build is below the server's floor.
  // Full-screen, non-dismissible - no settings form, no cancel. The proxy
  // middleware keeps redirecting Panel requests here while blocked. The button
  // reuses the Phase-1 OpenUpdateDownload (opens the GitHub asset in the
  // browser); self-apply is Phase 3.
  if (gate?.blocked) {
    return (
      <div className="loader">
        <div className="logo">
          <span className="brand-d">D</span>ylaris <span className="brand-beam">Beam</span>
        </div>
        <div className="settings-card" style={{ borderColor: 'var(--accent, #7048C8)' }}>
          <div className="settings-title">Update required</div>
          <div style={{ fontSize: '0.85em', opacity: 0.85, marginTop: '0.4rem' }}>
            This Beam version ({gate.current}) can no longer connect. The server requires
            at least {gate.minVersion}. Update to continue.
          </div>
          <div className="settings-actions">
            <button
              type="button"
              className="btn btn-primary"
              onClick={() => getBindings()?.OpenUpdateDownload?.()}
            >
              Download update
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="loader">
      <div className="logo">
        <span className="brand-d">D</span>ylaris <span className="brand-beam">Beam</span>
      </div>

      {update?.updateAvailable && (
        <div
          className="settings-card"
          style={{ marginBottom: '1rem', borderColor: 'var(--accent, #7048C8)', cursor: 'pointer' }}
          onClick={() => getBindings()?.OpenUpdateDownload?.()}
          title="Open the download in your browser"
        >
          <div className="settings-title">Update available</div>
          <div style={{ fontSize: '0.85em', opacity: 0.85, marginTop: '0.4rem' }}>
            A newer Beam ({update.latest}) is available. You have {update.current}. Click to download.
          </div>
        </div>
      )}

      <div className="settings-card">
        <div className="settings-title">Panel URL</div>
        <input
          type="text"
          className="url-input"
          value={inputUrl}
          onChange={e => setInputUrl(e.target.value)}
          placeholder="https://panel.dylaris.com"
          autoFocus
          disabled={!loaded}
          onKeyDown={e => {
            if (e.key === 'Enter') handleSave();
            if (e.key === 'Escape') window.location.href = '/';
          }}
        />
        {savingError && <div className="error">{savingError}</div>}

        <div className="settings-actions">
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => setInputUrl(defaultUrl)}
          >
            Use default
          </button>
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => { window.location.href = '/'; }}
          >
            Cancel
          </button>
          <button type="button" className="btn btn-primary" onClick={handleSave}>
            Save &amp; connect
          </button>
        </div>
      </div>
    </div>
  );
}
