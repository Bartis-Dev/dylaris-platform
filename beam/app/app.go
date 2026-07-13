package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the main Wails application struct.
// All methods on this struct are exposed to the frontend via bindings.
type App struct {
	ctx context.Context

	// connMu guards the session/connection pointers below. Wails dispatches
	// each frontend binding call on its own goroutine, and the health-check
	// goroutine touches them too, so client/relayClient/relayAddr are read and
	// written concurrently. The accessors keep critical sections to a single
	// field read/write and never hold the lock across network I/O. panelURL is
	// set once at startup and never mutated here, so it stays unguarded.
	connMu      sync.Mutex
	client      *CoreClient     // REST API client (login, config, tickets)
	relayClient *BeamNodeClient // gRPC client via BeamRelay (file ops)
	relayAddr   string          // cached relay address from GetBeamConfig
	panelURL    string          // URL the webview navigates to on startup (Panel)

	// Active chunked uploads, keyed by an opaque JS-supplied uploadID.
	// Each value is the open gRPC stream session — the FileBrowser
	// uploads one file at a time so the map normally has 0 or 1 entries,
	// but we don't enforce that (canceling a stuck upload is racier
	// without a unique ID per attempt).
	uploadsMu sync.Mutex
	uploads   map[string]*UploadSession

	// healthCheckStop is closed when the current health-check goroutine
	// should exit (Logout, SetSession on a new account). nil while no
	// check is running. The goroutine is owned exclusively by App and
	// kept lightweight — one Ping every 30s, skipped while uploads are
	// in flight so we never race the resume-retry loop on the JS side.
	healthCheckMu   sync.Mutex
	healthCheckStop chan struct{}

	// downloadMu guards lastDownloadPath, the destination the user picked
	// via the native OS save dialog in the most recent successful
	// DownloadFile/SelectiveDownload call. It is the only path
	// RevealInExplorer will ever open (see comment there).
	downloadMu       sync.Mutex
	lastDownloadPath string

	// gateMu guards the force-update gate. gateBlocked is set once the app knows
	// its build is below Core's advertised min_version (proactively after auth,
	// or reactively from a GetBeamTicket 426). While set, the app-shell shows the
	// mandatory-update screen and the proxy middleware pins the webview to it.
	gateMu      sync.Mutex
	gateBlocked bool
	gateMin     string
}

// userSettings is the schema of the JSON config file we persist between
// runs. Lives at $UserConfigDir/dylaris-beam/config.json — the user's
// Panel URL choice survives reinstalls of the same major version.
type userSettings struct {
	PanelURL string `json:"panelUrl"`
}

// settingsPath returns the OS-appropriate config file location:
//   Windows: %AppData%\dylaris-beam\config.json
//   macOS:   ~/Library/Application Support/dylaris-beam/config.json
//   Linux:   ~/.config/dylaris-beam/config.json
// Errors fall back to a path next to the executable so the app stays
// functional even on locked-down systems.
func settingsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		exe, _ := os.Executable()
		dir = filepath.Dir(exe)
	}
	return filepath.Join(dir, "dylaris-beam", "config.json")
}

func loadSettings() userSettings {
	var s userSettings
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, &s)
	s.PanelURL = strings.TrimSpace(s.PanelURL)
	return s
}

func saveSettings(s userSettings) error {
	path := settingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// GetPanelURL is exposed to the frontend stub so the embedded redirector
// knows where to point its window.location. Resolution priority:
//   1. Saved user setting (Settings dialog wrote it)
//   2. Build-time env var (DYLARIS_PANEL_URL set on the App struct)
//   3. Hard-coded default
func (a *App) GetPanelURL() string {
	if saved := strings.TrimSpace(loadSettings().PanelURL); saved != "" {
		return saved
	}
	if a.panelURL != "" {
		return a.panelURL
	}
	return "https://panel.dylaris.com"
}

// GetDefaultPanelURL returns the URL the redirector would use absent a
// saved override. The Settings dialog uses it to pre-fill the input
// and to offer a "Reset" button.
func (a *App) GetDefaultPanelURL() string {
	if a.panelURL != "" {
		return a.panelURL
	}
	return "https://panel.dylaris.com"
}

// SavePanelURL persists a new Panel URL chosen via the Settings dialog.
// Pass an empty string to clear the override (falls back to the
// build-time default on the next launch).
func (a *App) SavePanelURL(url string) error {
	url = strings.TrimSpace(url)
	// Trim a trailing slash so the redirector's later concatenation
	// (e.g. "/login") never produces a double-slash.
	url = strings.TrimRight(url, "/")
	return saveSettings(userSettings{PanelURL: url})
}

func NewApp() *App {
	return &App{
		uploads: make(map[string]*UploadSession),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// UpdateGate is the force-update decision the app-shell frontend reads to
// render the mandatory "update required" screen. Blocked is true when the
// running build is below Core's advertised min_version.
type UpdateGate struct {
	Blocked    bool   `json:"blocked"`
	Current    string `json:"current"`
	MinVersion string `json:"minVersion"`
}

// GetUpdateGate reports the force-update decision. Bound to the frontend; the
// mandatory screen renders when Blocked is true. The download button reuses the
// Phase-1 OpenUpdateDownload, so no download URL is carried here.
func (a *App) GetUpdateGate() *UpdateGate {
	a.gateMu.Lock()
	defer a.gateMu.Unlock()
	return &UpdateGate{Blocked: a.gateBlocked, Current: AppVersion, MinVersion: a.gateMin}
}

func (a *App) gateIsBlocked() bool {
	a.gateMu.Lock()
	defer a.gateMu.Unlock()
	return a.gateBlocked
}

// triggerMandatoryUpdate flips the gate and pulls the webview off the Panel
// onto the app-shell mandatory screen (/__beam/, where App.tsx renders it when
// GetUpdateGate reports blocked). The proxy middleware also redirects Panel
// requests while blocked, so a manual reload can't escape. Safe from any
// goroutine.
func (a *App) triggerMandatoryUpdate(minVer string) {
	a.gateMu.Lock()
	a.gateBlocked = true
	a.gateMin = minVer
	a.gateMu.Unlock()
	if a.ctx != nil {
		runtime.WindowExecJS(a.ctx, "window.location.href='/__beam/'")
	}
}

// evaluateUpdateGate is the PROACTIVE startup gate: once authenticated it reads
// Core's advertised min_version and blocks if this build is below it. Called
// (backgrounded) from SetSession. Best-effort - a fetch error leaves the gate
// unchanged; the reactive 426 in ConnectToServer is the backstop.
func (a *App) evaluateUpdateGate() {
	client := a.getClient()
	if client == nil {
		return
	}
	config, err := client.GetBeamConfig()
	if err != nil || config == nil {
		return
	}
	if belowMinVersion(AppVersion, config.MinVersion) {
		a.triggerMandatoryUpdate(config.MinVersion)
	}
}

// ─── Connection-state accessors (connMu-guarded) ─────────────────────
// Leaf helpers: each takes connMu only for the single field access and
// never calls another method, so callers can compose them freely without
// risking a deadlock. Close() on a returned relay client is always done by
// the caller OUTSIDE the lock, since teardown can block on network I/O.

func (a *App) getClient() *CoreClient {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	return a.client
}

func (a *App) getRelay() *BeamNodeClient {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	return a.relayClient
}

func (a *App) getRelayAddr() string {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	return a.relayAddr
}

func (a *App) setClient(c *CoreClient) {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	a.client = c
}

func (a *App) setRelayAddr(s string) {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	a.relayAddr = s
}

// setRelay swaps in a new relay client and returns the previous one for the
// caller to Close outside the lock.
func (a *App) setRelay(rc *BeamNodeClient) *BeamNodeClient {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	old := a.relayClient
	a.relayClient = rc
	return old
}

// newClientResetRelay installs a fresh REST client and drops the cached relay
// (a new session invalidates the old tunnel). Returns the old relay to Close.
func (a *App) newClientResetRelay(c *CoreClient) *BeamNodeClient {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	old := a.relayClient
	a.client = c
	a.relayClient = nil
	return old
}

// resetSession clears all session + connection state and returns the relay
// client to Close outside the lock.
func (a *App) resetSession() *BeamNodeClient {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	old := a.relayClient
	a.client = nil
	a.relayClient = nil
	a.relayAddr = ""
	return old
}

// ─── Auth ────────────────────────────────────────────────────────────

// Login authenticates with the Core REST API and returns the session info.
func (a *App) Login(apiURL, username, password string) (*LoginResult, error) {
	client, err := NewCoreClient(apiURL)
	if err != nil {
		return nil, err
	}

	result, err := client.Login(username, password)
	if err != nil {
		return nil, err
	}

	a.setClient(client)
	return result, nil
}

// Logout clears the session.
func (a *App) Logout() {
	a.stopHealthCheck()
	if old := a.resetSession(); old != nil {
		old.Close()
	}
}

// SetSession is the panel-driven counterpart to Login: when the Beam
// Desktop loads the Panel and the user authenticates against Core, the
// Panel JS pushes the resulting JWT into the Wails side so subsequent
// file ops can use the same identity. Idempotent — safe to call on
// every page load.
func (a *App) SetSession(apiURL, token string) error {
	if apiURL == "" || token == "" {
		return fmt.Errorf("apiURL and token are required")
	}
	// The token is a live JWT credential. Only accept an apiURL on the
	// configured Panel host so a compromised Panel page can't push the token
	// to an attacker-controlled URL.
	if u, err := url.Parse(apiURL); err != nil || u.Host != a.resolvePanelTarget().Host {
		return fmt.Errorf("apiURL host does not match the configured Panel")
	}
	client, err := NewCoreClient(apiURL)
	if err != nil {
		return err
	}
	client.token = token
	// New session invalidates any cached relay connection — the old
	// tunnel was tied to the previous user's tickets, and the
	// health-check goroutine was watching that tunnel.
	old := a.newClientResetRelay(client)
	a.stopHealthCheck()
	if old != nil {
		old.Close()
	}
	// Proactive force-update gate: now authenticated, check Core's advertised
	// minimum and block if this build is too old. Backgrounded so SetSession
	// returns to the Panel JS promptly (the check is a network round-trip).
	go a.evaluateUpdateGate()
	return nil
}

// stopHealthCheck signals the current health-check goroutine to exit
// (if one is running) and forgets it. Safe to call multiple times.
func (a *App) stopHealthCheck() {
	a.healthCheckMu.Lock()
	defer a.healthCheckMu.Unlock()
	if a.healthCheckStop != nil {
		close(a.healthCheckStop)
		a.healthCheckStop = nil
	}
}

// startHealthCheck (re)starts the background ping loop for the given
// serverUUID. Idempotent — replaces an existing checker (so a switch
// to a different server cleanly stops the old one) and is safe to call
// after every ConnectToServer.
func (a *App) startHealthCheck(serverUUID string) {
	a.healthCheckMu.Lock()
	if a.healthCheckStop != nil {
		close(a.healthCheckStop)
	}
	stop := make(chan struct{})
	a.healthCheckStop = stop
	a.healthCheckMu.Unlock()
	go a.runHealthCheck(serverUUID, stop)
}

// runHealthCheck pings the relay every 30s. On Ping failure the relay
// address cache is cleared and ConnectToServer is invoked again — that
// path re-fetches GetBeamConfig and gets a fresh (potentially different)
// relay from Core's random pick, so a dead relay rotates out without
// the user noticing. We deliberately skip the check while uploads are
// in flight so we never race the JS-side resume retry loop.
func (a *App) runHealthCheck(serverUUID string, stop chan struct{}) {
	const (
		interval    = 30 * time.Second
		pingTimeout = 5 * time.Second
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			// Don't ping while an upload is mid-flight — the JS side has
			// its own retry/resume path and tearing the tunnel out from
			// under it would just cause needless extra resume attempts.
			a.uploadsMu.Lock()
			inFlight := len(a.uploads)
			a.uploadsMu.Unlock()
			if inFlight > 0 {
				continue
			}
			relay := a.getRelay()
			if relay == nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
			err := relay.Ping(ctx)
			cancel()
			if err == nil {
				continue
			}
			// Ping failed — force a re-fetch of the relay address and
			// reconnect. If Core hands us back the same dead relay
			// (its Redis TTL hasn't expired yet) we'll just fail again
			// on the next tick — eventually the dead one ages out.
			a.setRelayAddr("")
			if cErr := a.ConnectToServer(serverUUID); cErr != nil {
				// Log to stderr; Wails apps don't have a UI for it but
				// it surfaces when launched from a console / dev mode.
				fmt.Fprintf(os.Stderr, "beam-app: health-check reconnect failed: %v (original ping err: %v)\n", cErr, err)
			}
		}
	}
}

// IsLoggedIn checks if there is an active session.
func (a *App) IsLoggedIn() bool {
	c := a.getClient()
	return c != nil && c.token != ""
}

// ─── Beam Config ─────────────────────────────────────────────────────

// GetBeamConfig returns the Beam relay address and branding from Core.
// Also caches the relay address for ConnectToServer.
func (a *App) GetBeamConfig() (*BeamConfig, error) {
	client := a.getClient()
	if client == nil {
		return nil, fmt.Errorf("not logged in")
	}
	config, err := client.GetBeamConfig()
	if err == nil && config.RelayAddress != "" {
		a.setRelayAddr(config.RelayAddress)
	}
	return config, err
}

// ─── Servers ─────────────────────────────────────────────────────────

// GetServers returns the server list for the logged-in user.
func (a *App) GetServers() ([]BeamServer, error) {
	client := a.getClient()
	if client == nil {
		return nil, fmt.Errorf("not logged in")
	}
	return client.GetBeamServers()
}

// ConnectToServer opens a file-ops tunnel to the Node hosting this server. The
// choice is PRESENCE-DRIVEN, decided by Core: if a relay is registered (non-empty
// relay_address) the connection ALWAYS goes through the relay so the node IP stays
// hidden; if no relay is present the app connects DIRECTLY to the node on its
// pinned-TLS beam port (a same-LAN IP or the node's public address). Direct dials
// only ever use the fingerprint-pinned TLS port, never the plain overlay port, so
// a direct hop is encrypted and MITM-proof or it does not happen at all.
//
// GETs (not POSTs): a CDN/WAF in front of Core was handing the desktop app's POSTs
// an HTML challenge page; GET /beam/* goes through cleanly.
func (a *App) ConnectToServer(serverUUID string) error {
	client := a.getClient()
	if client == nil {
		return fmt.Errorf("not logged in")
	}

	ticket, err := client.GetBeamTicket(serverUUID)
	if err != nil {
		// A mid-session raise of beam.min_version surfaces here as a typed 426:
		// show the mandatory screen instead of a generic error.
		var ure *UpdateRequiredError
		if errors.As(err, &ure) {
			a.triggerMandatoryUpdate(ure.MinVersion)
		}
		return fmt.Errorf("ticket: %w", err)
	}

	// Ask Core whether a relay is present. A non-empty address == gateway present.
	config, err := client.GetBeamConfig()
	if err != nil {
		return fmt.Errorf("beam config: %w", err)
	}
	relayAddr := strings.TrimSpace(config.RelayAddress)
	a.setRelayAddr(relayAddr)

	if relayAddr != "" {
		// Relay present -> relay only. No direct attempt: obscuring the node IP is
		// the whole point of the relay.
		rc, err := ConnectBeamNode(relayAddr, ticket.Ticket)
		if err != nil {
			// ConnectBeamNode already returns a self-descriptive English message.
			return err
		}
		if old := a.setRelay(rc); old != nil {
			old.Close()
		}
		a.startHealthCheck(serverUUID)
		return nil
	}

	// No relay -> connect directly to the node.
	rc, err := a.connectDirect(ticket)
	if err != nil {
		return err
	}
	if old := a.setRelay(rc); old != nil {
		old.Close()
	}
	a.startHealthCheck(serverUUID)
	return nil
}

// connectDirect dials the node directly on its pinned-TLS beam port, never the
// plain overlay port. It tries the LAN IPs first (a cheap TCP probe filters
// unreachable hints) and then the node's public address, returning the first that
// authenticates. A non-nil error means every direct target failed.
func (a *App) connectDirect(ticket *BeamTicket) (*BeamNodeClient, error) {
	port := ticket.LANPort
	if port == "" {
		port = "25523" // pinned-TLS beam port (BEAM_LAN_PORT default)
	}
	if ticket.LANFingerprint == "" {
		return nil, fmt.Errorf("node not reachable directly: Core provided no pinned-TLS fingerprint, and no gateway/relay is configured - add a gateway/relay to route the connection")
	}

	// 1) LAN IPs. Probe first so an unreachable hint costs a few hundred ms instead
	// of a full TLS dial timeout.
	if len(ticket.LANIPs) > 0 {
		if addr := probeBeamLAN(ticket.LANIPs, port); addr != "" {
			if rc, derr := ConnectBeamNodeDirect(addr, ticket.Ticket, ticket.LANFingerprint); derr == nil {
				return rc, nil
			} else {
				fmt.Fprintf(os.Stderr, "beam-app: direct LAN dial to %s failed: %v\n", addr, derr)
			}
		}
	}

	// 2) Public address (a remote solo node with the beam port reachable).
	// ConnectBeamNodeDirect bounds its own dial at 3s.
	if ticket.PublicAddr != "" {
		if rc, derr := ConnectBeamNodeDirect(ticket.PublicAddr, ticket.Ticket, ticket.LANFingerprint); derr == nil {
			return rc, nil
		} else {
			fmt.Fprintf(os.Stderr, "beam-app: direct public dial to %s failed: %v\n", ticket.PublicAddr, derr)
		}
	}

	return nil, fmt.Errorf("node not reachable directly and no gateway/relay is configured - open the node's beam port (%s) so this client can reach it, or add a gateway/relay to route the connection", port)
}

// ─── File Operations ─────────────────────────────────────────────────
// Prefer relay (gRPC via BeamRelay) when available; fall back to Core REST.

func (a *App) ListFiles(path string, serverUUID string) (*FileListResult, error) {
	client := a.getClient()
	if client == nil {
		return nil, fmt.Errorf("not logged in")
	}
	if relay := a.getRelay(); relay != nil {
		return relay.ListFiles(path)
	}
	return client.ListFiles(path, serverUUID)
}

func (a *App) GetFileContent(path string, serverUUID string) (*FileContentResult, error) {
	client := a.getClient()
	if client == nil {
		return nil, fmt.Errorf("not logged in")
	}
	if relay := a.getRelay(); relay != nil {
		return relay.GetFileContent(path)
	}
	return client.GetFileContent(path, serverUUID)
}

func (a *App) SaveFile(path string, content string, serverUUID string) error {
	client := a.getClient()
	if client == nil {
		return fmt.Errorf("not logged in")
	}
	if relay := a.getRelay(); relay != nil {
		return relay.SaveFile(path, content)
	}
	return client.SaveFile(path, content, serverUUID)
}

func (a *App) CreateFile(path string, isDir bool, serverUUID string) error {
	client := a.getClient()
	if client == nil {
		return fmt.Errorf("not logged in")
	}
	if relay := a.getRelay(); relay != nil {
		return relay.CreateFile(path, isDir)
	}
	return client.CreateFile(path, isDir, serverUUID)
}

func (a *App) DeleteFile(path string, serverUUID string) error {
	client := a.getClient()
	if client == nil {
		return fmt.Errorf("not logged in")
	}
	if relay := a.getRelay(); relay != nil {
		return relay.DeleteFile(path)
	}
	return client.DeleteFile(path, serverUUID)
}

func (a *App) RenameFile(oldPath string, newPath string, serverUUID string) error {
	client := a.getClient()
	if client == nil {
		return fmt.Errorf("not logged in")
	}
	if relay := a.getRelay(); relay != nil {
		// BeamNodeService takes just the new filename, not the full path
		return relay.RenameFile(oldPath, filepath.Base(newPath))
	}
	return client.RenameFile(oldPath, newPath, serverUUID)
}

func (a *App) CopyFile(srcPath string, dstPath string, serverUUID string) error {
	client := a.getClient()
	if client == nil {
		return fmt.Errorf("not logged in")
	}
	if relay := a.getRelay(); relay != nil {
		return relay.CopyFile(srcPath, dstPath)
	}
	return client.CopyFile(srcPath, dstPath, serverUUID)
}

// ─── Downloads ───────────────────────────────────────────────────────

// DownloadFile downloads a single file (or zipped directory) from the server.
// Opens a native save dialog so the user picks where to save it.
func (a *App) DownloadFile(path string, serverUUID string, isDir bool) error {
	client := a.getClient()
	if client == nil {
		return fmt.Errorf("not logged in")
	}

	suggestedName := filepath.Base(path)
	if isDir {
		suggestedName += ".zip"
	}

	savePath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Save File",
		DefaultFilename: suggestedName,
	})
	if err != nil || savePath == "" {
		return nil // user cancelled
	}

	if relay := a.getRelay(); relay != nil {
		if err := relay.DownloadFile(path, isDir, savePath, func(loaded, total int64) {
			runtime.EventsEmit(a.ctx, "download:progress", map[string]interface{}{
				"loaded": loaded,
				"total":  total,
			})
		}); err != nil {
			return err
		}
		a.setLastDownloadPath(savePath)
		return nil
	}

	rc, err := client.DownloadFile(path, serverUUID)
	if err != nil {
		return err
	}
	defer rc.Close()

	f, err := os.Create(savePath)
	if err != nil {
		return fmt.Errorf("could not create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, rc); err != nil {
		return err
	}
	a.setLastDownloadPath(savePath)
	return nil
}

// SelectiveDownload downloads selected files/folders as a zip.
// Opens a native save dialog so the user picks where to save it.
func (a *App) SelectiveDownload(basePath string, selected []string, selectAll bool, serverUUID string) error {
	client := a.getClient()
	if client == nil {
		return fmt.Errorf("not logged in")
	}

	savePath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Save Archive",
		DefaultFilename: "download.zip",
	})
	if err != nil || savePath == "" {
		return nil // user cancelled
	}

	if relay := a.getRelay(); relay != nil {
		if err := relay.SelectiveDownload(basePath, selected, selectAll, savePath, func(loaded, total int64) {
			runtime.EventsEmit(a.ctx, "download:progress", map[string]interface{}{
				"loaded": loaded,
				"total":  total,
			})
		}); err != nil {
			return err
		}
		a.setLastDownloadPath(savePath)
		return nil
	}

	rc, err := client.SelectiveDownload(basePath, selected, selectAll, serverUUID)
	if err != nil {
		return err
	}
	defer rc.Close()

	f, err := os.Create(savePath)
	if err != nil {
		return fmt.Errorf("could not create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, rc); err != nil {
		return err
	}
	a.setLastDownloadPath(savePath)
	return nil
}

// ─── Chunked Uploads (Beam-native, multi-GB safe) ────────────────────
// Three-call protocol driven by JS: Start → many Chunk → Finish (or
// Cancel). Each call references an opaque uploadID minted by the JS
// side so multiple uploads can't accidentally share a stream. The
// underlying gRPC stream stays open between calls, so chunks land
// straight on the Node's disk without buffering on Core or Relay.
// Chunks arrive base64-encoded because Wails IPC serializes everything
// as JSON; the per-chunk inflate (33%) is irrelevant compared to the
// HTTP-multipart base64-ish wrap it replaces.

func (a *App) BeamUploadStart(uploadID, path, filename, strategy string, totalSize int64) error {
	relay := a.getRelay()
	if relay == nil {
		return fmt.Errorf("no relay connection (call ConnectToServer first)")
	}
	if uploadID == "" {
		return fmt.Errorf("uploadID is required")
	}
	sess, err := relay.UploadStart(uploadID, path, filename, strategy, totalSize)
	if err != nil {
		return err
	}
	a.uploadsMu.Lock()
	// Overwriting a stale ID would orphan the previous stream — abort
	// it explicitly so we don't leak goroutines on the Node side.
	if prev := a.uploads[uploadID]; prev != nil {
		prev.Cancel()
	}
	a.uploads[uploadID] = sess
	a.uploadsMu.Unlock()
	return nil
}

// BeamUploadResume is the JS-driven retry path. JS calls it after a
// chunk Send fails (relay restart, transient network blip), and it
//   1. forces a fresh ConnectToServer — picks up a new relay address
//      from Core / re-handshakes the JWT ticket
//   2. opens a new UploadFile stream with the SAME uploadID
//      (gRPC metadata "x-beam-upload-id") so the Node reattaches to
//      the existing temp file at its current size
// JS then continues WriteChunk from the offset it had reached before
// the failure. The Node's WriteAt is idempotent on identical offsets,
// so a re-sent partial chunk is harmless.
func (a *App) BeamUploadResume(uploadID, serverUUID, path, filename, strategy string, totalSize int64) error {
	if a.getClient() == nil {
		return fmt.Errorf("not logged in")
	}
	if uploadID == "" {
		return fmt.Errorf("uploadID is required")
	}
	// Tear down the dead session before opening a new one so the
	// goroutine on the old stream doesn't outlive its tunnel.
	a.uploadsMu.Lock()
	if prev := a.uploads[uploadID]; prev != nil {
		prev.Cancel()
		delete(a.uploads, uploadID)
	}
	a.uploadsMu.Unlock()
	// Force a fresh ConnectToServer — if the relay died, the cached
	// relayClient is dead too and would just fail again.
	if err := a.ConnectToServer(serverUUID); err != nil {
		return fmt.Errorf("reconnect: %w", err)
	}
	relay := a.getRelay()
	if relay == nil {
		return fmt.Errorf("resume open: no relay connection after reconnect")
	}
	sess, err := relay.UploadStart(uploadID, path, filename, strategy, totalSize)
	if err != nil {
		return fmt.Errorf("resume open: %w", err)
	}
	a.uploadsMu.Lock()
	a.uploads[uploadID] = sess
	a.uploadsMu.Unlock()
	return nil
}

func (a *App) BeamUploadChunk(uploadID, dataB64 string, offset int64) error {
	a.uploadsMu.Lock()
	sess := a.uploads[uploadID]
	a.uploadsMu.Unlock()
	if sess == nil {
		return fmt.Errorf("unknown upload id")
	}
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return fmt.Errorf("decode chunk: %w", err)
	}
	return sess.WriteChunk(data, offset)
}

func (a *App) BeamUploadFinish(uploadID string) error {
	a.uploadsMu.Lock()
	sess := a.uploads[uploadID]
	delete(a.uploads, uploadID)
	a.uploadsMu.Unlock()
	if sess == nil {
		return fmt.Errorf("unknown upload id")
	}
	return sess.Finish()
}

// BeamUploadCancel is fire-and-forget — JS uses it as a best-effort
// cleanup when an upload aborts mid-stream (network blip, user cancel).
// Returning an error here just clutters the FileBrowser error path.
func (a *App) BeamUploadCancel(uploadID string) {
	a.uploadsMu.Lock()
	sess := a.uploads[uploadID]
	delete(a.uploads, uploadID)
	a.uploadsMu.Unlock()
	if sess != nil {
		sess.Cancel()
	}
}

// ─── Native-only extensions (Beam-specific) ──────────────────────────

// setLastDownloadPath records the destination of a just-completed
// DownloadFile/SelectiveDownload so RevealInExplorer has something
// trustworthy to open. Never derived from a JS-supplied argument.
func (a *App) setLastDownloadPath(path string) {
	a.downloadMu.Lock()
	a.lastDownloadPath = path
	a.downloadMu.Unlock()
}

// RevealInExplorer opens the platform's native file manager pointed at
// the most recently downloaded file. Used by the Panel for the "Reveal
// in Explorer / Finder" button which only renders inside the Beam
// Desktop App (the browser version has no equivalent).
//
// Deliberately no-arg (BC3 twin): this used to take a caller-supplied
// localPath and hand it straight to the OS shell-open, but it is a
// Wails-bound method reachable as window.go.main.App.RevealInExplorer(...)
// from ANY JS in the webview - the Wails webview reverse-proxies the
// remote Panel onto the wails:// origin, so a compromised/MITM'd Panel
// (or a user-set malicious Panel URL) could call it with a file://, UNC,
// or other dangerous path, triggering a native OS shell-open. The only
// legitimate target is wherever the user's own native save dialog
// (DownloadFile/SelectiveDownload) just wrote the file - not an
// arbitrary path the frontend hands us - so we track that path
// server-side instead of trusting an argument.
func (a *App) RevealInExplorer() error {
	a.downloadMu.Lock()
	path := a.lastDownloadPath
	a.downloadMu.Unlock()
	if path == "" {
		return fmt.Errorf("no downloaded file to reveal")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("downloaded file no longer exists: %w", err)
	}
	// Wails ships a cross-platform open-folder helper via runtime; we
	// pass the path as a file:// URL which BrowserOpenURL hands to the
	// OS shell. Works for both files (selects them) and directories
	// (opens them) on every supported platform.
	runtime.BrowserOpenURL(a.ctx, "file://"+filepath.ToSlash(abs))
	return nil
}
