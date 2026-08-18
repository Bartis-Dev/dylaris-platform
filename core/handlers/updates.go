package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"dylaris-core/services"
	"dylaris-core/updates"
)

// updatesFeedTTL bounds how often Core re-fetches a remote update feed, and so
// is the worst-case delay before a freshly pushed changelog entry reaches the
// admin bell. Short enough to see a same-day push, long enough to shield the
// upstream from the polling navbar (the panel polls every 60s but is served from
// this cache).
const updatesFeedTTL = 15 * time.Minute

// updatesEntryCap limits how many since-install entries the endpoint returns per
// service (newest first) so a long-lived feed never bloats the response.
const updatesEntryCap = 50

// updatesFeedMaxBytes caps a fetched feed body (fail-open on oversize).
const updatesFeedMaxBytes = 1 << 20

// cachedFeed is a TTL-cached snapshot of a remote feed's non-empty lines.
type cachedFeed struct {
	lines []string
	at    time.Time
}

// UpdatesHandler serves the admin-only in-panel update feed: it diffs the remote
// append-only changelog(s) against the baseline baked into this build and
// against each admin's per-service "seen" marker. Fail-open by design: an
// unreachable or absent feed yields "no updates", never an error to the caller.
type UpdatesHandler struct {
	state *AppState

	// platformInstalled is the feed line count baked into THIS build (the install
	// baseline). platformBaked are those baked lines, used as the fallback
	// "latest" when the remote fetch fails, so a failure reads as "no updates".
	platformInstalled int
	platformBaked     []string

	platformFeedURL string
	gatewayFeedURL  string

	cacheMu       sync.Mutex
	platformCache cachedFeed
	gatewayCache  cachedFeed
}

// NewUpdatesHandler wires the handler with the two remote feed URLs (platform
// always, gateway only used when gateway routing is enabled). An empty URL
// disables that feed's remote fetch.
func NewUpdatesHandler(state *AppState, platformFeedURL, gatewayFeedURL string) *UpdatesHandler {
	baked := updates.NonEmptyLines(updates.PlatformFeed())
	return &UpdatesHandler{
		state:             state,
		platformInstalled: len(baked),
		platformBaked:     baked,
		platformFeedURL:   platformFeedURL,
		gatewayFeedURL:    gatewayFeedURL,
	}
}

// updateServiceBlock is one FEED's slice of the update response (platform,
// gateway). Not one service - see perServiceBlock for that.
type updateServiceBlock struct {
	InstalledCount  int                 `json:"installedCount"`
	LatestCount     int                 `json:"latestCount"`
	UpdateAvailable bool                `json:"updateAvailable"`
	SeenCount       int                 `json:"seenCount"`
	Unseen          int                 `json:"unseen"`
	NewEntries      []updates.FeedEntry `json:"newEntries"`
	// PerService breaks the same delta down by the component each entry names,
	// each against ITS OWN installed baseline. See perServiceBlock.
	PerService []perServiceBlock `json:"perService"`
}

// perServiceBlock answers "is THIS component behind, and by what".
//
// One baseline for the whole feed is wrong the moment an operator updates
// unevenly. Core is the only component that answers /api/updates, so its own
// baked feed count used to stand in for every component - and an operator who
// deployed a new Core while leaving the node on last month's image was told the
// node's changes were installed, because Core's baseline had moved past them.
// The status quo is only correct when everything is deployed together, and
// nothing enforces that.
//
// BaselineKnown says whether this number came from the component ITSELF or is
// Core's baseline standing in. It must be surfaced: "up to date" and "nobody
// asked" look identical otherwise, and that is the confusion this replaces.
type perServiceBlock struct {
	Service string `json:"service"`
	// InstalledCount is the feed length this component was built at.
	InstalledCount int  `json:"installedCount"`
	BaselineKnown  bool `json:"baselineKnown"`
	// Behind is how many of this component's entries were published after its
	// own baseline. 0 means up to date, whatever the other components are doing.
	Behind     int                 `json:"behind"`
	NewEntries []updates.FeedEntry `json:"newEntries"`
}

// buildPerServiceBlocks splits a feed into one block per component, each diffed
// against its own baseline. baselines maps a lowercased service name to the feed
// length that component was built at; anything absent falls back to
// coreBaseline, which is the old whole-feed behaviour and stays correct for a
// fleet deployed in one go.
//
// Entries are newest-first, matching the rest of the response.
func buildPerServiceBlocks(remoteLines []string, coreBaseline int, baselines map[string]int) []perServiceBlock {
	all := updates.ParseEntries(remoteLines)
	out := []perServiceBlock{}
	for _, svc := range updates.Services(all) {
		installed, known := baselines[svc]
		if !known {
			installed = coreBaseline
		}
		// Slice the GLOBAL feed at this component's baseline, then keep its own
		// entries. Slicing per-service positions instead would compare against a
		// count that never existed as a build.
		delta := updates.ParseEntries(updates.Delta(remoteLines, installed))
		mine := updates.EntriesForService(delta, svc)
		reverseEntries(mine)
		if len(mine) > updatesEntryCap {
			mine = mine[:updatesEntryCap]
		}
		out = append(out, perServiceBlock{
			Service:        svc,
			InstalledCount: installed,
			BaselineKnown:  known,
			Behind:         len(mine),
			NewEntries:     mine,
		})
	}
	return out
}

// buildServiceBlock diffs a remote feed against the installed baseline and the
// caller's seen marker. newEntries is the since-install tail (newest first,
// capped); unseen counts entries published beyond BOTH the installed build and
// what this admin last acknowledged, so the bell badge only nags for genuinely
// new changes.
func buildServiceBlock(remoteLines []string, installedCount, seenCount int) updateServiceBlock {
	latest := len(remoteLines)
	entries := updates.ParseEntries(updates.Delta(remoteLines, installedCount))
	reverseEntries(entries)
	if len(entries) > updatesEntryCap {
		entries = entries[:updatesEntryCap]
	}
	base := installedCount
	if seenCount > base {
		base = seenCount
	}
	unseen := latest - base
	if unseen < 0 {
		unseen = 0
	}
	return updateServiceBlock{
		InstalledCount:  installedCount,
		LatestCount:     latest,
		UpdateAvailable: latest > installedCount,
		SeenCount:       seenCount,
		Unseen:          unseen,
		NewEntries:      entries,
	}
}

// serviceBaselines collects what each component reports about ITSELF.
//
//   - core: the feed baked into this binary. Authoritative, no round trip.
//   - node: the LOWEST baseline any live node reported, published by the
//     discovery loop. Absent when no node runs a build that reports one.
//   - panel: sent by the caller, because the panel is a static bundle in
//     someone's browser and Core has no other way to see which build it is.
//     Spoofable, and harmless: it only changes what that one admin is shown
//     about their own install, so it is clamped to a sane range rather than
//     trusted or rejected.
//
// A component that reports nothing is simply absent here, and the caller falls
// back to Core's baseline - the previous whole-feed behaviour, which stays
// correct for a fleet deployed in one go.
func (h *UpdatesHandler) serviceBaselines(r *http.Request) map[string]int {
	out := map[string]int{"core": h.platformInstalled}

	if h.state.Redis != nil {
		if v, err := h.state.Redis.Get(r.Context(), services.NodeFleetFeedBaselineKey).Int(); err == nil && v > 0 {
			out["node"] = v
		}
	}

	if raw := strings.TrimSpace(r.URL.Query().Get("panelBaseline")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 && v <= updatesMaxBaseline {
			out["panel"] = v
		}
	}
	return out
}

// updatesMaxBaseline bounds a caller-supplied baseline. Nothing breaks on a
// large value - Delta already clamps past-the-end to "no entries" - but an
// absurd one is a typo or a probe, not a build, and it should not be recorded as
// one.
const updatesMaxBaseline = 1_000_000

func reverseEntries(e []updates.FeedEntry) {
	for i, j := 0, len(e)-1; i < j; i, j = i+1, j-1 {
		e[i], e[j] = e[j], e[i]
	}
}

// GetUpdates GET /api/updates - ADMIN ONLY. Returns the platform update-feed
// delta (always) and the gateway delta (only when gateway routing is enabled
// AND a gateway feed URL is configured), plus the caller's unseen count for the
// navbar badge.
func (h *UpdatesHandler) GetUpdates(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	userID, _ := r.Context().Value("userID").(string)
	seenPlatform, seenGateway := 0, 0
	if userID != "" {
		if p, g, err := h.state.Store.GetUserUpdatesSeen(userID); err == nil {
			seenPlatform, seenGateway = p, g
		}
	}

	// Platform: baked baseline vs remote; on fetch failure fall back to the baked
	// lines so latest == installed and the panel shows no updates.
	platformLines := h.fetchFeedLines(r.Context(), h.platformFeedURL, &h.platformCache)
	if platformLines == nil {
		platformLines = h.platformBaked
	}
	platform := buildServiceBlock(platformLines, h.platformInstalled, seenPlatform)
	platform.PerService = buildPerServiceBlocks(platformLines, h.platformInstalled, h.serviceBaselines(r))

	resp := map[string]interface{}{
		"success":  true,
		"platform": platform,
	}
	unseen := platform.Unseen

	if h.state.gatewayEnabled() && strings.TrimSpace(h.gatewayFeedURL) != "" {
		gwLines := h.fetchFeedLines(r.Context(), h.gatewayFeedURL, &h.gatewayCache)
		// Core bakes no gateway baseline yet (the running gateway build would
		// report it via heartbeat, a later gateway-side addition), so installed
		// = 0 and every gateway entry reads as since-install until then.
		gateway := buildServiceBlock(gwLines, 0, seenGateway)
		resp["gateway"] = gateway
		unseen += gateway.Unseen
	}
	resp["unseen"] = unseen

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// MarkUpdatesSeen PUT /api/me/updates-seen - acknowledge the current feeds so
// the caller's navbar badge clears. Server-computed (ignores any client body) so
// a client can never desync the marker. Own data, authed-exempt.
func (h *UpdatesHandler) MarkUpdatesSeen(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	platformLines := h.fetchFeedLines(r.Context(), h.platformFeedURL, &h.platformCache)
	if platformLines == nil {
		platformLines = h.platformBaked
	}
	seenPlatform := len(platformLines)

	// Preserve the prior gateway marker when the gateway block is inactive so
	// toggling gateway routing off then on again does not resurface old entries.
	_, seenGateway, _ := h.state.Store.GetUserUpdatesSeen(userID)
	if h.state.gatewayEnabled() && strings.TrimSpace(h.gatewayFeedURL) != "" {
		seenGateway = len(h.fetchFeedLines(r.Context(), h.gatewayFeedURL, &h.gatewayCache))
	}

	if err := h.state.Store.SetUserUpdatesSeen(userID, seenPlatform, seenGateway); err != nil {
		sendJSONError(w, "Failed to save", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// fetchFeedLines returns a remote feed's non-empty lines, TTL-cached. Any error
// (unset URL, network, non-200, oversize) fails open to nil so the caller falls
// back to the baked baseline and the panel simply shows no new updates. Failures
// are cached too, to avoid hammering the upstream on the hot path.
func (h *UpdatesHandler) fetchFeedLines(ctx context.Context, url string, cache *cachedFeed) []string {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	if !cache.at.IsZero() && time.Since(cache.at) < updatesFeedTTL {
		return cache.lines
	}
	cache.lines = fetchRemoteFeed(ctx, url)
	cache.at = time.Now()
	return cache.lines
}

// fetchRemoteFeed GETs a JSONL feed and returns its non-empty lines, or nil on
// any failure. The body is size-capped; the whole call is fail-open by design.
func fetchRemoteFeed(ctx context.Context, url string) []string {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, updatesFeedMaxBytes))
	if err != nil {
		return nil
	}
	return updates.NonEmptyLines(body)
}
