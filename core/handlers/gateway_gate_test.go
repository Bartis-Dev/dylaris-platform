package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gatewayEnabled is the ONLY answer to "is the gateway routing traffic": every
// gateway write gate, the migration orchestrator and the rebalance worker all
// ask it, and the panel mirrors it (panel/src/lib/api/types.ts isGatewayRouting).
// A second stored flag once shadowed this and the two sides disagreed in both
// directions, so pin the rule here.
func TestGatewayEnabledReadsRoutingModeOnly(t *testing.T) {
	tests := []struct {
		name string
		kv   map[string]string
		want bool
	}{
		{"unset defaults to ip_port", map[string]string{}, false},
		{"ip_port", map[string]string{"routing_mode": "ip_port"}, false},
		{"gateway", map[string]string{"routing_mode": "gateway"}, true},
		{"both", map[string]string{"routing_mode": "both"}, true},
		{"unparseable value is off", map[string]string{"routing_mode": "nonsense"}, false},
		{"a stored feature flag cannot turn it on", map[string]string{
			"feature_gateway_enabled": "true",
		}, false},
		{"a stored feature flag cannot turn it off", map[string]string{
			"routing_mode":            "gateway",
			"feature_gateway_enabled": "false",
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &AppState{Store: &gateFakeStore{kv: tt.kv}}
			if got := st.gatewayEnabled(); got != tt.want {
				t.Fatalf("gatewayEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The feature-settings payload must not carry a gateway flag. Re-adding one
// re-creates the split brain: the panel would gate its routes UI on a value no
// gate in Core ever reads.
func TestFeatureSettingsCarriesNoGatewayFlag(t *testing.T) {
	st := &AppState{Store: &gateFakeStore{kv: map[string]string{
		"routing_mode": "gateway",
	}}}
	h := NewSettingsHandler(st)

	rw := httptest.NewRecorder()
	h.GetFeatureSettings(rw, httptest.NewRequest(http.MethodGet, "/api/settings/features", nil))

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	if strings.Contains(strings.ToLower(rw.Body.String()), "gateway") {
		t.Fatalf("feature settings mention a gateway flag: %s", rw.Body.String())
	}

	var got struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := got.Settings["proxyEnabled"]; !ok {
		t.Fatalf("proxyEnabled missing from %v", got.Settings)
	}
	if len(got.Settings) != 1 {
		t.Fatalf("settings = %v, want only proxyEnabled", got.Settings)
	}
}
