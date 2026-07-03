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
)

// solderAPIBase trims the user-supplied base to its bare API root (no trailing
// slash) so callers can append "/modpack..." paths uniformly.
func solderAPIBase(base string) string {
	return strings.TrimRight(strings.TrimSpace(base), "/")
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
