package modpack

import (
	"encoding/json"
	"fmt"
)

// NewProviderFromSettings reads the modpack_storage_* settings via getSetting
// and returns the configured provider. Returns (nil, nil) when no provider is
// configured (provider="local" with empty paths, or provider="s3" with empty
// bucket) — callers decide whether this is a fatal error or a soft "feature
// not configured yet" case.
//
// The function never panics: every setting access falls back to an empty
// string and never bubbles its error up. Only constructor failures (bad S3
// credentials, etc.) propagate.
func NewProviderFromSettings(getSetting func(key string) (string, error)) (ModpackStorageProvider, error) {
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
	}
	return nil, fmt.Errorf("modpack storage: unknown provider %q", prov)
}
