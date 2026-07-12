package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"runtime"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// AppVersion is the running build, injected at build time via
// -ldflags "-X main.AppVersion=<build-number>". "dev" for local builds. The
// updater compares it to the manifest's version.
var AppVersion = "dev"

// manifestURL is the R2-hosted manifest the app checks for updates. Public, so
// it works regardless of the source repo's visibility.
const manifestURL = "https://downloads.dylaris.com/beam/latest.json"

// UpdateInfo is returned to the frontend to drive the "update available" notice.
type UpdateInfo struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"updateAvailable"`
	DownloadURL     string `json:"downloadUrl"`
}

type manifest struct {
	Version   string `json:"version"`
	Platforms map[string]struct {
		URL string `json:"url"`
	} `json:"platforms"`
}

// GetUpdateInfo fetches the R2 manifest and reports whether a newer build exists
// + the download URL for this platform. Best-effort: any error reports "no
// update" so it never blocks the app. A build differing from the manifest's
// version (by equality, since builds aren't ordered) counts as an update.
func (a *App) GetUpdateInfo() *UpdateInfo {
	info := &UpdateInfo{Current: AppVersion}
	m, err := fetchManifest()
	if err != nil {
		return info
	}
	info.Latest = m.Version
	// "dev" builds never nag; otherwise any difference means a newer build.
	if AppVersion != "dev" && m.Version != "" && m.Version != AppVersion {
		info.UpdateAvailable = true
	}
	if p, ok := m.Platforms[runtime.GOOS+"-"+runtime.GOARCH]; ok {
		info.DownloadURL = p.URL
	}
	return info
}

func fetchManifest() (*manifest, error) {
	req, _ := http.NewRequest(http.MethodGet, manifestURL, nil)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, http.ErrNotSupported
	}
	var m manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// OpenUpdateDownload opens the update manifest's platform-specific download
// URL in the user's system browser (so it doesn't navigate the app's
// webview). Deliberately no-arg (BC3): OpenInBrowser used to take an
// arbitrary caller-supplied URL, but it is a Wails-bound method reachable as
// window.go.main.App.OpenInBrowser(...) from ANY JS in the webview - the
// Wails webview reverse-proxies the remote Panel onto the wails:// origin,
// so a compromised/MITM'd Panel (or a user-set malicious Panel URL) could
// call it with a file://, UNC, or other dangerous scheme, triggering a
// native OS shell-open. The no-arg design removes that JS-supplied-argument
// vector, but the manifest's DownloadURL is still Go-origin input passed
// straight to BrowserOpenURL, bypassing the Task 1 dispatcher's scheme
// check. The allowlist below additionally constrains even a poisoned or
// MITM'd manifest's DownloadURL to http/https, so it cannot reach a
// file:// shell-open either.
func (a *App) OpenUpdateDownload() {
	info := a.GetUpdateInfo()
	if info.DownloadURL == "" || a.ctx == nil {
		return
	}
	if u, err := url.Parse(info.DownloadURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return
	}
	wailsruntime.BrowserOpenURL(a.ctx, info.DownloadURL)
}
