package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"dylaris-core/services"
)

// deliveryCapabilities tells the panel which Solder delivery modes are usable
// with the CURRENTLY SAVED storage config, so it can grey out the rest.
type deliveryCapabilities struct {
	CanPresign       bool  `json:"canPresign"`
	PublicConfigured bool  `json:"publicConfigured"`
	PublicReachable  *bool `json:"publicReachable"`
	// PrivatePackCount is how many Solder-capable packs are private/hidden; the
	// panel warns that public delivery would expose their files in the bucket.
	PrivatePackCount int               `json:"privatePackCount"`
	Notes            map[string]string `json:"notes"`
	// StorageConfigured is whether modpack storage resolves to a real provider
	// at all. It is the same predicate every write path uses before returning
	// 424, surfaced so the panel can say so BEFORE someone builds a pack and
	// discovers it at upload time.
	StorageConfigured bool `json:"storageConfigured"`
}

// classifyReachable maps a SafeHead result to the publicReachable tri-state.
//
// probedObject says whether the URL was a REAL published mod file or merely the
// mirror base, and the two deserve different rules:
//
//   - A real object answers the actual question, so only a 2xx means "a player
//     can download this". Anything else is a no.
//   - The BASE cannot answer it: a correctly configured public bucket
//     legitimately 404s on its base path, so only the unambiguous "you are not
//     allowed" statuses count against it.
//
// The distinction is load-bearing rather than tidy. Probing the base reported
// REACHABLE for Cloudflare R2's S3 endpoint, which answers 400 to any
// unauthenticated request - measured against the real bucket, on the base and
// on a real object alike. The panel then offered public delivery for a base no
// player can read a single byte from, and every launcher would have failed.
//
// A transport error stays unknown (nil) either way.
func classifyReachable(status int, err error, probedObject bool) *bool {
	if err != nil {
		return nil
	}
	var b bool
	if probedObject {
		b = status >= 200 && status < 300
	} else {
		b = status != http.StatusUnauthorized && status != http.StatusForbidden
	}
	return &b
}

// buildDeliveryCapabilities is the pure assembler (canPresign from a provider
// probe, mirrorURL from settings, reach from SafeHead) into the panel payload.
func buildDeliveryCapabilities(canPresign bool, mirrorURL string, reach *bool, privatePackCount int, storageConfigured bool) deliveryCapabilities {
	mirrorURL = strings.TrimSpace(mirrorURL)
	publicConfigured := mirrorURL != "" && validatePublicBaseURL("solder mirror URL", mirrorURL) == nil
	notes := map[string]string{}
	if !canPresign {
		notes["presigned"] = "The current storage backend can't produce presigned URLs. Presigned delivery needs S3/R2-backed storage."
	}
	if !publicConfigured {
		notes["public"] = "Set a valid public Solder mirror URL to use public delivery."
	} else if reach != nil && !*reach {
		// Deliberately does not name a status code. The probe now fetches a real
		// pack object, so "unreadable" covers every non-2xx answer - and the one
		// that actually shows up in practice is R2's S3 endpoint replying 400 to an
		// unsigned request, not the 401/403 this note used to promise.
		notes["public"] = "The configured mirror URL did not serve a pack file publicly. Anonymous readers would not be able to download from it."
	}
	if !storageConfigured {
		notes["storage"] = "Modpack storage is not configured, so nothing can be uploaded. Pick local paths or a storage connection under Settings -> Modpacks."
	}
	return deliveryCapabilities{
		CanPresign:        canPresign,
		PublicConfigured:  publicConfigured,
		PublicReachable:   reach,
		PrivatePackCount:  privatePackCount,
		Notes:             notes,
		StorageConfigured: storageConfigured,
	}
}

// DeliveryCapabilities GET /api/admin/settings/modpacks/delivery-capabilities.
// Reflects the SAVED storage config only — it never presigns or HTTP-probes a
// caller-supplied URL, so it is not an SSRF lever.
func (h *ModpackSettingsHandler) DeliveryCapabilities(w http.ResponseWriter, r *http.Request) {
	get := func(k string) string { v, _ := h.state.Store.GetSetting(k); return v }

	canPresign := false
	// This screen is admin-only and rare, so it builds the provider directly -
	// it needs the object itself for the presign probe, not just the verdict.
	storageConfigured := false
	if prov, err := h.state.buildModpackStorageProvider(); err == nil && prov != nil {
		storageConfigured = true
		if u, e := prov.DownloadURL(r.Context(), "solder/__presign_probe__", time.Minute); e == nil && u != "" {
			canPresign = true
		}
	}

	mirrorURL := strings.TrimSpace(get("solder_mirror_url"))
	var reach *bool
	if mirrorURL != "" && validatePublicBaseURL("solder mirror URL", mirrorURL) == nil {
		// Prefer a REAL published mod file over the bare base - see
		// classifyReachable for why the base cannot answer this. Still the
		// SAVED config being probed, never a caller-supplied URL, so this is
		// not an SSRF lever either way.
		probeURL, probedObject := mirrorURL, false
		if key, kerr := h.state.Store.AnyPublishedSolderModKey(); kerr == nil && key != "" {
			probeURL = strings.TrimRight(mirrorURL, "/") + "/" + strings.TrimLeft(key, "/")
			probedObject = true
		}
		status, err := services.SafeHead(r.Context(), probeURL, 5*time.Second)
		reach = classifyReachable(status, err, probedObject)
	}

	privatePacks, _ := h.state.Store.CountPrivateSolderPacks()
	caps := buildDeliveryCapabilities(canPresign, mirrorURL, reach, privatePacks, storageConfigured)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "capabilities": caps})
}
