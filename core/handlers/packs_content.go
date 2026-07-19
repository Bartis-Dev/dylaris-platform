package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"dylaris-core/models"
	"dylaris-core/services"
	"dylaris-core/storage/modpack"

	"github.com/gorilla/mux"
)

// loadOwnedBuild returns the build if the caller owns its pack. Frozen state is
// left for callers to gate on (list/read is allowed on frozen builds; writes
// are not).
func (h *PacksHandler) loadOwnedBuild(r *http.Request) (*models.PackBuild, bool) {
	packID, _ := strconv.Atoi(mux.Vars(r)["id"])
	buildID, _ := strconv.Atoi(mux.Vars(r)["buildId"])
	userID, _ := r.Context().Value("userID").(string)
	p, err := h.state.Store.GetPack(packID)
	if err != nil || p == nil || p.OwnerID != userID {
		return nil, false
	}
	b, err := h.state.Store.GetPackBuild(buildID)
	if err != nil || b == nil || b.PackID != packID {
		return nil, false
	}
	return b, true
}

func (h *PacksHandler) ListContent(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.ownsPack(r, atoiVar(r, "id")); !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	buildID, _ := strconv.Atoi(mux.Vars(r)["buildId"])
	entries, err := h.state.Store.ListBuildContent(buildID)
	if err != nil {
		sendJSONError(w, "Failed to list content", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "content": entries})
}

type addModrinthRequest struct {
	ProjectID   string `json:"projectId"`
	VersionID   string `json:"versionId"`
	Side        string `json:"side"`
	ResolveDeps bool   `json:"resolveDeps"`
	ContentType string `json:"contentType"` // mod|resourcepack|shaderpack
}

func (h *PacksHandler) AddModrinth(w http.ResponseWriter, r *http.Request) {
	b, ok := h.loadOwnedBuild(r)
	if !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	if b.Frozen {
		sendJSONError(w, "Build is frozen", http.StatusConflict)
		return
	}
	var req addModrinthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.VersionID == "" {
		sendJSONError(w, "versionId required", http.StatusBadRequest)
		return
	}
	userID, _ := r.Context().Value("userID").(string)

	v, err := services.FetchModrinthVersion(req.VersionID)
	if err != nil {
		sendJSONError(w, "Modrinth version lookup failed", http.StatusBadGateway)
		return
	}
	if err := h.addModrinthVersion(userID, b, v, req.Side, req.ContentType); err != nil {
		sendJSONError(w, "Failed to add mod", http.StatusInternalServerError)
		return
	}

	added := 1
	if req.ResolveDeps {
		have := map[string]bool{v.ProjectID: true}
		deps, _ := services.ResolveModrinthDependencies(v, b.Minecraft, b.Loader, have)
		for _, d := range deps {
			if err := h.addModrinthVersion(userID, b, d.Version, models.SideBoth, models.ContentTypeMod); err == nil {
				added++
			}
		}
	}
	h.state.Events.Publish(r.Context(), "pack_content.changed", map[string]interface{}{"buildId": b.ID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "added": added})
}

// addModrinthVersion upserts the mod catalog row + a linked modversion and
// attaches it to the build. The Solder wrapped-zip is materialized lazily on
// publish (Phase 3); Phase 1 stores the Modrinth reference only.
func (h *PacksHandler) addModrinthVersion(ownerID string, b *models.PackBuild, v *services.ModrinthVersion, side, contentType string) error {
	if contentType == "" {
		contentType = models.ContentTypeMod
	}
	slug := slugify(v.ProjectSlugOrID())
	modID, err := h.state.Store.UpsertMod(&models.Mod{
		OwnerID: ownerID, Slug: slug, PrettyName: v.Name, ContentType: contentType,
	})
	if err != nil {
		return err
	}
	file := v.PrimaryFile()
	mvID, err := h.state.Store.CreateModversion(&models.Modversion{
		ModID:                 modID,
		Version:               v.VersionNum,
		Filesize:              file.Size,
		SHA1:                  file.Hashes["sha1"],
		SHA512:                file.Hashes["sha512"],
		URLOverride:           "",
		ModrinthDownloadURL:   file.URL,
		Source:                models.SourceModrinth,
		TargetPath:            targetPathFor(contentType, file.Filename),
		ModrinthProjectID:     v.ProjectID,
		ModrinthVersionID:     v.ID,
		ModrinthVersionNumber: v.VersionNum,
		ModrinthGameVersions:  strings.Join(v.GameVersions, ","),
	})
	if err != nil {
		return err
	}
	if side == "" {
		side = sideFromModrinthDefault()
	}
	_, err = h.state.Store.AttachModversionToBuild(b.ID, mvID, side)
	return err
}

func (h *PacksHandler) UploadContent(w http.ResponseWriter, r *http.Request) {
	b, ok := h.loadOwnedBuild(r)
	if !ok || b.Frozen {
		sendJSONError(w, "Forbidden or frozen", http.StatusForbidden)
		return
	}
	userID, _ := r.Context().Value("userID").(string)

	// multipart: field "file", plus form fields side + contentType
	if err := r.ParseMultipartForm(512 << 20); err != nil { // 512 MiB
		sendJSONError(w, "Upload too large or malformed", http.StatusBadRequest)
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		sendJSONError(w, "file field required", http.StatusBadRequest)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		sendJSONError(w, "Failed to read upload", http.StatusInternalServerError)
		return
	}
	side := r.FormValue("side")
	if side == "" {
		side = models.SideBoth
	}
	contentType := r.FormValue("contentType")
	if contentType == "" {
		contentType = models.ContentTypeMod
	}
	// Sanitize the client-supplied filename: normalize backslashes, take the
	// base name, and reject traversal so it cannot escape the zip/target path.
	fileName := path.Base(strings.ReplaceAll(hdr.Filename, `\`, "/"))
	if fileName == "." || fileName == "/" || fileName == "" || strings.Contains(fileName, "..") {
		sendJSONError(w, "Invalid file name", http.StatusBadRequest)
		return
	}

	// A raw .jar is wrapped into a Solder zip (mods/<file>.jar); an already-
	// Solder-format .zip is stored as-is; any other single raw file (e.g. a
	// config text file) is wrapped at its in-.minecraft target path so the
	// stored artifact is always a valid Solder zip.
	var zipBytes []byte
	switch {
	case strings.HasSuffix(strings.ToLower(fileName), ".jar"):
		zipBytes, err = modpack.WrapJarAsSolderZip(fileName, data)
		if err != nil {
			sendJSONError(w, "Failed to wrap jar", http.StatusInternalServerError)
			return
		}
	case strings.HasSuffix(strings.ToLower(fileName), ".zip") && contentType == models.ContentTypeMod:
		// A pre-structured Solder-format bundle (config/, resourcepacks/, ...):
		// store as-is, but reject traversal-bearing entries since the render
		// re-serves these bytes verbatim.
		if modpack.HasUnsafeZipEntry(data) {
			sendJSONError(w, "Zip contains unsafe entry paths", http.StatusBadRequest)
			return
		}
		// Store-time defense in depth (BC2 bundled minor): reject a per-entry
		// declared size over the render cap so a decompression bomb is never
		// persisted, on top of the render-time cap in packs_mrpack.go /
		// packs_serverpack.go.
		if hasOversizedZipEntry(data) {
			sendJSONError(w, "Zip entry exceeds the size cap", http.StatusBadRequest)
			return
		}
		zipBytes = data
	default:
		// A resourcepack/shaderpack/config file (incl. a raw .zip resourcepack
		// or a raw config text file): wrap at its target path so it lands in the
		// right in-.minecraft folder instead of the instance root.
		zipBytes, err = modpack.BuildSolderContentZip(targetPathFor(contentType, fileName), data)
		if err != nil {
			sendJSONError(w, "Failed to wrap file", http.StatusInternalServerError)
			return
		}
	}
	md5hex, sha1hex, _ := modpack.Hashes(zipBytes)
	// sha1 of the inner jar drives Modrinth auto-link; for a wrapped jar hash the jar itself.
	_, innerSha1, innerSha512 := modpack.Hashes(data)

	prov, err := modpack.NewProviderFromSettings(h.state.Store.GetSetting, h.state.buildCoreStorageProvider)
	if err != nil || prov == nil {
		sendJSONError(w, "No pack storage configured (Settings -> Modpacks)", http.StatusFailedDependency)
		return
	}
	slug := slugify(strings.TrimSuffix(fileName, ".jar"))
	version := "u-" + sha1hex[:8]
	key := "packs/" + userID + "/mods/" + slug + "/" + slug + "-" + version + ".zip"
	if err := prov.Put(r.Context(), key, zipBytes); err != nil {
		sendJSONError(w, "Storage put failed", http.StatusInternalServerError)
		return
	}

	modID, err := h.state.Store.UpsertMod(&models.Mod{
		OwnerID: userID, Slug: slug, PrettyName: fileName, ContentType: contentType,
	})
	if err != nil {
		sendJSONError(w, "Failed to save mod", http.StatusInternalServerError)
		return
	}
	mv := &models.Modversion{
		ModID:      modID,
		Version:    version,
		Filesize:   int64(len(zipBytes)),
		StorageKey: key,
		MD5:        md5hex,
		SHA1:       innerSha1,
		SHA512:     innerSha512,
		Source:     models.SourceUpload,
		TargetPath: targetPathFor(contentType, fileName),
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
		sendJSONError(w, "Failed to save version", http.StatusInternalServerError)
		return
	}
	if _, err := h.state.Store.AttachModversionToBuild(b.ID, mvID, side); err != nil {
		sendJSONError(w, "Failed to attach", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "pack_content.changed", map[string]interface{}{"buildId": b.ID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "modversionId": mvID, "linked": mv.ModrinthProjectID != ""})
}

func (h *PacksHandler) RemoveContent(w http.ResponseWriter, r *http.Request) {
	b, ok := h.loadOwnedBuild(r)
	if !ok || b.Frozen {
		sendJSONError(w, "Forbidden or frozen", http.StatusForbidden)
		return
	}
	mvID, _ := strconv.Atoi(mux.Vars(r)["modversionId"])
	if err := h.state.Store.DetachFromBuild(b.ID, mvID); err != nil {
		sendJSONError(w, "Failed to remove", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "pack_content.changed", map[string]interface{}{"buildId": b.ID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

type setSideRequest struct {
	Side string `json:"side"`
}

func (h *PacksHandler) SetSide(w http.ResponseWriter, r *http.Request) {
	b, ok := h.loadOwnedBuild(r)
	if !ok || b.Frozen {
		sendJSONError(w, "Forbidden or frozen", http.StatusForbidden)
		return
	}
	mvID, _ := strconv.Atoi(mux.Vars(r)["modversionId"])
	var req setSideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Side != models.SideClient && req.Side != models.SideServer && req.Side != models.SideBoth {
		sendJSONError(w, "Invalid side", http.StatusBadRequest)
		return
	}
	// AttachModversionToBuild is an upsert: without this check a caller could pass
	// another tenant's modversionId and pull it into their own build.
	if ok, _ := h.state.Store.IsModversionInBuild(b.ID, mvID); !ok {
		sendJSONError(w, "Content not found", http.StatusNotFound)
		return
	}
	if _, err := h.state.Store.AttachModversionToBuild(b.ID, mvID, req.Side); err != nil {
		sendJSONError(w, "Failed to set side", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "pack_content.changed", map[string]interface{}{"buildId": b.ID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// --- helpers ---

func atoiVar(r *http.Request, key string) int { n, _ := strconv.Atoi(mux.Vars(r)[key]); return n }

// targetPathFor derives the in-.minecraft path for a content type.
func targetPathFor(contentType, fileName string) string {
	switch contentType {
	case models.ContentTypeResourcepack:
		return "resourcepacks/" + fileName
	case models.ContentTypeShaderpack:
		return "shaderpacks/" + fileName
	case models.ContentTypeConfig:
		return "config/" + fileName
	default:
		return "mods/" + fileName
	}
}

func sideFromModrinthDefault() string { return models.SideBoth }
