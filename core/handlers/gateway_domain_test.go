package handlers

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"dylaris-core/models"
	"dylaris-core/store"
)

// gatewayDomainFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only GetSetting (read by resolveRouteDomain,
// loadBlockedRoutePrefixes and isSnapshotFetchHostAllowed) and
// GetGatewayRouteLimit (read by effectiveRouteLimit) are overridden. Any
// other call would panic - these tests never make one.
type gatewayDomainFakeStore struct {
	store.Store

	settings map[string]string

	routeLimits map[string]*models.GatewayRouteLimit
}

func (f *gatewayDomainFakeStore) GetSetting(key string) (string, error) {
	return f.settings[key], nil
}

func (f *gatewayDomainFakeStore) GetGatewayRouteLimit(scope string) (*models.GatewayRouteLimit, error) {
	l, ok := f.routeLimits[scope]
	if !ok {
		return nil, errors.New("not found")
	}
	return l, nil
}

func newGatewayDomainHandler(settings map[string]string) *GatewayHandler {
	return &GatewayHandler{state: &AppState{Store: &gatewayDomainFakeStore{settings: settings}}}
}

// routeDomainReq builds the exact anonymous struct type resolveRouteDomain
// takes (gateway.go:425-431), field-for-field including json tags, so it is
// directly assignable without a conversion.
func routeDomainReq(domain, subdomain, hosterDomain, customDomain string, targetPort int) *struct {
	Domain       string `json:"domain"`
	Subdomain    string `json:"subdomain"`
	HosterDomain string `json:"hosterDomain"`
	CustomDomain string `json:"customDomain"`
	TargetPort   int    `json:"targetPort"`
} {
	return &struct {
		Domain       string `json:"domain"`
		Subdomain    string `json:"subdomain"`
		HosterDomain string `json:"hosterDomain"`
		CustomDomain string `json:"customDomain"`
		TargetPort   int    `json:"targetPort"`
	}{Domain: domain, Subdomain: subdomain, HosterDomain: hosterDomain, CustomDomain: customDomain, TargetPort: targetPort}
}

func hosterDomainsJSON(t *testing.T, hosters []HosterDomain) string {
	t.Helper()
	b, err := json.Marshal(hosters)
	if err != nil {
		t.Fatalf("marshal hosters: %v", err)
	}
	return string(b)
}

// TestResolveRouteDomain covers the three-path decision tree (hoster-picker,
// custom-domain, legacy-raw) plus the reserved-label and label-count guards.
func TestResolveRouteDomain(t *testing.T) {
	hostersJSON := func(t *testing.T) string {
		return hosterDomainsJSON(t, []HosterDomain{{Domain: "dylaris.com", Validation: "dns"}})
	}

	t.Run("valid hoster subdomain", func(t *testing.T) {
		h := newGatewayDomainHandler(map[string]string{"gateway_hoster_domains": hostersJSON(t)})
		got, err := h.resolveRouteDomain(routeDomainReq("", "myserver", "dylaris.com", "", 25565))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "myserver.dylaris.com" {
			t.Fatalf("got %q, want myserver.dylaris.com", got)
		}
	})

	t.Run("hoster subdomain rejects an unconfigured hoster domain", func(t *testing.T) {
		h := newGatewayDomainHandler(map[string]string{"gateway_hoster_domains": hostersJSON(t)})
		_, err := h.resolveRouteDomain(routeDomainReq("", "myserver", "not-configured.com", "", 25565))
		if err == nil {
			t.Fatalf("expected an error for an unconfigured hoster domain")
		}
	})

	t.Run("valid custom domain", func(t *testing.T) {
		h := newGatewayDomainHandler(map[string]string{
			"gateway_hoster_domains":            hostersJSON(t),
			"gateway_custom_domains_enabled":    "true",
		})
		got, err := h.resolveRouteDomain(routeDomainReq("", "", "", "sub.example.com", 25565))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "sub.example.com" {
			t.Fatalf("got %q, want sub.example.com", got)
		}
	})

	t.Run("custom domain rejected when custom domains are not enabled", func(t *testing.T) {
		h := newGatewayDomainHandler(map[string]string{"gateway_custom_domains_enabled": "false"})
		_, err := h.resolveRouteDomain(routeDomainReq("", "", "", "sub.example.com", 25565))
		if err == nil {
			t.Fatalf("expected an error when custom domains are disabled")
		}
	})

	t.Run("reserved-label refusal on the hoster-picker path", func(t *testing.T) {
		h := newGatewayDomainHandler(map[string]string{"gateway_hoster_domains": hostersJSON(t)})
		_, err := h.resolveRouteDomain(routeDomainReq("", "admin", "dylaris.com", "", 25565))
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("err = %v, want a 'reserved' error for the default-blocklisted 'admin' subdomain", err)
		}
	})

	t.Run("reserved-label refusal on the custom-domain path (leftmost label)", func(t *testing.T) {
		h := newGatewayDomainHandler(map[string]string{"gateway_custom_domains_enabled": "true"})
		_, err := h.resolveRouteDomain(routeDomainReq("", "", "", "admin.example.com", 25565))
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("err = %v, want a 'reserved' error for the default-blocklisted 'admin' leftmost label", err)
		}
	})

	t.Run("custom domain with too few labels rejected", func(t *testing.T) {
		h := newGatewayDomainHandler(map[string]string{"gateway_custom_domains_enabled": "true"})
		_, err := h.resolveRouteDomain(routeDomainReq("", "", "", "onelabel", 25565))
		if err == nil || !strings.Contains(err.Error(), "at most two subdomain levels") {
			t.Fatalf("err = %v, want the label-count error", err)
		}
	})

	t.Run("custom domain with too many labels rejected", func(t *testing.T) {
		h := newGatewayDomainHandler(map[string]string{"gateway_custom_domains_enabled": "true"})
		_, err := h.resolveRouteDomain(routeDomainReq("", "", "", "a.b.c.d.e", 25565))
		if err == nil || !strings.Contains(err.Error(), "at most two subdomain levels") {
			t.Fatalf("err = %v, want the label-count error", err)
		}
	})

	t.Run("custom domain rejected as a subdomain of a configured hoster domain", func(t *testing.T) {
		h := newGatewayDomainHandler(map[string]string{
			"gateway_hoster_domains":         hostersJSON(t),
			"gateway_custom_domains_enabled": "true",
		})
		_, err := h.resolveRouteDomain(routeDomainReq("", "", "", "test.dylaris.com", 25565))
		if err == nil || !strings.Contains(err.Error(), "may not be a subdomain of a hoster domain") {
			t.Fatalf("err = %v, want the hoster-suffix rejection", err)
		}
	})

	t.Run("legacy raw domain path is accepted and lowercased", func(t *testing.T) {
		h := newGatewayDomainHandler(nil)
		got, err := h.resolveRouteDomain(routeDomainReq("MC.Example.com", "", "", "", 25565))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "mc.example.com" {
			t.Fatalf("got %q, want mc.example.com", got)
		}
	})

	t.Run("no domain provided at all is rejected", func(t *testing.T) {
		h := newGatewayDomainHandler(nil)
		_, err := h.resolveRouteDomain(routeDomainReq("", "", "", "", 25565))
		if err == nil {
			t.Fatalf("expected an error when no domain field is set")
		}
	})
}

// TestLoadBlockedRoutePrefixes pins the default-vs-explicit-empty semantics:
// an unset setting (raw == "") falls back to the protective default list; an
// explicitly-saved list - even an empty one - is honored as-is, including
// dropping the defaults entirely.
func TestLoadBlockedRoutePrefixes(t *testing.T) {
	t.Run("unset setting falls back to the default list", func(t *testing.T) {
		h := newGatewayDomainHandler(nil)
		got := h.loadBlockedRoutePrefixes()
		if !got["admin"] {
			t.Fatalf("blocked = %+v, want the default list (includes 'admin')", got)
		}
	})

	t.Run("explicit empty list overrides the default (nothing is blocked)", func(t *testing.T) {
		h := newGatewayDomainHandler(map[string]string{"gateway_blocked_route_prefixes": "[]"})
		got := h.loadBlockedRoutePrefixes()
		if len(got) != 0 {
			t.Fatalf("blocked = %+v, want empty (admin should no longer be reserved)", got)
		}
	})

	t.Run("explicit list is honored verbatim, lowercased and trimmed", func(t *testing.T) {
		h := newGatewayDomainHandler(map[string]string{"gateway_blocked_route_prefixes": `[" Custom ", "OTHER"]`})
		got := h.loadBlockedRoutePrefixes()
		if !got["custom"] || !got["other"] || got["admin"] {
			t.Fatalf("blocked = %+v, want exactly {custom, other} and NOT the default 'admin'", got)
		}
	})
}

// TestEffectiveRouteLimit pins the override -> user_default -> global
// precedence (gateway_link_routes.go:44). The user-specific override wins
// even when it is explicitly 0 (disabled); user_default/global only take
// effect when their MaxRoutes is > 0, so a 0 at those tiers means "unset,
// check the next tier" rather than "disabled" - an intentional asymmetry
// worth pinning explicitly.
func TestEffectiveRouteLimit(t *testing.T) {
	const uid = "user-1"

	t.Run("user override wins even when zero (explicitly disabled)", func(t *testing.T) {
		h := newGatewayDomainHandlerWithLimits(map[string]*models.GatewayRouteLimit{
			"user:" + uid:   {MaxRoutes: 0},
			"user_default":  {MaxRoutes: 5},
			"global":        {MaxRoutes: 10},
		})
		limit, has := h.effectiveRouteLimit(uid)
		if !has || limit != 0 {
			t.Fatalf("effectiveRouteLimit() = (%d, %v), want (0, true)", limit, has)
		}
	})

	t.Run("user override wins with a positive value", func(t *testing.T) {
		h := newGatewayDomainHandlerWithLimits(map[string]*models.GatewayRouteLimit{
			"user:" + uid: {MaxRoutes: 7},
			"global":      {MaxRoutes: 10},
		})
		limit, has := h.effectiveRouteLimit(uid)
		if !has || limit != 7 {
			t.Fatalf("effectiveRouteLimit() = (%d, %v), want (7, true)", limit, has)
		}
	})

	t.Run("no override falls to user_default when positive", func(t *testing.T) {
		h := newGatewayDomainHandlerWithLimits(map[string]*models.GatewayRouteLimit{
			"user_default": {MaxRoutes: 5},
			"global":       {MaxRoutes: 10},
		})
		limit, has := h.effectiveRouteLimit(uid)
		if !has || limit != 5 {
			t.Fatalf("effectiveRouteLimit() = (%d, %v), want (5, true)", limit, has)
		}
	})

	t.Run("user_default present but zero is treated as unset, falls to global", func(t *testing.T) {
		h := newGatewayDomainHandlerWithLimits(map[string]*models.GatewayRouteLimit{
			"user_default": {MaxRoutes: 0},
			"global":       {MaxRoutes: 3},
		})
		limit, has := h.effectiveRouteLimit(uid)
		if !has || limit != 3 {
			t.Fatalf("effectiveRouteLimit() = (%d, %v), want (3, true) - a zero user_default should not win", limit, has)
		}
	})

	t.Run("nothing configured anywhere is unlimited", func(t *testing.T) {
		h := newGatewayDomainHandlerWithLimits(map[string]*models.GatewayRouteLimit{})
		limit, has := h.effectiveRouteLimit(uid)
		if has || limit != 0 {
			t.Fatalf("effectiveRouteLimit() = (%d, %v), want (0, false)", limit, has)
		}
	})

	t.Run("global present but zero is also treated as unset -> unlimited", func(t *testing.T) {
		h := newGatewayDomainHandlerWithLimits(map[string]*models.GatewayRouteLimit{
			"global": {MaxRoutes: 0},
		})
		limit, has := h.effectiveRouteLimit(uid)
		if has || limit != 0 {
			t.Fatalf("effectiveRouteLimit() = (%d, %v), want (0, false)", limit, has)
		}
	})
}

func newGatewayDomainHandlerWithLimits(limits map[string]*models.GatewayRouteLimit) *GatewayHandler {
	return &GatewayHandler{state: &AppState{Store: &gatewayDomainFakeStore{routeLimits: limits}}}
}

// --- ServerHandler.isSnapshotFetchHostAllowed (SSRF allowlist) ---

func newServerSnapshotHandler(settings map[string]string) *ServerHandler {
	return &ServerHandler{state: &AppState{Store: &gatewayDomainFakeStore{settings: settings}}}
}

// TestIsSnapshotFetchHostAllowed pins the SSRF host-allowlist gate
// (server_modpack_snapshot.go:140): cdn.modrinth.com or the configured
// Solder-mirror host, everything else refused.
func TestIsSnapshotFetchHostAllowed(t *testing.T) {
	localMirrorSettings := map[string]string{
		"modpack_storage_provider": "local",
		"core_public_url":          "https://core.example.com",
	}

	t.Run("modrinth cdn is allowed", func(t *testing.T) {
		h := newServerSnapshotHandler(localMirrorSettings)
		if !h.isSnapshotFetchHostAllowed("https://cdn.modrinth.com/data/abc/file.mrpack") {
			t.Fatalf("expected cdn.modrinth.com to be allowed")
		}
	})

	t.Run("configured local solder-mirror host is allowed", func(t *testing.T) {
		h := newServerSnapshotHandler(localMirrorSettings)
		if !h.isSnapshotFetchHostAllowed("https://core.example.com/solder/mirror/pack.mrpack") {
			t.Fatalf("expected the configured core_public_url host to be allowed")
		}
	})

	t.Run("configured s3 solder-mirror host is allowed", func(t *testing.T) {
		h := newServerSnapshotHandler(map[string]string{
			"modpack_storage_provider": "s3",
			"solder_delivery_mode":     "public",
			"solder_mirror_url":        "https://cdn.myhoster.example/mirror",
		})
		if !h.isSnapshotFetchHostAllowed("https://cdn.myhoster.example/mirror/pack.mrpack") {
			t.Fatalf("expected the configured S3 solder_mirror_url host to be allowed")
		}
	})

	t.Run("arbitrary host is refused", func(t *testing.T) {
		h := newServerSnapshotHandler(localMirrorSettings)
		if h.isSnapshotFetchHostAllowed("https://evil.example.com/data/abc/file.mrpack") {
			t.Fatalf("expected an arbitrary host to be refused")
		}
	})

	t.Run("garbage URL is refused", func(t *testing.T) {
		h := newServerSnapshotHandler(localMirrorSettings)
		if h.isSnapshotFetchHostAllowed("%zz") {
			t.Fatalf("expected an unparseable URL to be refused")
		}
	})

	// NOTE (real behavior, not a fix): isSnapshotFetchHostAllowed only ever
	// inspects u.Hostname() - it does not check u.Scheme at all. An
	// http:// URL to the same allowlisted host passes this gate exactly like
	// https://. This is not a standalone SSRF hole: the actual fetch goes
	// through services.SafeFetch, which independently rejects any scheme
	// other than "http"/"https" - so http is a deliberately still-allowed
	// scheme downstream, just not vetted a second time at this layer.
	t.Run("http scheme on an allowed host is NOT rejected by this gate (scheme is not checked here)", func(t *testing.T) {
		h := newServerSnapshotHandler(localMirrorSettings)
		if !h.isSnapshotFetchHostAllowed("http://cdn.modrinth.com/data/abc/file.mrpack") {
			t.Fatalf("expected http scheme on an allowed host to still pass this host-only gate")
		}
	})
}

// TestCnameLabelValidation pins that the custom-domain CNAME setting is a single
// DNS LABEL, not a full domain. It is prefixed onto every hoster base, so a
// value like "route.eu.example.com" would silently build
// "route.eu.example.com.eu.example.com" - a name that resolves nowhere and only
// surfaces as a customer whose CNAME never works. Save-side validation uses the
// same subRegexDNS the subdomain picker uses, so this pins the shared contract.
func TestCnameLabelValidation(t *testing.T) {
	valid := []string{"route", "connect", "mc", "r", "play-here", "a1"}
	for _, v := range valid {
		if !subRegexDNS.MatchString(v) {
			t.Errorf("label %q should be accepted as a CNAME label", v)
		}
	}

	invalid := []string{
		"route.eu.example.com", // the whole point: a full domain is rejected
		"route.eu",
		".route",
		"route.",
		"-route",  // a DNS label may not start with a hyphen
		"route-",  // ...nor end with one
		"ROUTE",   // callers lowercase first; the raw form must not slip through
		"ro ute",
		"route_1",
	}
	for _, v := range invalid {
		if subRegexDNS.MatchString(v) {
			t.Errorf("label %q should be rejected as a CNAME label", v)
		}
	}
}

// TestRouteIsReservedByDefault pins that "route" cannot be claimed as a user
// subdomain. It is the default CNAME label, so a user holding route.<base>
// would hijack the exact name every other user is told to point their own
// domain at.
func TestRouteIsReservedByDefault(t *testing.T) {
	h := newGatewayDomainHandler(nil)
	if !h.loadBlockedRoutePrefixes()["route"] {
		t.Fatal("'route' must be in the default reserved-prefix list")
	}
}
