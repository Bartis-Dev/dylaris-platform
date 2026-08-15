package handlers

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"dylaris-core/storage/modpack"

	"github.com/gorilla/mux"
)

const (
	solderDeliveryCore      = "core"
	solderDeliveryPresigned = "presigned"
	solderDeliveryPublic    = "public"

	// solderPresignTTL is the lifetime of a presigned Solder mod URL. The build
	// JSON is re-projected (and re-presigned) on every launcher fetch, so each
	// install starts with a fresh window; 6h comfortably covers a large pack or
	// a paused-and-resumed download. Caveat: a launcher that caches the build
	// JSON and later repairs one mod WITHOUT re-fetching it could hit an expired
	// URL under presigned delivery; core/public have permanent URLs.
	solderPresignTTL = 6 * time.Hour
)

const solderDeliveryModeKey = "solder_delivery_mode"

// solderModURL builds the launcher-facing download URL for one stored Solder
// object (a mod/loader zip key), honoring solder_delivery_mode. A gated
// (private/hidden) pack is NEVER served a permanent public URL: under public
// mode it is downgraded to presigned when the backend can presign, else core.
func solderModURL(ctx context.Context, getSetting func(string) (string, error), prov modpack.ModpackStorageProvider, key string, packGated bool) (string, error) {
	mode, _ := getSetting(solderDeliveryModeKey)
	if mode == "" {
		mode = solderDeliveryCore
	}
	if mode == solderDeliveryPublic && packGated {
		if prov != nil {
			if u, err := prov.DownloadURL(ctx, key, solderPresignTTL); err == nil && u != "" {
				return u, nil
			}
		}
		mode = solderDeliveryCore
	}
	switch mode {
	case solderDeliveryPublic:
		base, _ := getSetting("solder_mirror_url")
		base = strings.TrimSpace(base)
		if base == "" {
			return "", fmt.Errorf("solder_mirror_url is not set (required for public delivery)")
		}
		return strings.TrimRight(base, "/") + "/" + key, nil
	case solderDeliveryPresigned:
		if prov == nil {
			return "", fmt.Errorf("storage provider unavailable for presigned delivery")
		}
		u, err := prov.DownloadURL(ctx, key, solderPresignTTL)
		if err != nil {
			return "", fmt.Errorf("presign %s: %w", key, err)
		}
		if u == "" {
			return "", fmt.Errorf("storage backend cannot presign (presigned delivery requires S3/R2)")
		}
		return u, nil
	default: // core
		base, _ := getSetting("core_public_url")
		base = strings.TrimSpace(base)
		if base == "" {
			return "", fmt.Errorf("core_public_url is not set (required for core delivery)")
		}
		return strings.TrimRight(base, "/") + "/solder/mirror/" + key, nil
	}
}

// solderMirrorBase resolves the base URL under which Solder mod-zip URLs resolve,
// switching on solder_delivery_mode: public advertises solder_mirror_url; core and
// presigned both advertise Core's own mirror route (every mods[].url we serve is
// already absolute, so a launcher never needs to prepend this for a presigned URL).
// The returned base ALWAYS ends with a single trailing slash (the Solder contract).
func solderMirrorBase(getSetting func(string) (string, error)) (string, error) {
	mode, _ := getSetting(solderDeliveryModeKey)
	if mode == solderDeliveryPublic {
		base, _ := getSetting("solder_mirror_url")
		base = strings.TrimSpace(base)
		if base == "" {
			return "", fmt.Errorf("solder_mirror_url is not set (required for public delivery)")
		}
		return strings.TrimRight(base, "/") + "/", nil
	}
	// core AND presigned advertise the core mirror base for the modpack-list
	// mirror_url field; every mods[].url we serve is already absolute, so a
	// launcher never needs to prepend it.
	base, _ := getSetting("core_public_url")
	base = strings.TrimSpace(base)
	if base == "" {
		return "", fmt.Errorf("core_public_url is not set (required for the local Solder mirror)")
	}
	return strings.TrimRight(base, "/") + "/solder/mirror/", nil
}

// SolderMirror streams a stored public artifact (a Solder mod zip, a loader
// zip, or a rendered pack .mrpack). SECURITY: it serves ONLY keys under
// solder/mods/, loaders/, or modpacks/ after path.Clean, and rejects any
// traversal (..). It must never read an arbitrary storage object.
//
// This route is unauthenticated by protocol: a Technic launcher fetches these
// URLs with no credential, so everything under the allowed prefixes is public
// to anyone holding the URL. That makes the prefix list the whole boundary,
// and it has to be the NARROWEST set a launcher actually requests - not every
// prefix the render path happens to write.
//
// It used to allow all of solder/, which also covers solder/manifests/. A
// build manifest is written there but read only by Core itself (GetBuild does
// a server-side prov.Get and projects it into the launcher's build shape), so
// no client ever requests that key. MEASURED on the testbed: a PRIVATE pack
// answered 404 on /solder/api/modpack/{slug} as designed, while
// /solder/mirror/solder/manifests/{owner}/{slug}/{version}/build.json returned
// it in full - the whole mod list of a pack whose launcher API refuses to
// admit it exists. Keep this list to what a launcher downloads.
func (h *SolderHandler) SolderMirror(w http.ResponseWriter, r *http.Request) {
	if !h.state.FeatureFlags.IsModpacksEnabled(r.Context()) {
		solderJSONError(w, "Modpacks are disabled", http.StatusForbidden)
		return
	}
	rest := mux.Vars(r)["rest"]
	key := path.Clean(rest)
	if strings.Contains(key, "..") || !(strings.HasPrefix(key, "solder/mods/") || strings.HasPrefix(key, "loaders/") || strings.HasPrefix(key, "modpacks/")) {
		solderJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	prov, err := h.state.buildModpackStorageProvider()
	if err != nil || prov == nil {
		solderJSONError(w, "Storage not configured", http.StatusInternalServerError)
		return
	}
	// deliverStream, not deliverRedirect, and that is a deliberate limit rather
	// than an oversight. This route serves the Technic launcher as well as our
	// own nodes, and whether that launcher follows a 302 is not something this
	// codebase can verify - a wrong guess breaks pack installs for every
	// launcher user. Streaming is correct for every client and already removes
	// the defect that mattered, which was Core holding the whole pack in memory
	// once per concurrent request. Switching this to a redirect would save
	// bandwidth too, but needs a real launcher to test against first.
	if err := serveModpackObject(w, r, prov, key, deliverStream, "application/zip", path.Base(key)); err != nil {
		solderJSONError(w, "Not found", http.StatusNotFound)
		return
	}
}
