package handlers

import (
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"dylaris-core/models"
	"dylaris-core/services"
	"dylaris-core/storage/modpack"

	"github.com/gorilla/mux"
)

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
	var req replaceModrinthRequest
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
	file := v.PrimaryFile()
	if file.URL == "" {
		sendJSONError(w, "Modrinth version has no downloadable file", http.StatusBadGateway)
		return
	}

	// Download Modrinth's exact jar.
	jar, err := downloadURL(file.URL)
	if err != nil {
		sendJSONError(w, "Download from Modrinth failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Integrity gate: the whole point of Replace is byte-identity with Modrinth.
	// Verify the downloaded bytes against Modrinth's published hashes BEFORE we
	// wrap, store, or delete anything. A mismatch aborts with nothing touched.
	if err := verifyModrinthBytes(jar, file.Hashes); err != nil {
		sendJSONError(w, "Modrinth download integrity check failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Re-wrap + store under a fresh key; delete the old one.
	fileName := file.Filename
	if fileName == "" {
		fileName = mv.TargetPath[strings.LastIndex(mv.TargetPath, "/")+1:]
	}
	zipBytes, err := modpack.WrapJarAsSolderZip(fileName, jar)
	if err != nil {
		sendJSONError(w, "Wrap failed", http.StatusInternalServerError)
		return
	}
	md5hex, _, _ := modpack.Hashes(zipBytes)
	prov, err := modpack.NewProviderFromSettings(h.state.Store.GetSetting)
	if err != nil || prov == nil {
		sendJSONError(w, "No pack storage configured", http.StatusFailedDependency)
		return
	}
	slug := slugify(strings.TrimSuffix(fileName, ".jar"))
	newKey := "packs/" + userID + "/mods/" + slug + "/" + slug + "-" + v.VersionNum + ".zip"
	// Safe-cutover ordering: store the new artifact first. The old key is only
	// deleted after the DB row is repointed, so nothing references a missing
	// object at any point and an early failure leaves the old artifact intact.
	if err := prov.Put(newKey, zipBytes); err != nil {
		sendJSONError(w, "Storage put failed", http.StatusInternalServerError)
		return
	}
	oldKey := mv.StorageKey

	// Rewrite the modversion to Modrinth's exact file.
	mv.Version = v.VersionNum
	mv.Filesize = int64(len(zipBytes))
	mv.StorageKey = newKey
	mv.MD5 = md5hex             // md5 of the stored Solder-wrapped zip
	mv.SHA1 = file.Hashes["sha1"]     // sha1 of the inner Modrinth file (mrpack files[])
	mv.SHA512 = file.Hashes["sha512"] // sha512 of the inner Modrinth file (mrpack files[])
	mv.ModrinthDownloadURL = file.URL // cdn.modrinth.com — clean mrpack files[] reference
	mv.URLOverride = ""               // Solder uses the mirror URL from storage_key, not the Modrinth jar
	mv.Source = models.SourceModrinth
	mv.ModrinthProjectID = v.ProjectID
	mv.ModrinthVersionID = v.ID
	mv.ModrinthVersionNumber = v.VersionNum
	mv.ModrinthGameVersions = strings.Join(v.GameVersions, ",")
	if err := h.state.Store.UpdateModversion(mv); err != nil {
		sendJSONError(w, "Failed to update content", http.StatusInternalServerError)
		return
	}
	if oldKey != "" && oldKey != newKey {
		_ = prov.Delete(oldKey) // best-effort; the DB no longer references it
	}

	h.state.Events.Publish(r.Context(), "pack_content.changed", map[string]interface{}{"buildId": build.ID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "modversionId": mv.ID, "linked": true})
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
func downloadURL(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}
