package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"dylaris-core/models"
	"dylaris-core/services"
	"dylaris-core/storage/modpack"
)

// Import bounds. A single import is capped in builds, mods per build, per-file
// bytes, and total bytes so one request cannot exhaust storage or memory.
const (
	solderImportMaxBuilds       = 25
	solderImportMaxModsPerBuild = 500
	solderImportMaxModBytes     = 512 << 20 // 512 MiB per mod zip
	solderImportMaxTotalBytes   = 4 << 30   // 4 GiB per import
	solderImportDownloadTimeout = 300 * time.Second
)

type solderImportRequest struct {
	URL  string `json:"url"`
	Slug string `json:"slug"`
}

type solderImportPreviewRow struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// solderImportReport accumulates what happened so the response can tell the
// user how much came in and what was skipped (skips are non-fatal).
type solderImportReport struct {
	Builds   int
	Imported int
	Skipped  []string
}

// ImportSolderPreview reads the pack index of an external Solder instance so
// the UI can list what is importable. No content is downloaded.
func (h *PacksHandler) ImportSolderPreview(w http.ResponseWriter, r *http.Request) {
	var req solderImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.URL) == "" {
		sendJSONError(w, "url required", http.StatusBadRequest)
		return
	}
	idx, err := services.FetchSolderIndex(r.Context(), req.URL)
	if err != nil {
		sendJSONError(w, "Could not read Solder instance: "+err.Error(), http.StatusBadGateway)
		return
	}
	rows := make([]solderImportPreviewRow, 0, len(idx))
	for slug, name := range idx {
		rows = append(rows, solderImportPreviewRow{Slug: slug, Name: name})
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].Slug) < strings.ToLower(rows[j].Slug)
	})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "packs": rows})
}

// ImportSolder imports one modpack (all its builds) from an external Solder
// instance into a new draft pack owned by the caller. Each mod zip is pulled
// through the SSRF-safe fetcher, stored, hash-linked to Modrinth where the
// inner jar matches, and deduped against the owner's catalog by sha1. Per-mod
// and per-build failures are non-fatal and recorded in the report.
func (h *PacksHandler) ImportSolder(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var req solderImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		strings.TrimSpace(req.URL) == "" || strings.TrimSpace(req.Slug) == "" {
		sendJSONError(w, "url and slug required", http.StatusBadRequest)
		return
	}

	prov, err := modpack.NewProviderFromSettings(h.state.Store.GetSetting)
	if err != nil || prov == nil {
		sendJSONError(w, "No pack storage configured (Settings -> Modpacks)", http.StatusFailedDependency)
		return
	}

	mp, err := services.FetchSolderModpack(r.Context(), req.URL, req.Slug)
	if err != nil {
		sendJSONError(w, "Could not read modpack: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Create the pack under a per-owner-unique internal slug.
	name := strings.TrimSpace(mp.DisplayName)
	if name == "" {
		name = strings.TrimSpace(req.Slug)
	}
	internalSlug, err := h.uniqueInternalSlug(userID, slugify(name))
	if err != nil {
		sendJSONError(w, "Failed to allocate pack slug", http.StatusInternalServerError)
		return
	}
	packID, err := h.state.Store.CreatePack(&models.Pack{
		OwnerID:           userID,
		InternalName:      name,
		InternalSlug:      internalSlug,
		SolderDisplayName: name,
	})
	if err != nil {
		sendJSONError(w, "Failed to create pack", http.StatusInternalServerError)
		return
	}

	builds := mp.Builds
	if len(builds) > solderImportMaxBuilds {
		builds = builds[:solderImportMaxBuilds]
	}

	var totalBytes int64
	newBySHA1 := map[string]int{} // inner-jar sha1 -> modversion id created this run
	report := solderImportReport{}

importLoop:
	for _, bv := range builds {
		bd, err := services.FetchSolderBuild(r.Context(), req.URL, req.Slug, bv)
		if err != nil {
			report.Skipped = append(report.Skipped, "build "+bv+": "+err.Error())
			continue
		}
		loader, loaderVer := "", strings.TrimSpace(bd.Forge)
		if loaderVer != "" {
			loader = "forge"
		}
		buildID, err := h.state.Store.CreatePackBuild(&models.PackBuild{
			PackID:        packID,
			VersionString: bv,
			Minecraft:     bd.Minecraft,
			Loader:        loader,
			LoaderVersion: loaderVer,
			MinJava:       bd.Java,
			MinMemory:     bd.Memory,
			Channel:       models.ChannelDraft,
		})
		if err != nil {
			report.Skipped = append(report.Skipped, "build "+bv+": create failed")
			continue
		}
		report.Builds++
		attached := map[int]bool{}

		mods := bd.Mods
		if len(mods) > solderImportMaxModsPerBuild {
			mods = mods[:solderImportMaxModsPerBuild]
			report.Skipped = append(report.Skipped,
				"build "+bv+": mod list truncated to "+strconv.Itoa(solderImportMaxModsPerBuild))
		}
		for _, m := range mods {
			if totalBytes >= solderImportMaxTotalBytes {
				report.Skipped = append(report.Skipped, "import size cap reached; remaining mods skipped")
				break importLoop
			}
			mvID, n, err := h.importOneSolderMod(r.Context(), prov, userID, m, newBySHA1)
			if err != nil {
				report.Skipped = append(report.Skipped, "mod "+m.Name+": "+err.Error())
				continue
			}
			totalBytes += n
			if _, err := h.state.Store.AttachModversionToBuild(buildID, mvID, models.SideBoth); err != nil {
				report.Skipped = append(report.Skipped, "mod "+m.Name+": attach failed")
				continue
			}
			if !attached[mvID] {
				attached[mvID] = true
				report.Imported++
			}
		}
	}

	if report.Builds == 0 && report.Imported == 0 {
		_ = h.state.Store.DeletePack(packID, userID)
		sendJSONError(w, "Nothing could be imported from that modpack", http.StatusBadGateway)
		return
	}

	h.state.Events.Publish(r.Context(), "packs.changed", map[string]interface{}{"ownerId": userID})
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"packId":   packID,
		"builds":   report.Builds,
		"imported": report.Imported,
		"skipped":  report.Skipped,
	})
}

// importOneSolderMod downloads, stores (unless already in the owner's catalog),
// hash-links, and returns the modversion id for one Solder mod entry. It also
// returns the number of downloaded bytes so the caller can enforce the total
// cap. Dedup: an inner jar whose sha1 already exists (this run, or the owner's
// catalog) reuses that modversion and skips the storage write.
func (h *PacksHandler) importOneSolderMod(ctx context.Context, prov modpack.ModpackStorageProvider, ownerID string, m services.SolderMod, newBySHA1 map[string]int) (int, int64, error) {
	if strings.TrimSpace(m.URL) == "" {
		return 0, 0, fmt.Errorf("no url")
	}
	zipBytes, err := services.SafeFetch(ctx, m.URL, solderImportMaxModBytes, solderImportDownloadTimeout)
	if err != nil {
		return 0, 0, err
	}
	n := int64(len(zipBytes))
	if modpack.HasUnsafeZipEntry(zipBytes) {
		return 0, 0, fmt.Errorf("unsafe zip entry path")
	}
	// Store-time defense in depth (BC2 bundled minor): an external Solder
	// instance is not a trusted source; cap declared per-entry size before
	// persisting, same as the local upload path.
	if hasOversizedZipEntry(zipBytes) {
		return 0, 0, fmt.Errorf("zip entry exceeds the size cap")
	}
	md5hex, _, _ := modpack.Hashes(zipBytes)

	innerName, innerJar, hasJar := modpack.FirstInnerJar(zipBytes)
	var innerSha1, innerSha512 string
	if hasJar {
		_, innerSha1, innerSha512 = modpack.Hashes(innerJar)
	}

	// Dedup by inner-jar sha1: reuse a modversion already made this run or
	// already in the owner's catalog, so a mod shared across builds is stored
	// and hashed once.
	if innerSha1 != "" {
		if id, ok := newBySHA1[innerSha1]; ok {
			return id, n, nil
		}
		if existing, _ := h.state.Store.FindModversionBySHA1(ownerID, innerSha1); existing != nil {
			return existing.ID, n, nil
		}
	}

	slug := slugify(m.Name)
	if slug == "" {
		slug = "mod"
	}
	version := strings.TrimSpace(m.Version)
	// The storage key must never contain raw remote text (path-traversal / key
	// injection). Sanitize for the key and make it unique via the zip md5; keep
	// the raw version only for the display field below.
	keyVersion := slugify(version)
	if keyVersion == "" {
		keyVersion = "s"
	}
	keyVersion = keyVersion + "-" + md5hex[:8]
	if version == "" {
		version = keyVersion
	}
	key := "packs/" + ownerID + "/mods/" + slug + "/" + slug + "-" + keyVersion + ".zip"
	if err := prov.Put(key, zipBytes); err != nil {
		return 0, 0, fmt.Errorf("storage put failed")
	}

	fileName := innerName
	if fileName == "" {
		fileName = slug + ".jar"
	}
	modID, err := h.state.Store.UpsertMod(&models.Mod{
		OwnerID: ownerID, Slug: slug, PrettyName: m.Name, ContentType: models.ContentTypeMod,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("save mod failed")
	}
	mv := &models.Modversion{
		ModID:      modID,
		Version:    version,
		Filesize:   n,
		StorageKey: key,
		MD5:        md5hex,
		SHA1:       innerSha1,
		SHA512:     innerSha512,
		Source:     models.SourceUpload,
		TargetPath: targetPathFor(models.ContentTypeMod, fileName),
	}
	// Auto-link: does this jar's sha1 match a Modrinth project?
	if innerSha1 != "" {
		if v := services.ModrinthByHash(innerSha1); v != nil {
			mv.Source = models.SourceModrinth
			mv.ModrinthProjectID = v.ProjectID
			mv.ModrinthVersionID = v.ID
			mv.ModrinthVersionNumber = v.VersionNum
			mv.ModrinthGameVersions = strings.Join(v.GameVersions, ",")
			mv.ModrinthDownloadURL = v.PrimaryFile().URL
		}
	}
	mvID, err := h.state.Store.CreateModversion(mv)
	if err != nil {
		return 0, 0, fmt.Errorf("save version failed")
	}
	if innerSha1 != "" {
		newBySHA1[innerSha1] = mvID
	}
	return mvID, n, nil
}

// uniqueInternalSlug returns base, or base-2, base-3... - the first slug not
// already used by one of the owner's packs (internal_slug is UNIQUE per owner).
func (h *PacksHandler) uniqueInternalSlug(ownerID, base string) (string, error) {
	if base == "" {
		base = "imported-pack"
	}
	packs, err := h.state.Store.ListPacksByOwner(ownerID)
	if err != nil {
		return "", err
	}
	used := map[string]bool{}
	for _, p := range packs {
		used[p.InternalSlug] = true
	}
	if !used[base] {
		return base, nil
	}
	for i := 2; i < 1000; i++ {
		cand := base + "-" + strconv.Itoa(i)
		if !used[cand] {
			return cand, nil
		}
	}
	return "", fmt.Errorf("no free slug")
}
