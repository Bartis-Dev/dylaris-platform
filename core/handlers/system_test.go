package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetCoreInfo_IncludesTabProxyFields(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		active bool
	}{
		{"isolation active", "https://mc.example.com:25502", true},
		{"isolation inactive", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewSystemHandler("eu", "core-1", tc.origin, tc.active)
			rec := httptest.NewRecorder()

			h.GetCoreInfo(rec, httptest.NewRequest("GET", "/api/system/core-info", nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var body struct {
				Success                 bool   `json:"success"`
				Region                  string `json:"region"`
				CoreID                  string `json:"coreId"`
				TabProxyOrigin          string `json:"tabProxyOrigin"`
				TabProxyIsolationActive bool   `json:"tabProxyIsolationActive"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !body.Success || body.Region != "eu" || body.CoreID != "core-1" {
				t.Errorf("base fields wrong: %+v", body)
			}
			if body.TabProxyOrigin != tc.origin || body.TabProxyIsolationActive != tc.active {
				t.Errorf("tabProxy fields = (%q,%v), want (%q,%v)", body.TabProxyOrigin, body.TabProxyIsolationActive, tc.origin, tc.active)
			}
		})
	}
}
