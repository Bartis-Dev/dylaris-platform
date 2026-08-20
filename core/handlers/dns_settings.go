package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"dylaris-core/services"
)

// DNS settings API. The credential is written but never read back: responses
// carry a boolean and the SOURCE it came from, never the token itself.
//
// Saving probes the provider for real before the configuration is accepted, on
// the same principle as the storage reachability verifier - prove it works, do
// not assume it. A DNS credential that is wrong fails invisibly otherwise: the
// reconciler logs and retries every 30 seconds while the records never move.

type DNSSettingsHandler struct {
	state *AppState
}

func NewDNSSettingsHandler(state *AppState) *DNSSettingsHandler {
	return &DNSSettingsHandler{state: state}
}

// dnsProbeTimeout bounds the provider call made while an admin waits on a save.
const dnsProbeTimeout = 15 * time.Second

type dnsSettingsResponse struct {
	Success bool `json:"success"`

	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	// Zones the reconciler may write into. One credential, several zones: what
	// multiplies for a hoster is zones, not providers.
	Zones []string `json:"zones"`
	// RegionNames is the per-region record-name selection. A region absent here
	// falls back to its edges' own EDGE_WILDCARD.
	RegionNames map[string][]string `json:"regionNames"`
	// GraceMinutes is how long a name must go unadvertised before its records
	// are removed.
	GraceMinutes int `json:"graceMinutes"`
	// ManagedNames is every name in effect per region, labelled by origin.
	// Without this the failure mode is silent: an admin ticks domains in the
	// panel while a leftover EDGE_WILDCARD quietly keeps winning.
	ManagedNames []managedNameView `json:"managedNames"`

	// TokenSet says a credential is configured; the token itself is never sent.
	TokenSet bool `json:"tokenSet"`
	// Source is which configuration is in effect: "env", "panel" or "none".
	Source string `json:"source"`
	// EnvManaged means the environment supplies the token, so the panel field is
	// read-only. Without this the screen would happily accept a token that the
	// resolver then ignores.
	EnvManaged bool `json:"envManaged"`

	// Providers the build supports, so the panel never offers one that would be
	// rejected on save.
	// Each entry carries the credential FIELDS that provider needs, so the panel
	// renders the right inputs instead of assuming every provider takes a single
	// API token (netcup, OVH, Route 53 and friends do not).
	Providers []services.DNSProviderSpec `json:"providers"`

	Status *services.DNSReconcilerStatus `json:"status,omitempty"`
}

// managedNameView is one name in effect, with where it came from. Origin is
// "panel" or "edge"; Routable is false when the name falls outside every managed
// zone, which is the case an admin has to see - it is advertised and will never
// be written.
type managedNameView struct {
	Name     string `json:"name"`
	Region   string `json:"region"`
	Zone     string `json:"zone"`
	Origin   string `json:"origin"`
	Routable bool   `json:"routable"`
}

// Get GET /api/settings/dns - the effective DNS configuration: provider,
// zones, per-region names, orphan grace and the names Core currently manages.
// The API token itself is never returned, only a tokenSet flag saying whether
// one is stored.
func (h *DNSSettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	resolver := h.state.DNSConfig
	if resolver == nil {
		sendJSONError(w, "DNS settings unavailable", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(h.buildResponse(r.Context(), resolver.Resolve()))
}

// buildResponse renders the effective configuration. Shared by Get and Save so
// the screen after a save can never disagree with the screen after a reload.
func (h *DNSSettingsHandler) buildResponse(ctx context.Context, cfg services.DNSConfig) dnsSettingsResponse {
	resp := dnsSettingsResponse{
		Success:      true,
		Enabled:      cfg.Enabled,
		Provider:     cfg.Provider,
		Zones:        cfg.Zones,
		RegionNames:  cfg.RegionNames,
		GraceMinutes: int(cfg.OrphanGrace / time.Minute),
		ManagedNames: h.managedNames(ctx, cfg),
		TokenSet:     cfg.Token != "",
		Source:       string(cfg.Source),
		EnvManaged:   h.state.DNSConfig.EnvHasToken(),
		Providers:    services.SupportedDNSProviders(),
		Status:       services.LoadDNSStatus(ctx, h.state.Redis),
	}
	if resp.Zones == nil {
		resp.Zones = []string{}
	}
	if resp.RegionNames == nil {
		resp.RegionNames = map[string][]string{}
	}
	return resp
}

// managedNames resolves what the reconciler would act on right now, so the panel
// shows the names actually in effect rather than the ones just typed.
func (h *DNSSettingsHandler) managedNames(ctx context.Context, cfg services.DNSConfig) []managedNameView {
	if h.state.Redis == nil {
		return []managedNameView{}
	}
	plan := services.BuildDNSPlan(
		services.GetEdgesFromRedis(ctx, h.state.Redis),
		BeamRelayAdverts(ctx, h.state.Redis),
		cfg.RegionNames,
		cfg.Zones,
	)
	out := make([]managedNameView, 0, len(plan.Names)+len(plan.Unroutable))
	for _, n := range plan.Names {
		out = append(out, managedNameView{
			Name: n.Name, Region: n.Region, Zone: n.Zone, Origin: n.Origin, Routable: true,
		})
	}
	for _, n := range plan.Unroutable {
		out = append(out, managedNameView{
			Name: n.Name, Region: n.Region, Origin: n.Origin, Routable: false,
		})
	}
	return out
}

type dnsSaveRequest struct {
	Enabled  bool     `json:"enabled"`
	Provider string   `json:"provider"`
	Zones    []string `json:"zones"`
	// RegionNames is the per-region name selection. A region left out falls back
	// to its edges' own EDGE_WILDCARD.
	RegionNames map[string][]string `json:"regionNames"`
	// GraceMinutes is how long a name must go unadvertised before deletion. 0
	// means "keep the current value", so a client that does not send the field
	// cannot silently reset it.
	GraceMinutes int `json:"graceMinutes"`
	// Token is optional on update: an empty string keeps the stored credential,
	// so an admin can change the zone without re-typing the token. ClearToken
	// removes it, which an empty string cannot express.
	Token      string `json:"token"`
	ClearToken bool   `json:"clearToken"`
}

// Save POST /api/settings/dns - settings.write.
func (h *DNSSettingsHandler) Save(w http.ResponseWriter, r *http.Request) {
	resolver := h.state.DNSConfig
	if resolver == nil {
		sendJSONError(w, "DNS settings unavailable", http.StatusInternalServerError)
		return
	}
	var req dnsSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	zones := normalizeDNSList(req.Zones)
	token := strings.TrimSpace(req.Token)

	if provider != "" && !dnsProviderSupported(provider) {
		sendJSONError(w, "Unknown DNS provider: "+provider, http.StatusBadRequest)
		return
	}

	// Every selected name must sit inside a managed zone. Saving one that does
	// not would leave a name that is advertised and silently never written -
	// exactly the invisible failure this screen exists to prevent.
	regionNames := map[string][]string{}
	for region, names := range req.RegionNames {
		r := strings.ToLower(strings.TrimSpace(region))
		clean := normalizeDNSList(names)
		if r == "" || len(clean) == 0 {
			continue
		}
		for _, n := range clean {
			if services.ResolveZone(n, zones) == "" {
				sendJSONError(w, "The name "+n+" is not inside any managed zone. Add its zone first.", http.StatusBadRequest)
				return
			}
		}
		regionNames[r] = clean
	}

	// The environment owns the credential when it sets one. Accepting a token
	// here would store a value the resolver never reads - a setting that looks
	// applied and does nothing.
	if resolver.EnvHasToken() && (token != "" || req.ClearToken) {
		sendJSONError(w, "The DNS token is supplied by the environment (DNS_API_TOKEN) and cannot be changed here", http.StatusConflict)
		return
	}

	// Resolve what the configuration WOULD be, so the probe tests the real
	// combination rather than the fields typed on this screen alone.
	effective := resolver.Resolve()
	probeToken := effective.Token
	switch {
	case req.ClearToken:
		probeToken = ""
	case token != "":
		probeToken = token
	}
	probeProvider := provider
	if probeProvider == "" {
		probeProvider = effective.Provider
	}

	// Probe only when the updater is being turned on. A disabled configuration
	// is allowed to be incomplete - that is how an admin saves a zone now and
	// adds the credential later.
	if req.Enabled {
		if probeToken == "" {
			sendJSONError(w, "An API token is required to enable the DNS updater", http.StatusBadRequest)
			return
		}
		if len(zones) == 0 {
			sendJSONError(w, "At least one zone is required to enable the DNS updater (the zone NAME, e.g. example.com)", http.StatusBadRequest)
			return
		}
		// Probe EVERY zone, not just the first. A credential scoped to one zone
		// of several would otherwise pass here and then fail forever on the
		// others, in a log rather than on screen.
		for _, z := range zones {
			if err := probeDNSZone(r.Context(), probeProvider, probeToken, z); err != nil {
				sendJSONError(w, "DNS provider rejected zone "+z+": "+err.Error(), http.StatusBadRequest)
				return
			}
		}
	}

	zonesJSON, err := json.Marshal(zones)
	if err != nil {
		sendJSONError(w, "Failed to encode zones", http.StatusInternalServerError)
		return
	}
	regionJSON, err := json.Marshal(regionNames)
	if err != nil {
		sendJSONError(w, "Failed to encode region names", http.StatusInternalServerError)
		return
	}

	// There is no transaction across settings, so a failure mid-loop leaves a
	// partial configuration. The ORDER decides which partial state that is, and
	// the credential goes FIRST on purpose: if a later write fails, the stored
	// token is the one that just passed the probe, and the enabled flag still
	// holds its previous value. Writing it last produced the opposite - the
	// updater switched on against a credential that was never updated, which
	// fails in a log every 30 seconds rather than on this screen.
	var pairs []struct{ k, v string }
	switch {
	case req.ClearToken:
		pairs = append(pairs, struct{ k, v string }{services.DNSTokenSettingKey, ""})
	case token != "":
		pairs = append(pairs, struct{ k, v string }{services.DNSTokenSettingKey, token})
	}
	pairs = append(pairs,
		struct{ k, v string }{services.DNSProviderSettingKey, provider},
		struct{ k, v string }{services.DNSZonesSettingKey, string(zonesJSON)},
		struct{ k, v string }{services.DNSRegionNamesSettingKey, string(regionJSON)},
	)
	// 0 means "not sent", so a client that omits the field cannot reset the
	// grace period to the floor without meaning to.
	if req.GraceMinutes > 0 {
		grace := req.GraceMinutes
		if grace < services.MinDNSOrphanGraceMinutes {
			grace = services.MinDNSOrphanGraceMinutes
		}
		pairs = append(pairs, struct{ k, v string }{services.DNSGraceSettingKey, strconv.Itoa(grace)})
	}
	// Last, so the updater only ever switches on over settings that are already
	// stored.
	pairs = append(pairs, struct{ k, v string }{services.DNSEnabledSettingKey, boolSetting(req.Enabled)})
	for _, p := range pairs {
		if err := h.state.Store.SetSetting(p.k, p.v); err != nil {
			sendJSONError(w, "Failed to save DNS settings", http.StatusInternalServerError)
			return
		}
	}

	json.NewEncoder(w).Encode(h.buildResponse(r.Context(), resolver.Resolve()))
}

// normalizeDNSList lowercases, trims the trailing root dot, drops blanks and
// dedupes, so a zone typed as "Example.com." and one typed as "example.com"
// cannot end up as two entries that never match each other.
func normalizeDNSList(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range in {
		v := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// Zone discovery has FOUR outcomes and they must stay distinguishable. Merging
// "the call failed" into "no zones found" sends an admin to widen a token that
// was already fine, which is the wrong and slower remedy.
const (
	dnsZonesOK          = "ok"          // zones returned
	dnsZonesEmpty       = "empty"       // supported, credential works, no zones visible
	dnsZonesUnsupported = "unsupported" // the provider cannot list zones at all
	dnsZonesError       = "error"       // the call failed; show the real error
)

type dnsZonesResponse struct {
	Success bool     `json:"success"`
	State   string   `json:"state"`
	Zones   []string `json:"zones"`
	Error   string   `json:"error,omitempty"`
}

// Zones GET /api/settings/dns/zones - lists the zones the credential can see.
// Always 200: every outcome here is information for the screen, not a failure of
// the request itself.
func (h *DNSSettingsHandler) Zones(w http.ResponseWriter, r *http.Request) {
	resolver := h.state.DNSConfig
	if resolver == nil {
		sendJSONError(w, "DNS settings unavailable", http.StatusInternalServerError)
		return
	}
	cfg := resolver.Resolve()
	if cfg.Token == "" {
		json.NewEncoder(w).Encode(dnsZonesResponse{
			Success: true, State: dnsZonesError, Zones: []string{},
			Error: "no API token configured",
		})
		return
	}
	provider, err := services.NewDNSProvider(cfg.Provider, cfg.Token)
	if err != nil || provider == nil {
		msg := "provider could not be built"
		if err != nil {
			msg = err.Error()
		}
		json.NewEncoder(w).Encode(dnsZonesResponse{
			Success: true, State: dnsZonesError, Zones: []string{}, Error: msg,
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), dnsProbeTimeout)
	defer cancel()

	zones, err := provider.Zones(ctx)
	switch {
	case errors.Is(err, services.ErrZoneListingUnsupported):
		json.NewEncoder(w).Encode(dnsZonesResponse{
			Success: true, State: dnsZonesUnsupported, Zones: []string{},
		})
	case err != nil:
		// The raw provider error, deliberately. libdns does not normalise
		// errors, so a 403 and a timeout cannot be told apart here - only the
		// original text lets an admin tell which one they are looking at.
		json.NewEncoder(w).Encode(dnsZonesResponse{
			Success: true, State: dnsZonesError, Zones: []string{}, Error: err.Error(),
		})
	case len(zones) == 0:
		json.NewEncoder(w).Encode(dnsZonesResponse{
			Success: true, State: dnsZonesEmpty, Zones: []string{},
		})
	default:
		json.NewEncoder(w).Encode(dnsZonesResponse{
			Success: true, State: dnsZonesOK, Zones: zones,
		})
	}
}

// probeDNSZone proves the credential can actually READ the zone it is about to
// manage. Reading is the right probe: it needs the same permission scope the
// reconciler needs and, unlike a write, leaves nothing behind.
func probeDNSZone(ctx context.Context, providerName, token, zone string) error {
	provider, err := services.NewDNSProvider(providerName, token)
	if err != nil {
		return err
	}
	if provider == nil {
		return errors.New("no API token")
	}
	probeCtx, cancel := context.WithTimeout(ctx, dnsProbeTimeout)
	defer cancel()
	// A zone with no A records at all is a valid answer - the check is that the
	// call is authorised and the zone resolves, not that it holds anything.
	_, err = provider.ListA(probeCtx, zone, "probe."+zone)
	return err
}

func dnsProviderSupported(name string) bool {
	_, ok := services.DNSProviderSpecFor(name)
	return ok
}

func boolSetting(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
