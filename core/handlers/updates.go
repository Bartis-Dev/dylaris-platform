package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

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

// updateServiceBlock is one service's slice of the update response.
type updateServiceBlock struct {
	InstalledCount  int                 `json:"installedCount"`
	LatestCount     int                 `json:"latestCount"`
	UpdateAvailable bool                `json:"updateAvailable"`
	SeenCount       int                 `json:"seenCount"`
	Unseen          int                 `json:"unseen"`
	NewEntries      []updates.FeedEntry `json:"newEntries"`
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
