package handlers

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dylaris-core/models"
	"dylaris-core/storage/modpack"

	"github.com/gorilla/mux"
)

type mrpackIndexOut struct {
	FormatVersion int               `json:"formatVersion"`
	Game          string            `json:"game"`
	VersionID     string            `json:"versionId"`
	Name          string            `json:"name"`
	Summary       string            `json:"summary,omitempty"`
	Files         []mrpackFileOut   `json:"files"`
	Dependencies  map[string]string `json:"dependencies"`
}

type mrpackFileOut struct {
	Path      string            `json:"path"`
	Hashes    map[string]string `json:"hashes"`
	Env       map[string]string `json:"env,omitempty"`
	Downloads []string          `json:"downloads"`
	FileSize  int64             `json:"fileSize"`
}

// envForSide maps a build-content side to the mrpack per-file env block.
func envForSide(side string) map[string]string {
	switch side {
	case models.SideClient:
		return map[string]string{"client": "required", "server": "unsupported"}
	case models.SideServer:
		return map[string]string{"client": "unsupported", "server": "required"}
	default:
		return map[string]string{"client": "required", "server": "required"}
	}
}

// buildMrpackIndex builds modrinth.index.json. Modrinth-linked content (has a
// cdn.modrinth.com download + sha1+sha512) goes in files[]; everything else is
// embedded under overrides/ (returned separately for the zip writer).
func buildMrpackIndex(pack *models.Pack, build *models.PackBuild, content []models.BuildContentEntry) mrpackIndexOut {
	files := make([]mrpackFileOut, 0, len(content))
	for _, e := range content {
		// A file is a clean files[] reference only if it is Modrinth-linked AND
		// carries both hashes AND a Modrinth CDN download URL.
		dl := modrinthCDNURL(e)
		if e.Linked && e.SHA1 != "" && e.SHA512 != "" && dl != "" {
			files = append(files, mrpackFileOut{
				Path:      e.TargetPath,
				Hashes:    map[string]string{"sha1": e.SHA1, "sha512": e.SHA512},
				Env:       envForSide(e.Side),
				Downloads: []string{dl},
				FileSize:  e.Filesize,
			})
		}
		// Non-linked content is embedded via overrides/ in writeMrpackZip.
	}

	deps := map[string]string{"minecraft": build.Minecraft}
	switch strings.ToLower(build.Loader) {
	case "fabric":
		deps["fabric-loader"] = build.LoaderVersion
	case "quilt":
		deps["quilt-loader"] = build.LoaderVersion
	case "forge":
		deps["forge"] = build.LoaderVersion
	case "neoforge":
		deps["neoforge"] = build.LoaderVersion
	}

	summary := pack.Summary
	if build.Changelog != "" {
		summary = strings.TrimSpace(pack.Summary + "\n\n" + build.Changelog)
	}
	name := pack.SolderDisplayName
	if name == "" {
		name = pack.InternalName
	}
	return mrpackIndexOut{
		FormatVersion: 1,
		Game:          "minecraft",
		VersionID:     build.VersionString,
		Name:          name,
		Summary:       summary,
		Files:         files,
		Dependencies:  deps,
	}
}

// modrinthCDNURL returns the cdn.modrinth.com download URL for a linked entry,
// or "". It reads modrinth_download_url (set at add/replace time); url_override
// is reserved for the Solder mirror URL and is NOT used here. An entry with no
// cdn URL falls to overrides/ (still installs, just embedded not referenced).
func modrinthCDNURL(e models.BuildContentEntry) string {
	if strings.HasPrefix(e.ModrinthDownloadURL, "https://cdn.modrinth.com/") {
		return e.ModrinthDownloadURL
	}
	return ""
}

// overrideEntriesFromStoredZip reads a content entry's stored Solder zip and
// returns its inner files rekeyed under overrides/. A Solder zip already holds
// the file at its .minecraft-relative path (e.g. mods/x.jar), so we prefix
// "overrides/". Skips content that has no storage_key (pure Modrinth reference).
func (h *PacksHandler) overrideEntriesFromStoredZip(prov modpack.ModpackStorageProvider, e models.BuildContentEntry) (map[string][]byte, error) {
	out := map[string][]byte{}
	if e.StorageKey == "" {
		return out, nil
	}
	raw, err := prov.Get(e.StorageKey)
	if err != nil {
		return nil, fmt.Errorf("override read %s: %w", e.StorageKey, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("override unzip %s: %w", e.StorageKey, err)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		out["overrides/"+f.Name] = b
	}
	return out, nil
}

// writeMrpackZip writes modrinth.index.json + all overrides into zw.
func (h *PacksHandler) writeMrpackZip(zw *zip.Writer, pack *models.Pack, build *models.PackBuild, content []models.BuildContentEntry, prov modpack.ModpackStorageProvider) error {
	idx := buildMrpackIndex(pack, build, content)
	indexBytes, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	iw, err := zw.CreateHeader(&zip.FileHeader{Name: "modrinth.index.json", Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return err
	}
	if _, err := iw.Write(indexBytes); err != nil {
		return err
	}

	// Embed non-linked content under overrides/.
	for _, e := range content {
		if e.Linked && e.SHA1 != "" && e.SHA512 != "" && modrinthCDNURL(e) != "" {
			continue // already a files[] reference
		}
		if prov == nil {
			continue // no storage configured; skip embedding
		}
		entries, err := h.overrideEntriesFromStoredZip(prov, e)
		if err != nil {
			return err
		}
		for name, b := range entries {
			ew, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
			if err != nil {
				return err
			}
			if _, err := ew.Write(b); err != nil {
				return err
			}
		}
	}
	return nil
}

// renderMrpack returns the full .mrpack bytes for a build.
func (h *PacksHandler) renderMrpack(pack *models.Pack, build *models.PackBuild, content []models.BuildContentEntry) ([]byte, error) {
	prov, _ := modpack.NewProviderFromSettings(h.state.Store.GetSetting)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := h.writeMrpackZip(zw, pack, build, content, prov); err != nil {
		zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// persistMrpackForBuild renders + (for beta/release) persists the mrpack to
// storage and freezes the build. Drafts are rendered fresh, never persisted.
func (h *PacksHandler) persistMrpackForBuild(pack *models.Pack, build *models.PackBuild, content []models.BuildContentEntry) ([]byte, error) {
	data, err := h.renderMrpack(pack, build, content)
	if err != nil {
		return nil, err
	}
	if build.Channel == models.ChannelDraft {
		return data, nil
	}
	prov, err := modpack.NewProviderFromSettings(h.state.Store.GetSetting)
	if err != nil {
		return nil, fmt.Errorf("modpack storage misconfigured: %w", err)
	}
	if prov == nil {
		return nil, fmt.Errorf("no modpack storage configured (Settings -> Modpacks)")
	}
	key := fmt.Sprintf("modpacks/%s/%s/%s/pack.mrpack", pack.OwnerID, pack.InternalSlug, build.VersionString)
	if err := prov.Put(key, data); err != nil {
		return nil, fmt.Errorf("mrpack storage put: %w", err)
	}
	sum := sha256.Sum256(data)
	build.MrpackStorageKey = key
	build.MrpackSHA256 = hex.EncodeToString(sum[:])
	build.Frozen = true
	if err := h.state.Store.UpdatePackBuild(build); err != nil {
		return nil, fmt.Errorf("mrpack persisted but stamp failed: %w", err)
	}
	return data, nil
}

// ExportMrpack streams the .mrpack for a build (self-distributed download).
func (h *PacksHandler) ExportMrpack(w http.ResponseWriter, r *http.Request) {
	packID, _ := strconv.Atoi(mux.Vars(r)["id"])
	p, ok := h.ownsPack(r, packID)
	if !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	buildID, _ := strconv.Atoi(mux.Vars(r)["buildId"])
	build, err := h.state.Store.GetPackBuild(buildID)
	if err != nil || build == nil || build.PackID != packID {
		sendJSONError(w, "Build not found", http.StatusNotFound)
		return
	}
	content, err := h.state.Store.ListBuildContent(buildID)
	if err != nil {
		sendJSONError(w, "Failed to load content", http.StatusInternalServerError)
		return
	}
	data, err := h.renderMrpack(p, build, content)
	if err != nil {
		sendJSONError(w, "Failed to build .mrpack: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-modrinth-modpack+zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", p.InternalSlug+"-"+build.VersionString+".mrpack"))
	_, _ = w.Write(data)
}
