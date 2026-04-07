package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// VersionEntry represents a single version with its major group and build string.
type VersionEntry struct {
	Major string `json:"major"` // e.g. "1.21"
	Build string `json:"build"` // e.g. "1.21.4"
}

// SoftwareProvider is the interface for version sources.
type SoftwareProvider interface {
	Name() string
	FetchVersions() ([]VersionEntry, error)
}

// SoftwareInfo is returned by GetSoftwareList with type metadata.
type SoftwareInfo struct {
	Name string `json:"name"`
	Type string `json:"type"` // "game" or "proxy"
}

// ==========================================
// PaperMC Provider (generic for Paper, Velocity, Waterfall)
// ==========================================

type PaperMCProvider struct {
	project string
}

func (p *PaperMCProvider) Name() string { return p.project }

func (p *PaperMCProvider) FetchVersions() ([]VersionEntry, error) {
	url := fmt.Sprintf("https://api.papermc.io/v2/projects/%s", p.project)
	resp, err := fetchJSON(url)
	if err != nil {
		return nil, err
	}

	versions, ok := resp["versions"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected %s API response", p.project)
	}

	var entries []VersionEntry
	for i := len(versions) - 1; i >= 0; i-- { // newest first
		build, _ := versions[i].(string)
		if build == "" {
			continue
		}
		entries = append(entries, VersionEntry{
			Major: getMajorVersion(build),
			Build: build,
		})
	}
	return entries, nil
}

// ==========================================
// Vanilla Provider
// ==========================================

type VanillaProvider struct{}

func (v *VanillaProvider) Name() string { return "vanilla" }

func (v *VanillaProvider) FetchVersions() ([]VersionEntry, error) {
	resp, err := fetchJSON("https://launchermeta.mojang.com/mc/game/version_manifest.json")
	if err != nil {
		return nil, err
	}

	rawVersions, ok := resp["versions"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected vanilla API response")
	}

	var entries []VersionEntry
	for _, rv := range rawVersions {
		m, _ := rv.(map[string]interface{})
		if m == nil {
			continue
		}
		if m["type"] != "release" {
			continue
		}
		id, _ := m["id"].(string)
		if id == "" {
			continue
		}
		entries = append(entries, VersionEntry{
			Major: getMajorVersion(id),
			Build: id,
		})
	}
	return entries, nil
}

// ==========================================
// BungeeCord Provider (Jenkins CI)
// ==========================================

type BungeeCordProvider struct{}

func (b *BungeeCordProvider) Name() string { return "bungeecord" }

func (b *BungeeCordProvider) FetchVersions() ([]VersionEntry, error) {
	resp, err := fetchJSON("https://ci.md-5.net/job/BungeeCord/api/json?tree=builds[number,result]{0,20}")
	if err != nil {
		return nil, err
	}

	builds, ok := resp["builds"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected BungeeCord CI response")
	}

	var entries []VersionEntry
	for _, rb := range builds {
		m, _ := rb.(map[string]interface{})
		if m == nil {
			continue
		}
		result, _ := m["result"].(string)
		if result != "SUCCESS" {
			continue
		}
		number, _ := m["number"].(float64)
		if number == 0 {
			continue
		}
		build := fmt.Sprintf("#%d", int(number))
		entries = append(entries, VersionEntry{
			Major: "latest",
			Build: build,
		})
	}
	return entries, nil
}

// ==========================================
// Provider Registry
// ==========================================

var softwareProviders = map[string]SoftwareProvider{
	"paper":      &PaperMCProvider{project: "paper"},
	"vanilla":    &VanillaProvider{},
	"velocity":   &PaperMCProvider{project: "velocity"},
	"waterfall":  &PaperMCProvider{project: "waterfall"},
	"bungeecord": &BungeeCordProvider{},
}

// softwareTypes maps provider names to their server type (game or proxy).
var softwareTypes = map[string]string{
	"paper":      "game",
	"vanilla":    "game",
	"velocity":   "proxy",
	"waterfall":  "proxy",
	"bungeecord": "proxy",
}

// ==========================================
// Handler
// ==========================================

type VersionHandler struct {
	state *AppState
}

func NewVersionHandler(state *AppState) *VersionHandler {
	return &VersionHandler{state: state}
}

// GetVersions GET /api/versions?software=paper
func (h *VersionHandler) GetVersions(w http.ResponseWriter, r *http.Request) {
	software := r.URL.Query().Get("software")
	provider, ok := softwareProviders[software]
	if !ok {
		sendJSONError(w, "Unknown software: "+software, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	cacheKey := fmt.Sprintf("dylaris:versions:%s", software)

	// Try Redis cache first
	if h.state.Redis != nil {
		cached, err := h.state.Redis.Get(ctx, cacheKey).Bytes()
		if err == nil && len(cached) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.Write(cached)
			return
		}
	}

	versions, err := provider.FetchVersions()
	if err != nil {
		sendJSONError(w, "Failed to fetch versions: "+err.Error(), http.StatusBadGateway)
		return
	}

	result := map[string]interface{}{
		"success":  true,
		"software": software,
		"versions": versions,
	}

	data, _ := json.Marshal(result)

	// Cache for 5 minutes
	if h.state.Redis != nil {
		h.state.Redis.Set(ctx, cacheKey, data, 5*time.Minute)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// GetSoftwareList GET /api/versions/software (public)
func (h *VersionHandler) GetSoftwareList(w http.ResponseWriter, r *http.Request) {
	list := make([]SoftwareInfo, 0, len(softwareProviders))
	for name := range softwareProviders {
		stype := softwareTypes[name]
		if stype == "" {
			stype = "game"
		}
		list = append(list, SoftwareInfo{Name: name, Type: stype})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"software": list,
	})
}

// ==========================================
// Helpers
// ==========================================

func getMajorVersion(version string) string {
	parts := splitDot(version)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return version
}

func splitDot(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func fetchJSON(url string) (map[string]interface{}, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}
