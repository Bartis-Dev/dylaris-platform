package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Modrinth API proxy. Mods/plugins browse, version metadata,
// project details. Cached in Redis to keep panel browse traffic off
// Modrinth's rate limit and to make the UI feel instant.
//
// User-Agent: Modrinth's terms ask for an identifying UA; we send
// "Dylaris/<frontend-url> (admin contact via that site)". Operators can
// override with DYLARIS_MODRINTH_UA in the future if needed.

const (
	modrinthBaseURL     = "https://api.modrinth.com/v2"
	modrinthSearchTTL   = 5 * time.Minute
	modrinthProjectTTL  = 1 * time.Hour
	modrinthCategoryTTL = 24 * time.Hour
)

type ModrinthHandler struct {
	state *AppState
	ua    string
	http  *http.Client
}

func NewModrinthHandler(state *AppState, ua string) *ModrinthHandler {
	if ua == "" {
		ua = "Dylaris/unknown (https://github.com/Bartis-Dev/dylaris-platform)"
	}
	return &ModrinthHandler{
		state: state,
		ua:    ua,
		http:  &http.Client{Timeout: 15 * time.Second},
	}
}

// proxyJSON fetches the URL with caching. The cache key is a sha256 of the
// full URL so we don't accidentally collide between similar queries.
func (h *ModrinthHandler) proxyJSON(ctx context.Context, ttl time.Duration, urlStr string, w http.ResponseWriter) {
	cacheKey := "dylaris:modrinth:" + hashURL(urlStr)
	if h.state.Redis != nil {
		if cached, err := h.state.Redis.Get(ctx, cacheKey).Result(); err == nil && cached != "" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Cache", "HIT")
			w.Write([]byte(cached))
			return
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		sendJSONError(w, "Bad upstream request", http.StatusBadGateway)
		return
	}
	req.Header.Set("User-Agent", h.ua)
	req.Header.Set("Accept", "application/json")

	resp, err := h.http.Do(req)
	if err != nil {
		sendJSONError(w, "Upstream call failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Cache only successful 200s — error responses change shape and
	// shouldn't poison the cache for 5+ minutes.
	if resp.StatusCode == http.StatusOK && h.state.Redis != nil {
		h.state.Redis.Set(ctx, cacheKey, string(body), ttl)
	}

	w.Header().Set("Content-Type", "application/json")
	if resp.StatusCode == http.StatusOK {
		w.Header().Set("X-Cache", "MISS")
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

func hashURL(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

// Search GET /api/modrinth/search
// Query params (Modrinth-compatible): query, limit, offset, facets (JSON
// array of arrays), loaders (comma list), versions (comma list), categories
// (comma list), project_type.
//
// We re-translate loaders/versions/categories into facets for Modrinth's API
// since the facets shape is the actual filter mechanism; the comma params
// are panel-friendly aliases.
func (h *ModrinthHandler) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	values := url.Values{}
	if v := strings.TrimSpace(q.Get("query")); v != "" {
		if len(v) > 200 { // bound the cache-key / upstream query size
			v = v[:200]
		}
		values.Set("query", v)
	}
	if v := q.Get("limit"); v != "" {
		values.Set("limit", v)
	}
	if v := q.Get("offset"); v != "" {
		values.Set("offset", v)
	}
	if v := q.Get("index"); v != "" {
		values.Set("index", v)
	}

	// Facets: [["categories:fabric"],["versions:1.20.2"],["project_type:mod"]]
	type facet = []string
	facets := []facet{}
	addFacet := func(prefix, csv string) {
		csv = strings.TrimSpace(csv)
		if csv == "" {
			return
		}
		for _, item := range strings.Split(csv, ",") {
			item = strings.TrimSpace(item)
			if item != "" {
				facets = append(facets, facet{prefix + ":" + item})
			}
		}
	}
	addFacet("categories", q.Get("loaders"))
	addFacet("versions", q.Get("versions"))
	addFacet("categories", q.Get("categories"))
	addFacet("project_type", q.Get("project_type"))

	if len(facets) > 0 {
		raw, _ := json.Marshal(facets)
		values.Set("facets", string(raw))
	}

	upstream := modrinthBaseURL + "/search"
	if encoded := values.Encode(); encoded != "" {
		upstream += "?" + encoded
	}
	h.proxyJSON(r.Context(), modrinthSearchTTL, upstream, w)
}

// Project GET /api/modrinth/project/{slug} — full project metadata.
func (h *ModrinthHandler) Project(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.URL.Query().Get("slug"))
	if slug == "" {
		// Path-style: /api/modrinth/project/<slug>
		slug = strings.TrimPrefix(r.URL.Path, "/api/modrinth/project/")
	}
	if slug == "" {
		sendJSONError(w, "slug required", http.StatusBadRequest)
		return
	}
	upstream := fmt.Sprintf("%s/project/%s", modrinthBaseURL, url.PathEscape(slug))
	h.proxyJSON(r.Context(), modrinthProjectTTL, upstream, w)
}

// ProjectVersions GET /api/modrinth/project/{slug}/versions?loaders=&versions=
func (h *ModrinthHandler) ProjectVersions(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.URL.Query().Get("slug"))
	if slug == "" {
		path := strings.TrimPrefix(r.URL.Path, "/api/modrinth/project/")
		path = strings.TrimSuffix(path, "/versions")
		slug = path
	}
	if slug == "" {
		sendJSONError(w, "slug required", http.StatusBadRequest)
		return
	}
	values := url.Values{}
	if loaders := r.URL.Query().Get("loaders"); loaders != "" {
		raw, _ := json.Marshal(strings.Split(loaders, ","))
		values.Set("loaders", string(raw))
	}
	if versions := r.URL.Query().Get("versions"); versions != "" {
		raw, _ := json.Marshal(strings.Split(versions, ","))
		values.Set("game_versions", string(raw))
	}
	upstream := fmt.Sprintf("%s/project/%s/version", modrinthBaseURL, url.PathEscape(slug))
	if enc := values.Encode(); enc != "" {
		upstream += "?" + enc
	}
	h.proxyJSON(r.Context(), modrinthProjectTTL, upstream, w)
}

// Version GET /api/modrinth/version/{id} — single version metadata.
// Used by the install path to pull the download URL + sha512 before sending
// the install command to the node.
func (h *ModrinthHandler) Version(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/modrinth/version/")
	if id == "" {
		sendJSONError(w, "version id required", http.StatusBadRequest)
		return
	}
	upstream := fmt.Sprintf("%s/version/%s", modrinthBaseURL, url.PathEscape(id))
	h.proxyJSON(r.Context(), modrinthProjectTTL, upstream, w)
}

// Categories GET /api/modrinth/categories — the Modrinth category tag list
// (each entry: name, project_type, header, icon SVG). Very stable, so it is
// cached for a day; the panel uses it to build the always-visible category
// sidebar in the Content tab.
func (h *ModrinthHandler) Categories(w http.ResponseWriter, r *http.Request) {
	upstream := modrinthBaseURL + "/tag/category"
	h.proxyJSON(r.Context(), modrinthCategoryTTL, upstream, w)
}
