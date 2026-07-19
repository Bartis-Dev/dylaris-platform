package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"dylaris-core/models"
	"dylaris-core/services"
	"dylaris-core/storage/modpack"
)

// loaderBuildTimeout bounds an EnsureLoader build so a slow meta upstream cannot
// hold the request goroutine open indefinitely.
const loaderBuildTimeout = 45 * time.Second

// EnsureLoader makes sure a launcher-side loader artifact exists for the triple,
// DB-check-first. It is BEST-EFFORT: any failure is recorded in the loaders table
// (build_status='failed' + build_error) and swallowed — it never fails the caller's
// pack-build write. A build whose loader has no client-side loader (vanilla/paper)
// or an empty loader/loaderVersion is skipped.
func (h *PacksHandler) EnsureLoader(minecraft, loader, loaderVersion string) {
	// An empty loaderVersion is not resolved to latest-stable here (deferred to the
	// Phase 3b render, which needs it): with no pinned version there is nothing to
	// build+cache in 3a.
	if minecraft == "" || loader == "" || loaderVersion == "" {
		return
	}
	switch loader {
	case "fabric", "quilt", "forge", "neoforge":
		// buildable vocabulary (forge/neoforge record 'failed: deferred')
	default:
		return // vanilla / paper / unknown => no launcher loader artifact
	}

	// DB-check-first: a ready row for this exact triple is reused as-is.
	if existing, err := h.state.Store.GetLoader(minecraft, loader, loaderVersion); err == nil &&
		existing != nil && existing.BuildStatus == models.LoaderStatusReady && existing.ClientStorageKey != "" {
		return
	}

	// Mark pending (so a concurrent/observed state is 'pending', not missing).
	_, _ = h.state.Store.UpsertLoader(&models.Loader{
		Minecraft: minecraft, Loader: loader, LoaderVersion: loaderVersion,
		BuildStatus: models.LoaderStatusPending,
	})

	fail := func(msg string) {
		_ = h.state.Store.UpdateLoaderStatus(minecraft, loader, loaderVersion, models.LoaderStatusFailed, msg)
	}

	prov, err := modpack.NewProviderFromSettings(h.state.Store.GetSetting, h.state.buildCoreStorageProvider)
	if err != nil || prov == nil {
		fail("modpack storage not configured; cannot store loader artifact")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), loaderBuildTimeout)
	defer cancel()

	zipBytes, resolved, err := services.BuildLoaderArtifact(ctx, minecraft, loader, loaderVersion)
	if err != nil {
		if err == services.ErrLoaderDeferred {
			fail("Forge/NeoForge loader builds are not yet supported (coming in a follow-up sub-phase)")
			return
		}
		fail("loader build failed: " + err.Error())
		return
	}

	key := "loaders/" + loader + "/" + minecraft + "/" + resolved + "/loader.zip"
	if err := prov.Put(ctx, key, zipBytes); err != nil {
		fail("loader storage put failed: " + err.Error())
		return
	}
	md5hex, _, _ := modpack.Hashes(zipBytes)
	now := time.Now()
	// loaderVersion is non-empty (guarded above) so BuildLoaderArtifact returns
	// resolved == loaderVersion; persist the single READY row under it.
	_, _ = h.state.Store.UpsertLoader(&models.Loader{
		Minecraft: minecraft, Loader: loader, LoaderVersion: loaderVersion,
		ClientStorageKey: key, MD5: md5hex, Filesize: int64(len(zipBytes)),
		BuildStatus: models.LoaderStatusReady, BuiltAt: &now,
	})
}

// GetBuildLoader returns the cached loader row for a build's (minecraft, loader,
// loader_version) triple, so the panel / hand-test can see build status + md5.
func (h *PacksHandler) GetBuildLoader(w http.ResponseWriter, r *http.Request) {
	b, ok := h.loadOwnedBuild(r)
	if !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	l, err := h.state.Store.GetLoader(b.Minecraft, b.Loader, b.LoaderVersion)
	if err != nil {
		sendJSONError(w, "Failed to read loader", http.StatusInternalServerError)
		return
	}
	// nil => no loader row yet (e.g. vanilla build or not built). Return a shaped
	// "none" so the client can distinguish from an error.
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "loader": l})
}
