package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"dylaris-core/models"
	"dylaris-core/services"
	"dylaris-core/storage/modpack"
)

// solderRenderTimeout bounds the whole render (its Modrinth downloads) so a slow
// CDN cannot hold the publish request open indefinitely.
const solderRenderTimeout = 5 * time.Minute

// solderMod is one materialized mods[] artifact + the storage key its zip lives at.
// SolderKey is stored in the manifest; the read path turns it into url = mirrorBase+SolderKey.
type solderMod struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	MD5       string `json:"md5"`
	SolderKey string `json:"solderKey"`
	Filesize  int64  `json:"filesize"`
}

// solderManifest is the deterministic build.json persisted per published build; the
// read path (GetBuild) reads it back and projects it into the launcher's build shape.
type solderManifest struct {
	Minecraft string      `json:"minecraft"`
	Java      string      `json:"java"`
	Memory    int         `json:"memory"`
	Mods      []solderMod `json:"mods"`
}

// solderModStorageKey is the deterministic public key for a materialized mod zip.
// It is the suffix the mirror route serves after /solder/mirror/, so it must be
// path-clean and start with "solder/".
func solderModStorageKey(ownerID, modSlug, version string) string {
	return fmt.Sprintf("solder/mods/%s/%s/%s-%s.zip", ownerID, modSlug, modSlug, version)
}

// solderManifestKey is the deterministic key of a build's manifest. Recomputed by
// the read path from pack+build (no DB column). CAVEAT: changing a pack's SolderSlug
// changes this key, so builds must be re-published after a slug change.
func solderManifestKey(ownerID, solderSlug, versionString string) string {
	return fmt.Sprintf("solder/manifests/%s/%s/%s/build.json", ownerID, solderSlug, versionString)
}

// safeSolderKeyComponent rejects a storage-key path component that could escape the
// solder/ namespace at Put time. Modrinth version numbers reach us verbatim (no
// charset validation), so a crafted "../.." could otherwise clobber another tenant's
// objects — and the mirror's read-time guard cannot undo a bad write.
func safeSolderKeyComponent(s string) bool {
	return s != "" && !strings.ContainsAny(s, `/\`) && !strings.Contains(s, "..")
}

// renderSolderBuild materializes every content entry of the build into the public
// solder/ namespace, injects the Phase-3a loader as an ordinary mods[] entry,
// writes the deterministic manifest, then freezes + marks the build Solder-published.
// It is idempotent: re-running produces byte-identical artifacts + manifest.
func (h *PacksHandler) renderSolderBuild(pack *models.Pack, build *models.PackBuild) error {
	ctx, cancel := context.WithTimeout(context.Background(), solderRenderTimeout)
	defer cancel()

	content, err := h.state.Store.ListBuildContent(build.ID) // already ORDER BY slug ASC
	if err != nil {
		return fmt.Errorf("list build content: %w", err)
	}

	prov, err := modpack.NewProviderFromSettings(h.state.Store.GetSetting)
	if err != nil {
		return fmt.Errorf("modpack storage not configured: %w", err)
	}
	if prov == nil {
		return fmt.Errorf("modpack storage not configured")
	}

	mods := make([]solderMod, 0, len(content)+1)

	for _, e := range content {
		if !safeSolderKeyComponent(e.ModSlug) || !safeSolderKeyComponent(e.Version) {
			return fmt.Errorf("content %q has an invalid slug or version (path characters not allowed)", e.ModSlug)
		}
		solderKey := solderModStorageKey(pack.OwnerID, e.ModSlug, e.Version)
		var md5hex string
		var size int64

		switch e.Source {
		case "upload":
			// Uploaded content is already a stored Solder zip (its md5 is recorded).
			// Copy the identical bytes into the public solder/ key so the md5 is
			// unchanged and the launcher's differential cache stays valid.
			if e.StorageKey == "" || e.MD5 == "" {
				return fmt.Errorf("upload content %q missing storage key or md5", e.ModSlug)
			}
			bytesData, err := prov.Get(e.StorageKey)
			if err != nil {
				return fmt.Errorf("read stored content %q: %w", e.ModSlug, err)
			}
			if err := prov.Put(solderKey, bytesData); err != nil {
				return fmt.Errorf("publish content %q: %w", e.ModSlug, err)
			}
			md5hex = e.MD5
			size = e.Filesize

		case "modrinth":
			// Download the bare jar from the Modrinth CDN (hash-verified), wrap it
			// into a byte-stable Solder zip whose inner entry is the .minecraft-
			// relative TargetPath, store it, and hash the resulting zip.
			if e.ModrinthDownloadURL == "" || e.SHA1 == "" {
				return fmt.Errorf("modrinth content %q missing download URL or sha1", e.ModSlug)
			}
			jar, err := services.DownloadModrinthJar(ctx, e.ModrinthDownloadURL, e.SHA1, e.SHA512)
			if err != nil {
				return fmt.Errorf("download %q: %w", e.ModSlug, err)
			}
			innerPath := e.TargetPath
			if innerPath == "" {
				return fmt.Errorf("modrinth content %q missing target path", e.ModSlug)
			}
			zipBytes, err := modpack.BuildSolderContentZip(innerPath, jar)
			if err != nil {
				return fmt.Errorf("wrap %q: %w", e.ModSlug, err)
			}
			if err := prov.Put(solderKey, zipBytes); err != nil {
				return fmt.Errorf("publish content %q: %w", e.ModSlug, err)
			}
			md5hex, _, _ = modpack.Hashes(zipBytes)
			size = int64(len(zipBytes))

		default:
			return fmt.Errorf("content %q has unsupported source %q", e.ModSlug, e.Source)
		}

		mods = append(mods, solderMod{
			Name:      e.ModSlug,
			Version:   e.Version,
			MD5:       strings.ToLower(md5hex),
			SolderKey: solderKey,
			Filesize:  size,
		})
	}

	// Inject the loader as an ordinary mods[] entry (only for client-loader types).
	// vanilla/paper/empty => no loader entry. Forge/NeoForge hit the not-ready check
	// by design in Phase 3b (their artifacts are recorded 'failed' by Phase 3a).
	switch build.Loader {
	case "fabric", "quilt", "forge", "neoforge":
		l, err := h.state.Store.GetLoader(build.Minecraft, build.Loader, build.LoaderVersion)
		if err != nil {
			return fmt.Errorf("read loader: %w", err)
		}
		if l == nil || l.BuildStatus != models.LoaderStatusReady || l.ClientStorageKey == "" {
			return fmt.Errorf("loader not available for %s %s (build it first / Forge-NeoForge unsupported)", build.Loader, build.LoaderVersion)
		}
		loaderVersion := build.LoaderVersion
		if loaderVersion == "" {
			loaderVersion = l.LoaderVersion
		}
		// The loader stays at its loaders/... key; the mirror route serves that
		// prefix too, so no copy into solder/ is needed.
		mods = append(mods, solderMod{
			Name:      build.Loader,
			Version:   loaderVersion,
			MD5:       strings.ToLower(l.MD5),
			SolderKey: l.ClientStorageKey,
			Filesize:  l.Filesize,
		})
	default:
		// vanilla / paper / "" — no launcher-side loader artifact.
	}

	// Re-sort case-insensitively by Name to match Solder reference ordering (the
	// launcher does not require it, but it keeps the loader from always being last).
	sort.SliceStable(mods, func(i, j int) bool {
		return strings.ToLower(mods[i].Name) < strings.ToLower(mods[j].Name)
	})

	manifest := solderManifest{
		Minecraft: build.Minecraft,
		Java:      build.MinJava,
		Memory:    build.MinMemory,
		Mods:      mods,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if !safeSolderKeyComponent(build.VersionString) {
		return fmt.Errorf("build version %q has path characters not allowed in a Solder key", build.VersionString)
	}
	manifestKey := solderManifestKey(pack.OwnerID, pack.SolderSlug, build.VersionString)
	if err := prov.Put(manifestKey, manifestJSON); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	build.SolderPublished = true
	build.Frozen = true
	if err := h.state.Store.UpdatePackBuild(build); err != nil {
		return fmt.Errorf("mark build published: %w", err)
	}
	return nil
}

// PublishSolder renders + publishes a build to the public Solder API. Re-publishing
// a frozen build via THIS endpoint is allowed — the render is idempotent; only
// content-edit endpoints stay frozen-blocked.
func (h *PacksHandler) PublishSolder(w http.ResponseWriter, r *http.Request) {
	build, ok := h.loadOwnedBuild(r)
	if !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	pack, err := h.state.Store.GetPack(build.PackID)
	if err != nil || pack == nil {
		sendJSONError(w, "Pack not found", http.StatusNotFound)
		return
	}
	if pack.SolderSlug == "" {
		sendJSONError(w, "Set a Solder slug for this pack first", http.StatusBadRequest)
		return
	}
	if err := h.renderSolderBuild(pack, build); err != nil {
		// A not-available loader (incl. Forge/NeoForge) is user-actionable => 422.
		if strings.Contains(err.Error(), "loader not available") {
			sendJSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		log.Printf("PublishSolder render error (build %d): %v", build.ID, err)
		sendJSONError(w, "Failed to publish build", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "packs.changed", map[string]interface{}{"ownerId": pack.OwnerID})
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true, "slug": pack.SolderSlug, "build": build.VersionString,
	})
}

// solderConfigRequest is the PATCH body for the pack's Solder-facing settings.
type solderConfigRequest struct {
	SolderSlug        string `json:"solderSlug"`
	SolderDisplayName string `json:"solderDisplayName"`
	RecommendedBuild  string `json:"recommendedBuild"`
	LatestBuild       string `json:"latestBuild"`
	Private           bool   `json:"private"`
}

// SetSolderConfig sets the pack's public Solder identity + visibility. Slug is
// validated with the same rules as pack slugs (lowercase, non-empty, charset).
func (h *PacksHandler) SetSolderConfig(w http.ResponseWriter, r *http.Request) {
	pack, ok := h.ownsPack(r, atoiVar(r, "id"))
	if !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	// ownsPack lets admins read; a write must be the actual owner.
	userID, _ := r.Context().Value("userID").(string)
	if pack.OwnerID != userID {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	var req solderConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	slug := strings.TrimSpace(req.SolderSlug)
	if slug == "" {
		slug = slugify(req.SolderDisplayName)
	}
	if slug == "" || !packSlugRe.MatchString(slug) {
		sendJSONError(w, "Invalid solder slug", http.StatusBadRequest)
		return
	}
	pack.SolderSlug = slug
	pack.SolderDisplayName = strings.TrimSpace(req.SolderDisplayName)
	pack.RecommendedBuild = strings.TrimSpace(req.RecommendedBuild)
	pack.LatestBuild = strings.TrimSpace(req.LatestBuild)
	pack.Private = req.Private
	if err := h.state.Store.UpdatePack(pack); err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique") {
			sendJSONError(w, "Solder slug already taken", http.StatusConflict)
			return
		}
		sendJSONError(w, "Failed to update pack", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "packs.changed", map[string]interface{}{"ownerId": pack.OwnerID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "pack": pack})
}
