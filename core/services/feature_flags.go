package services

import (
	"context"
	"strconv"
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
	cacheTTL time.Duration
}

type cachedFlag struct {
	value bool
	at    time.Time
}

func NewFeatureFlags(st settingsReader) *FeatureFlags {
	return &FeatureFlags{
		store:    st,
		cache:    map[string]cachedFlag{},
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

// IsRedisACLEnabled gates per-node Redis ACL provisioning for untrusted BYON
// nodes. Default = false: the platform keeps the shared-password Redis setup.
// Enabling REQUIRES Valkey deployed with an aclfile (see docs/superpowers/
// redis-acl-deploy.md) or ACL persistence will silently not survive a restart.
func (f *FeatureFlags) IsRedisACLEnabled(ctx context.Context) bool {
	return f.Get(ctx, "feature_redis_acl", false)
}

// Invalidate drops the cached entry for a key so the next Get re-reads from
// the store. Called by settings PUT handlers after a write.
func (f *FeatureFlags) Invalidate(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.cache, key)
}
