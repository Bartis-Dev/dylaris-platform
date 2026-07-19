package handlers

import (
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"

	"dylaris-core/storage/modpack"

	"github.com/gorilla/mux"
)

// solderMirrorBase resolves the base URL under which Solder mod-zip URLs resolve.
// For S3 storage the operator sets solder_mirror_url to the public bucket base;
// for local storage the mirror is served by Core at {core_public_url}/solder/mirror/.
// The returned base ALWAYS ends with a single trailing slash (the Solder contract).
func solderMirrorBase(getSetting func(string) (string, error)) (string, error) {
	provider, _ := getSetting("modpack_storage_provider")
	if provider == "s3" {
		base, _ := getSetting("solder_mirror_url")
		base = strings.TrimSpace(base)
		if base == "" {
			return "", fmt.Errorf("solder_mirror_url is not set (required for S3 storage)")
		}
		return strings.TrimRight(base, "/") + "/", nil
	}
	// local / "" — Core serves the mirror route.
	base, _ := getSetting("core_public_url")
	base = strings.TrimSpace(base)
	if base == "" {
		return "", fmt.Errorf("core_public_url is not set (required for the local Solder mirror)")
	}
	return strings.TrimRight(base, "/") + "/solder/mirror/", nil
}

// SolderMirror streams a stored public artifact (a Solder mod zip, a loader
// zip, or a rendered pack .mrpack). SECURITY: it serves ONLY keys under
// solder/, loaders/, or modpacks/ after path.Clean, and rejects any
// traversal (..). It must never read an arbitrary storage object.
func (h *SolderHandler) SolderMirror(w http.ResponseWriter, r *http.Request) {
	if !h.state.FeatureFlags.IsModpacksEnabled(r.Context()) {
		solderJSONError(w, "Modpacks are disabled", http.StatusForbidden)
		return
	}
	rest := mux.Vars(r)["rest"]
	key := path.Clean(rest)
	if strings.Contains(key, "..") || !(strings.HasPrefix(key, "solder/") || strings.HasPrefix(key, "loaders/") || strings.HasPrefix(key, "modpacks/")) {
		solderJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	prov, err := modpack.NewProviderFromSettings(h.state.Store.GetSetting, h.state.buildCoreStorageProvider)
	if err != nil || prov == nil {
		solderJSONError(w, "Storage not configured", http.StatusInternalServerError)
		return
	}
	data, err := prov.Get(key)
	if err != nil {
		solderJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(key)))
	_, _ = w.Write(data)
}
