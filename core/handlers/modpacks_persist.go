package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"dylaris-core/models"
	"dylaris-core/storage/modpack"
)

// persistMrpackIfBetaOrRelease guarantees that a freshly-built .mrpack lives in
// storage for non-draft versions, and lazily writes it on the first call so we
// don't waste disk on every draft tweak. The same call freezes the version
// (frozen=true), so subsequent AddMod / RemoveMod / DeleteVersion calls return
// 409 instead of mutating bytes that downstream Modrinth or installer users may
// already hold.
//
// Returns the canonical bytes (either freshly built or read back from storage).
// For drafts, the version stays unfrozen and unsaved.
//
// Errors:
//   - "no modpack storage configured" when the channel is beta/release and the
//     admin hasn't set a path/bucket yet. Caller surfaces this so admin knows.
//   - underlying provider errors (disk full, bad creds, etc.)
func (s *AppState) persistMrpackIfBetaOrRelease(pack *models.Modpack, version *models.ModpackVersion, mods []models.ModpackMod) ([]byte, error) {
	// If already persisted, read back. This also covers the export path so
	// repeated downloads serve identical bytes byte-for-byte.
	if version.MrpackStorageKey != "" {
		prov, _ := modpack.NewProviderFromSettings(s.Store.GetSetting)
		if prov != nil {
			if data, err := prov.Get(version.MrpackStorageKey); err == nil {
				return data, nil
			}
			// Storage missing the key — fall through to rebuild + re-persist.
		}
	}

	owner, _ := s.Store.GetUserByID(pack.OwnerID)
	data, err := buildMrpackBytes(pack, version, mods, owner)
	if err != nil {
		return nil, err
	}

	// Drafts are never persisted or frozen — they get rebuilt fresh on every
	// download / publish attempt.
	if version.Channel == models.ModpackChannelDraft {
		return data, nil
	}

	prov, err := modpack.NewProviderFromSettings(s.Store.GetSetting)
	if err != nil {
		return nil, fmt.Errorf("modpack storage misconfigured: %w", err)
	}
	if prov == nil {
		return nil, errors.New("no modpack storage configured — admin must set a path in Settings → Modpacks")
	}

	key := fmt.Sprintf("modpacks/%s/%s/%s/pack.mrpack",
		pack.OwnerID, pack.Slug, version.VersionString)
	if err := prov.Put(key, data); err != nil {
		return nil, fmt.Errorf("modpack storage put: %w", err)
	}

	sum := sha256.Sum256(data)
	version.MrpackStorageKey = key
	version.MrpackSHA256 = hex.EncodeToString(sum[:])
	version.FileSize = int64(len(data))
	version.Frozen = true
	if err := s.Store.UpdateModpackVersion(version); err != nil {
		// Storage already has the bytes; DB stamp failed. Surface so caller
		// knows the next call will retry the stamp.
		return nil, fmt.Errorf("modpack storage persisted but local stamp failed: %w", err)
	}
	return data, nil
}
