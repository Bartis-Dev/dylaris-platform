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
func (f *FeatureFlags) Get(ctx context.Context, key string, defaultV bool) bool {
	_ = ctx // accepted for future-proofing; current impl is sync
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

// Invalidate drops the cached entry for a key so the next Get re-reads from
// the store. Called by settings PUT handlers after a write.
func (f *FeatureFlags) Invalidate(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.cache, key)
}
