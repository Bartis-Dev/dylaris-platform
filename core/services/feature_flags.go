package services

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

// settingsReader is the slice of the Store interface this service needs.
// Defining it locally avoids an import cycle between services and store.
type settingsReader interface {
	GetSetting(key string) (string, error)
}

// FeatureFlags is a 60-second-cached read of boolean platform feature toggles
// stored in the settings table. The cache is best-effort — a writer that
// flips a flag should call Invalidate(key) so its own subsequent reads pick
// up the new value within the same Core process; cross-process staleness is
// bounded by the cache TTL.
type FeatureFlags struct {
	store    settingsReader
	mu       sync.Mutex
	cache    map[string]cachedFlag
	cacheInt map[string]cachedInt
	cacheTTL time.Duration
}

type cachedFlag struct {
	value bool
	at    time.Time
}

type cachedInt struct {
	value int
	at    time.Time
}

func NewFeatureFlags(st settingsReader) *FeatureFlags {
	return &FeatureFlags{
		store:    st,
		cache:    map[string]cachedFlag{},
		cacheInt: map[string]cachedInt{},
		cacheTTL: 60 * time.Second,
	}
}

// Get returns the boolean value for key, defaulting to defaultV when the
// setting is missing or unparseable.
func (f *FeatureFlags) Get(_ context.Context, key string, defaultV bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cf, ok := f.cache[key]; ok && time.Since(cf.at) < f.cacheTTL {
		return cf.value
	}
	v, err := f.store.GetSetting(key)
	if err != nil || v == "" {
		return defaultV
	}
	b, parseErr := strconv.ParseBool(v)
	if parseErr != nil {
		return defaultV
	}
	f.cache[key] = cachedFlag{value: b, at: time.Now()}
	return b
}

// GetInt returns the integer value for key, defaulting to defaultV when the
// setting is missing or unparseable. Cached like Get.
func (f *FeatureFlags) GetInt(_ context.Context, key string, defaultV int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ci, ok := f.cacheInt[key]; ok && time.Since(ci.at) < f.cacheTTL {
		return ci.value
	}
	v, err := f.store.GetSetting(key)
	if err != nil || v == "" {
		return defaultV
	}
	n, perr := strconv.Atoi(strings.TrimSpace(v))
	if perr != nil {
		return defaultV
	}
	f.cacheInt[key] = cachedInt{value: n, at: time.Now()}
	return n
}

// IsModpacksEnabled is a convenience wrapper for the most-checked flag.
// Default = true (features ship enabled; admins opt out).
func (f *FeatureFlags) IsModpacksEnabled(ctx context.Context) bool {
	return f.Get(ctx, "feature_modpacks_enabled", true)
}

// IsTicketsEnabled gates the ticket subsystem (tickets, categories, canned
// responses, attachments, notifications, settings, deletion log). Default =
// false (the feature ships OFF and the admin opts in via Settings → Features).
func (f *FeatureFlags) IsTicketsEnabled(ctx context.Context) bool {
	return f.Get(ctx, "feature_tickets_enabled", false)
}

// IsAutoMoveEnabled gates the gateway-only auto-move (server migration between
// nodes) feature. Default = false (opt-in, and only meaningful while gateway
// routing is active — the gateway is what lets a server keep its address after
// it changes node). The handler layer ANDs this with the live routing mode.
func (f *FeatureFlags) IsAutoMoveEnabled(ctx context.Context) bool {
	return f.Get(ctx, "feature_auto_move_enabled", false)
}

// IsBYONEnabled gates the bring-your-own-node multi-tenancy (per-user node
// enrollment, node ownership scoping, plans/billing). Default = false: the
// platform ships as today's single-operator panel and the operator opts in.
func (f *FeatureFlags) IsBYONEnabled(ctx context.Context) bool {
	return f.Get(ctx, "feature_byon_enabled", false)
}

// IsShareLinksEnabled gates CREATION of tokenized modpack share/download links.
// Default = false (opt-in distribution surface; the admin enables it in
// Settings -> Modpacks). Existing links keep serving while the parent modpacks
// feature is on; this only gates minting new links.
func (f *FeatureFlags) IsShareLinksEnabled(ctx context.Context) bool {
	return f.Get(ctx, "modpack_share_links_enabled", false)
}

// IsTabProxyEnabled is the WS5 master toggle. Default false: the custom-tab
// reverse proxy is inert until an admin enables it.
func (f *FeatureFlags) IsTabProxyEnabled(ctx context.Context) bool {
	return f.Get(ctx, "feature_tab_proxy_enabled", false)
}

// TabProxyAllowPublicLinks gates anonymous public share links. Default false.
func (f *FeatureFlags) TabProxyAllowPublicLinks(ctx context.Context) bool {
	return f.Get(ctx, "tab_proxy_allow_public_links", false)
}

// TabProxyMaxPerServer caps proxied tabs per server. Default 10 (floored >0).
func (f *FeatureFlags) TabProxyMaxPerServer(ctx context.Context) int {
	if v := f.GetInt(ctx, "tab_proxy_max_per_server", 10); v > 0 {
		return v
	}
	return 10
}

// TabProxyMaxShareLinksPerUser caps active share links per user. Default 20.
func (f *FeatureFlags) TabProxyMaxShareLinksPerUser(ctx context.Context) int {
	if v := f.GetInt(ctx, "tab_proxy_max_share_links_per_user", 20); v > 0 {
		return v
	}
	return 20
}

// Invalidate drops the cached entry for a key so the next Get re-reads from
// the store. Called by settings PUT handlers after a write.
func (f *FeatureFlags) Invalidate(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.cache, key)
	delete(f.cacheInt, key)
}
