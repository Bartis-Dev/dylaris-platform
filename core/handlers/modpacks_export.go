package handlers

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dylaris-core/models"

	"github.com/gorilla/mux"
)

// Phase 14.2 — .mrpack export. Streams a Modrinth-format archive (zip with
// modrinth.index.json) built from the modpack_mods rows of a version. The
// archive content is generated on the fly so we never have to write the
// .mrpack to disk — the same byte stream feeds the Modrinth publish call
// in P14.3.

// mrpackIndexOut mirrors Modrinth's modrinth.index.json schema. We write
// only the keys Modrinth's spec marks as required + the ones the launcher
// uses for client/server filtering.
type mrpackIndexOut struct {
	FormatVersion int                 `json:"formatVersion"`
	Game          string              `json:"game"`
	VersionID     string              `json:"versionId"`
	Name          string              `json:"name"`
	Summary       string              `json:"summary,omitempty"`
	Files         []mrpackFileOut     `json:"files"`
	Dependencies  map[string]string   `json:"dependencies"`
}

type mrpackFileOut struct {
	Path      string            `json:"path"`
	Hashes    map[string]string `json:"hashes"`
	Env       map[string]string `json:"env"`
	Downloads []string          `json:"downloads"`
	FileSize  int64             `json:"fileSize"`
}

// ExportMrpack GET /api/modpacks/{id}/versions/{versionId}/mrpack
//
// Returns: application/x-modrinth-modpack+zip stream. No body. Auth via
// session JWT (cookie or query token); ownership enforced via ownsModpack.
func (h *ModpacksHandler) ExportMrpack(w http.ResponseWriter, r *http.Request) {
	modpackID, _ := strconv.Atoi(mux.Vars(r)["id"])
	versionID, _ := strconv.Atoi(mux.Vars(r)["versionId"])
	pack, ok := h.ownsModpack(r, modpackID)
	if !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	version, err := h.state.Store.GetModpackVersion(versionID)
	if err != nil || version.ModpackID != modpackID {
		sendJSONError(w, "Version not found", http.StatusNotFound)
		return
	}
	mods, err := h.state.Store.ListModpackMods(versionID)
	if err != nil {
		sendJSONError(w, "Failed to load mods", http.StatusInternalServerError)
		return
	}

	idx := buildMrpackIndex(pack, version, mods)
	indexBytes, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		sendJSONError(w, "Failed to encode index", http.StatusInternalServerError)
		return
	}

	// Set headers BEFORE writing anything to the response.
	filename := fmt.Sprintf("%s-%s.mrpack", pack.Slug, version.VersionString)
	w.Header().Set("Content-Type", "application/x-modrinth-modpack+zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeAttachmentName(filename)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")

	zw := zip.NewWriter(w)
	defer zw.Close()

	indexEntry, err := zw.CreateHeader(&zip.FileHeader{
		Name:     "modrinth.index.json",
		Method:   zip.Deflate,
		Modified: time.Now(),
	})
	if err != nil {
		return
	}
	if _, err := indexEntry.Write(indexBytes); err != nil {
		return
	}

	// Per the .mrpack spec, overrides/ is optional. V1 we don't persist
	// user-supplied config files yet so the zip contains only the index.
	// P14.3 will add overrides storage + include them here.
}

func buildMrpackIndex(pack *models.Modpack, version *models.ModpackVersion, mods []models.ModpackMod) mrpackIndexOut {
	files := make([]mrpackFileOut, 0, len(mods))
	for _, m := range mods {
		env := envForSide(m.Side, m.Required)
		hashes := map[string]string{}
		if m.SHA512 != "" {
			hashes["sha512"] = m.SHA512
		}
		// Modrinth places loader-mod jars under mods/. For plugins we'd
		// use the overrides/plugins/ pattern, but Modrinth modpack spec
		// only supports the loader-mod side of MC; plugin packs aren't a
		// thing in this format. We stick to mods/.
		filePath := "mods/" + sanitizePath(m.FileName)
		files = append(files, mrpackFileOut{
			Path:      filePath,
			Hashes:    hashes,
			Env:       env,
			Downloads: []string{m.DownloadURL},
			FileSize:  0, // size unknown unless we re-fetch; Modrinth tolerates 0
		})
	}

	deps := map[string]string{}
	if pack.McVersion != "" {
		deps["minecraft"] = pack.McVersion
	}
	if pack.Loader != "" {
		// The mrpack spec keys are "fabric-loader" / "quilt-loader" /
		// "forge" / "neoforge". Map our loader names to those.
		switch strings.ToLower(pack.Loader) {
		case "fabric":
			deps["fabric-loader"] = ""
		case "quilt":
			deps["quilt-loader"] = ""
		case "forge":
			deps["forge"] = ""
		case "neoforge":
			deps["neoforge"] = ""
		}
	}

	summary := pack.Summary
	if version.Changelog != "" {
		summary = pack.Summary + "\n\n" + version.Changelog
	}

	return mrpackIndexOut{
		FormatVersion: 1,
		Game:          "minecraft",
		VersionID:     version.VersionString,
		Name:          pack.Name,
		Summary:       summary,
		Files:         files,
		Dependencies:  deps,
	}
}

// envForSide maps our DB side+required pair to the per-file env block the
// mrpack format wants: { client: "required"|"optional"|"unsupported",
// server: same }. Both-side mods are required everywhere; client-only mods
// don't go to the server; server-only mods don't go to the client.
func envForSide(side string, required bool) map[string]string {
	missing := "unsupported"
	val := "required"
	if !required {
		val = "optional"
	}
	switch side {
	case "client":
		return map[string]string{"client": val, "server": missing}
	case "server":
		return map[string]string{"client": missing, "server": val}
	default:
		return map[string]string{"client": val, "server": val}
	}
}

// sanitizePath strips path separators from a file name so a malicious
// stored fileName can't escape the mods/ prefix at extract time.
func sanitizePath(s string) string {
	s = strings.ReplaceAll(s, "..", "")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}

// sanitizeAttachmentName keeps the filename header from carrying CRLF /
// quotes that browsers would refuse.
func sanitizeAttachmentName(s string) string {
	s = strings.ReplaceAll(s, `"`, "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}
