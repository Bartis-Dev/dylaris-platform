package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// External Solder API response shapes (Technic Solder read contract). Only the
// fields consumed on import are modeled; unknown fields are ignored.

// SolderModpack is GET /api/modpack/{slug}: the pack identity + its build list.
type SolderModpack struct {
	Name        string   `json:"name"`         // slug
	DisplayName string   `json:"display_name"` // human name
	Recommended string   `json:"recommended"`
	Latest      string   `json:"latest"`
	Builds      []string `json:"builds"`
	Error       string   `json:"error"`
}

// SolderBuild is GET /api/modpack/{slug}/{build}: MC/loader/memory + mod list.
type SolderBuild struct {
	Minecraft string      `json:"minecraft"`
	Java      string      `json:"java"`
	Memory    int         `json:"memory"`
	Forge     string      `json:"forge"` // loader version (legacy Solder field name)
	Mods      []SolderMod `json:"mods"`
	Error     string      `json:"error"`
}

// SolderMod is one entry of a build's mods[]: slug + version + the mirror URL.
type SolderMod struct {
	Name     string `json:"name"` // slug
	Version  string `json:"version"`
	MD5      string `json:"md5"`
	URL      string `json:"url"`
	Filesize int64  `json:"filesize"`
}

const (
	solderMetaMaxBytes = 4 << 20 // 4 MiB cap on any Solder API JSON response
	solderMetaTimeout  = 30 * time.Second
	// solderRootProbeTimeout bounds the info-endpoint reachability probe in
	// ResolveSolderBase; it is a small HEAD-like GET, not the real fetch.
	solderRootProbeTimeout = 10 * time.Second
)

// solderAPIBase trims the user-supplied base to its bare API root (no trailing
// slash) so callers can append "/modpack..." paths uniformly.
func solderAPIBase(base string) string {
	return strings.TrimRight(strings.TrimSpace(base), "/")
}

// solderBaseCandidates returns the ordered API-base candidates to try for a
// user-supplied Solder URL: the base as given, then the base with "/api"
// appended. Solder servers (TechnicSolder, solder.py) serve the API under /api,
// but users routinely paste the bare host; this lets both work. The "/api"
// variant is skipped when the base already ends in /api (no /api/api).
func solderBaseCandidates(rawBase string) []string {
	base := solderAPIBase(rawBase)
	out := []string{base}
	if base != "" && !strings.HasSuffix(base, "/api") {
		out = append(out, base+"/api")
	}
	return out
}

// isSolderInfoBody reports whether body is a Solder info document: a JSON object
// carrying a non-empty "api" field (e.g. {"api":"TechnicSolder"} or
// {"api":"solder.py"}). This is the stable marker of a Solder API root, served
// at {base}/ regardless of how many modpacks the instance has.
func isSolderInfoBody(body []byte) bool {
	var info struct {
		API string `json:"api"`
	}
	if json.Unmarshal(body, &info) != nil {
		return false
	}
	return strings.TrimSpace(info.API) != ""
}

// ResolveSolderBase resolves a user-supplied Solder URL to its working API base
// by probing the Solder info endpoint ({candidate}/) for each candidate from
// solderBaseCandidates and returning the first that answers as a Solder root.
// If none do (unreachable, or not a Solder server), the trimmed base as given is
// returned unchanged, so the caller surfaces the same error it would have
// before this resolution existed. The probe uses the SSRF-safe fetcher.
func ResolveSolderBase(ctx context.Context, rawBase string) string {
	candidates := solderBaseCandidates(rawBase)
	for _, c := range candidates {
		body, err := SafeFetch(ctx, c+"/", solderMetaMaxBytes, solderRootProbeTimeout)
		if err == nil && isSolderInfoBody(body) {
			return c
		}
	}
	return candidates[0]
}

// FetchSolderIndex reads GET {base}/modpack and returns the slug->display-name
// map the instance advertises.
func FetchSolderIndex(ctx context.Context, base string) (map[string]string, error) {
	var out struct {
		Modpacks map[string]string `json:"modpacks"`
		Error    string            `json:"error"`
	}
	if err := getSolderJSON(ctx, solderAPIBase(base)+"/modpack", &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("solder error: %s", out.Error)
	}
	if out.Modpacks == nil {
		return nil, fmt.Errorf("not a Solder instance (no modpacks field)")
	}
	return out.Modpacks, nil
}

// FetchSolderModpack reads GET {base}/modpack/{slug}.
func FetchSolderModpack(ctx context.Context, base, slug string) (*SolderModpack, error) {
	var mp SolderModpack
	u := solderAPIBase(base) + "/modpack/" + url.PathEscape(slug)
	if err := getSolderJSON(ctx, u, &mp); err != nil {
		return nil, err
	}
	if mp.Error != "" {
		return nil, fmt.Errorf("solder error: %s", mp.Error)
	}
	return &mp, nil
}

// FetchSolderBuild reads GET {base}/modpack/{slug}/{build}.
func FetchSolderBuild(ctx context.Context, base, slug, build string) (*SolderBuild, error) {
	var b SolderBuild
	u := solderAPIBase(base) + "/modpack/" + url.PathEscape(slug) + "/" + url.PathEscape(build)
	if err := getSolderJSON(ctx, u, &b); err != nil {
		return nil, err
	}
	if b.Error != "" {
		return nil, fmt.Errorf("solder error: %s", b.Error)
	}
	return &b, nil
}

func getSolderJSON(ctx context.Context, rawURL string, v interface{}) error {
	body, err := SafeFetch(ctx, rawURL, solderMetaMaxBytes, solderMetaTimeout)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("bad JSON from upstream: %w", err)
	}
	return nil
}
