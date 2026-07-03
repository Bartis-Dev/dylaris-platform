package handlers

import (
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dylaris-core/models"
	"dylaris-core/services"
	"dylaris-core/storage/modpack"

	"github.com/gorilla/mux"
)

// replaceDownloadClient bounds the cdn.modrinth.com file download so a stalled
// connection cannot hang the handler goroutine indefinitely.
var replaceDownloadClient = &http.Client{Timeout: 120 * time.Second}

type replaceModrinthRequest struct {
	VersionID string `json:"versionId"` // the Modrinth version to align to
}

// ReplaceWithModrinth swaps a stored content artifact for Modrinth's exact file:
// download the chosen Modrinth version's primary file, verify its bytes against
// Modrinth's published hashes, re-wrap + store it, delete the old storage object,
// and rewrite the modversion so it becomes byte-identical to Modrinth (clean
// files[] reference + auto-update eligible).
func (h *PacksHandler) ReplaceWithModrinth(w http.ResponseWriter, r *http.Request) {
	build, ok := h.loadOwnedBuild(r)
	if !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	// loadOwnedBuild does not gate frozen; replacing content on a frozen build
	// would break byte-identity of an already-published build, so reject here.
	if build.Frozen {
		sendJSONError(w, "Build is frozen", http.StatusConflict)
		return
	}
	mvID, _ := strconv.Atoi(mux.Vars(r)["modversionId"])
	mv, err := h.state.Store.GetModversion(mvID)
	if err != nil || mv == nil {
		sendJSONError(w, "Content not found", http.StatusNotFound)
		return
	}
	// GetModversion is a globally unscoped by-id lookup; loadOwnedBuild only proves
	// pack/build ownership, not that this modversion belongs to it. Without this
	// check a caller could pass another tenant's modversionId and repoint their
	// storage_key/hashes to this caller's storage path.
	if ok, _ := h.state.Store.IsModversionInBuild(build.ID, mvID); !ok {
		sendJSONError(w, "Content not found", http.StatusNotFound)
		return
	}
	var req replaceModrinthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.VersionID == "" {
		sendJSONError(w, "versionId required", http.StatusBadRequest)
		return
	}
	userID, _ := r.Context().Value("userID").(string)
	if err := h.swapModversionToModrinth(r.Context(), userID, mv, req.VersionID); err != nil {
		sendJSONError(w, err.Error(), http.StatusBadGateway)
		return
	}
	h.state.Events.Publish(r.Context(), "pack_content.changed", map[string]interface{}{"buildId": build.ID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "modversionId": mv.ID, "linked": true})
}

// swapModversionToModrinth aligns a modversion to a specific Modrinth version:
// fetch the version, download + integrity-verify its primary file against
// Modrinth's published hashes, re-wrap + store under a fresh key, rewrite the
// modversion refs, then delete the old object (safe-cutover ordering). Shared by
// ReplaceWithModrinth and UpdateMods. Sets modrinth_latest_version_id to the
// aligned version and stamps modrinth_last_checked so "update available" clears.
func (h *PacksHandler) swapModversionToModrinth(ctx context.Context, ownerID string, mv *models.Modversion, versionID string) error {
	v, err := services.FetchModrinthVersion(versionID)
	if err != nil {
		return fmt.Errorf("modrinth version lookup failed: %w", err)
	}
	file := v.PrimaryFile()
	if file.URL == "" {
		return fmt.Errorf("modrinth version has no downloadable file")
	}
	jar, err := downloadURL(ctx, file.URL)
	if err != nil {
		return fmt.Errorf("download from modrinth failed: %w", err)
	}
	if err := verifyModrinthBytes(jar, file.Hashes); err != nil {
		return fmt.Errorf("modrinth download integrity check failed: %w", err)
	}
	fileName := file.Filename
	if fileName == "" {
		fileName = mv.TargetPath[strings.LastIndex(mv.TargetPath, "/")+1:]
	}
	zipBytes, err := modpack.WrapJarAsSolderZip(fileName, jar)
	if err != nil {
		return fmt.Errorf("wrap failed: %w", err)
	}
	md5hex, _, _ := modpack.Hashes(zipBytes)
	prov, err := modpack.NewProviderFromSettings(h.state.Store.GetSetting)
	if err != nil || prov == nil {
		return fmt.Errorf("no pack storage configured")
	}
	slug := slugify(strings.TrimSuffix(fileName, ".jar"))
	newKey := "packs/" + ownerID + "/mods/" + slug + "/" + slug + "-" + v.VersionNum + ".zip"
	if err := prov.Put(newKey, zipBytes); err != nil {
		return fmt.Errorf("storage put failed: %w", err)
	}
	oldKey := mv.StorageKey

	now := time.Now()
	mv.Version = v.VersionNum
	mv.Filesize = int64(len(zipBytes))
	mv.StorageKey = newKey
	mv.MD5 = md5hex
	mv.SHA1 = file.Hashes["sha1"]
	mv.SHA512 = file.Hashes["sha512"]
	mv.ModrinthDownloadURL = file.URL
	mv.URLOverride = ""
	mv.Source = models.SourceModrinth
	mv.ModrinthProjectID = v.ProjectID
	mv.ModrinthVersionID = v.ID
	mv.ModrinthVersionNumber = v.VersionNum
	mv.ModrinthGameVersions = strings.Join(v.GameVersions, ",")
	mv.ModrinthLatestVersionID = v.ID // aligned: current == latest-known -> clears "update available"
	mv.ModrinthLastChecked = &now
	if err := h.state.Store.UpdateModversion(mv); err != nil {
		return fmt.Errorf("failed to update content: %w", err)
	}
	if oldKey != "" && oldKey != newKey {
		_ = prov.Delete(oldKey) // best-effort; the DB no longer references it
	}
	return nil
}

// verifyModrinthBytes checks the downloaded bytes against Modrinth's published
// hashes. It requires at least one known hash and fails on any mismatch, so an
// altered or corrupted download can never be wrapped and stored as "identical".
func verifyModrinthBytes(data []byte, hashes map[string]string) error {
	verified := false
	if want := hashes["sha1"]; want != "" {
		sum := sha1.Sum(data)
		got := hex.EncodeToString(sum[:])
		if !strings.EqualFold(got, want) {
			return fmt.Errorf("sha1 mismatch: expected %s, got %s", want, got)
		}
		verified = true
	}
	if want := hashes["sha512"]; want != "" {
		sum := sha512.Sum512(data)
		got := hex.EncodeToString(sum[:])
		if !strings.EqualFold(got, want) {
			return fmt.Errorf("sha512 mismatch: expected %s, got %s", want, got)
		}
		verified = true
	}
	if !verified {
		return fmt.Errorf("no sha1/sha512 hash published to verify against")
	}
	return nil
}

// downloadURL fetches a URL's body with a bounded size (256 MiB) for mod files.
func downloadURL(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := replaceDownloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}
