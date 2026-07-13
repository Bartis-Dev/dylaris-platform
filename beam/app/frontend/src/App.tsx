// Beam Desktop settings page: the one piece of UI the app ships itself.
// Reachable at /__beam/ (linked from the Panel-unreachable error page). Everything else
// the window shows is the remote Dylaris Panel, reverse-proxied through
// the Wails asset server - see proxy.go. This page lets the user point
// the app at a different Panel (dev / staging / self-hosted), is the
// fallback shown when the configured Panel can't be reached, and hosts the
// Phase 3 self-update flow (mandatory + optional).

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
  SavePanelURL?: (token: string, url: string) => Promise<void>;
  GetUpdateInfo?: () => Promise<UpdateInfo>;
  GetUpdateGate?: () => Promise<UpdateGate>;
  OpenUpdateDownload?: (token: string) => void;
  ApplyUpdate?: (token: string) => Promise<void>;
};

declare global {
  interface Window {
    go?: { main?: { App?: WailsBindings } };
    // Per-run shell capability token, spliced into this page's HTML by the Go
    // proxy (serveBeamIndex). Required as the first arg of the side-effecting
    // bound methods; the proxied Panel never receives it.
    __beamShellToken?: string;
    // Wails injects window.runtime at load; EventsOn returns an unsubscribe fn.
    runtime?: {
      EventsOn?: (eventName: string, callback: (data: any) => void) => (() => void);
    };
  }
}

function getBindings(): WailsBindings | undefined {
  return window.go?.main?.App;
}

// The Beam shell publishes a per-run capability token on window (spliced into
// this page's HTML by the Go proxy). It is read once and passed as the first
// arg of the side-effecting bound methods; a proxied Panel never holds it.
const shellToken = window.__beamShellToken ?? '';

// Human-readable label per self-update phase (matches the Go update:status states).
const UPDATE_LABELS: Record<string, string> = {
  downloading: 'Downloading update...',
  verifying: 'Verifying update...',
  applying: 'Applying update...',
  relaunching: 'Restarting...',
};

export default function App() {
  const [defaultUrl, setDefaultUrl] = useState('https://panel.dylaris.com');
  const [inputUrl, setInputUrl] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [savingError, setSavingError] = useState<string | null>(null);
  const [update, setUpdate] = useState<UpdateInfo | null>(null);
  const [gate, setGate] = useState<UpdateGate | null>(null);

  // Self-update flow state (Phase 3). updateState is one of the update:status
  // states while a self-apply runs, 'error' on failure, or null when idle.
  const [updateState, setUpdateState] = useState<string | null>(null);
  const [updateError, setUpdateError] = useState<string | null>(null);
  const [updateProgress, setUpdateProgress] = useState<{ loaded: number; total: number } | null>(null);

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

  // Subscribe to the Go self-update events. EventsOn returns an unsubscribe fn.
  useEffect(() => {
    const on = window.runtime?.EventsOn;
    if (!on) return;
    const offProgress = on('update:progress', (data: { loaded: number; total: number }) => {
      setUpdateProgress(data);
    });
    const offStatus = on('update:status', (data: { state: string; message: string }) => {
      setUpdateState(data.state);
      if (data.state === 'error') {
        setUpdateError(data.message || 'Update failed.');
      }
    });
    return () => {
      offProgress?.();
      offStatus?.();
    };
  }, []);

  // Kick off the self-apply flow. Both the mandatory screen and the optional nag
  // call this; the Go side re-verifies fail-closed before applying.
  const startUpdate = () => {
    setUpdateError(null);
    setUpdateProgress(null);
    setUpdateState('downloading');
    getBindings()?.ApplyUpdate?.(shellToken);
  };

  const updating = updateState !== null && updateState !== 'error';
  const pct =
    updateProgress && updateProgress.total > 0
      ? Math.min(100, Math.round((updateProgress.loaded / updateProgress.total) * 100))
      : null;

  // Shared progress/status + error-fallback block rendered inside both update surfaces.
  const updateFlow = (
    <>
      {updating && (
        <div style={{ marginTop: '0.6rem' }}>
          <div style={{ fontSize: '0.8em', opacity: 0.85, marginBottom: '0.4rem' }}>
            {UPDATE_LABELS[updateState as string] || 'Working...'}
          </div>
          <div style={{ height: '6px', background: '#1C1F24', borderRadius: '3px', overflow: 'hidden' }}>
            <div
              style={{
                height: '100%',
                width: pct !== null ? `${pct}%` : '100%',
                background: '#7048C8',
                transition: 'width 0.2s ease',
                opacity: pct !== null ? 1 : 0.5,
              }}
            />
          </div>
        </div>
      )}
      {updateError && (
        <>
          <div className="error" style={{ marginTop: '0.6rem' }}>{updateError}</div>
          <div className="settings-actions">
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => getBindings()?.OpenUpdateDownload?.(shellToken)}
            >
              Download in browser instead
            </button>
          </div>
        </>
      )}
    </>
  );

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
      await getBindings()?.SavePanelURL?.(shellToken, candidate);
    } catch (err) {
      setSavingError(err instanceof Error ? err.message : String(err));
      return;
    }
    window.location.href = '/';
  };

  // MANDATORY (blocking) tier: the running build is below the server's floor.
  // Full-screen, non-dismissible - no settings form, no cancel. The proxy
  // middleware keeps redirecting Panel requests here while blocked. The button
  // now runs the Phase 3 self-apply; the browser download stays as the fallback
  // shown on error.
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
          {!updating && (
            <div className="settings-actions">
              <button type="button" className="btn btn-primary" onClick={startUpdate}>
                Update now
              </button>
            </div>
          )}
          {updateFlow}
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
          style={{ marginBottom: '1rem', borderColor: 'var(--accent, #7048C8)' }}
        >
          <div className="settings-title">Update available</div>
          <div style={{ fontSize: '0.85em', opacity: 0.85, marginTop: '0.4rem' }}>
            A newer Beam ({update.latest}) is available. You have {update.current}.
          </div>
          {!updating && (
            <div className="settings-actions">
              <button type="button" className="btn btn-primary" onClick={startUpdate}>
                Update now
              </button>
            </div>
          )}
          {updateFlow}
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
