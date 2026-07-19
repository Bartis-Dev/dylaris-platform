package modpack

import (
	"encoding/json"
	"fmt"

	"dylaris-core/storage"
)

// NewProviderFromSettings reads the modpack_storage_* settings via getSetting
// and returns the configured provider. Returns (nil, nil) when no provider is
// configured (provider="local" with empty paths, or provider="s3" with empty
// bucket) - callers decide whether this is a fatal error or a soft "feature
// not configured yet" case.
//
// buildCore builds a Core-file-storage provider scoped to the given
// sub-prefix, and is only consulted for provider="core-storage". It is passed
// in rather than imported so this package never depends on handlers (which
// owns the config and already imports this package). Callers that can never
// reach the core-storage branch may pass nil.
//
// The function never panics: every setting access falls back to an empty
// string and never bubbles its error up. Only constructor failures (bad S3
// credentials, an unbuildable Core file storage config, etc.) propagate.
func NewProviderFromSettings(
	getSetting func(key string) (string, error),
	buildCore func(subPrefix string) (storage.StorageProvider, error),
) (ModpackStorageProvider, error) {
	get := func(k string) string {
		v, _ := getSetting(k)
		return v
	}
	prov := get("modpack_storage_provider")
	switch prov {
	case "", "local":
		raw := get("modpack_storage_paths")
		var paths []string
		if raw != "" {
			_ = json.Unmarshal([]byte(raw), &paths)
		}
		if len(paths) == 0 {
			return nil, nil
		}
		return &LocalProvider{Paths: paths}, nil
	case "s3":
		bucket := get("modpack_storage_s3_bucket")
		if bucket == "" {
			return nil, nil
		}
		return NewS3(
			get("modpack_storage_s3_endpoint"),
			get("modpack_storage_s3_region"),
			bucket,
			get("modpack_storage_s3_access_key"),
			get("modpack_storage_s3_secret_key"),
		)
	case "core-storage":
		if buildCore == nil {
			return nil, fmt.Errorf("modpack storage: core-storage provider unavailable (no builder supplied)")
		}
		p, err := buildCore(CoreStorageSubPrefix)
		if err != nil {
			return nil, fmt.Errorf("modpack storage: core-storage: %w", err)
		}
		return NewCoreStorageProvider(p), nil
	}
	return nil, fmt.Errorf("modpack storage: unknown provider %q", prov)
}
