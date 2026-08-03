package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dylaris-core/services"
	"dylaris-core/store"
)

// nodeEnrollFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only GetSetting (read by services.FeatureFlags
// for the byonActive gate) and CreateNodeEnrollToken (what MintToken writes)
// are overridden. Any other call would panic - these tests never make one.
type nodeEnrollFakeStore struct {
	store.Store

	settings map[string]string

	createCalls []nodeEnrollCreateCall
	createErr   error
}

type nodeEnrollCreateCall struct {
	userID    string
	plaintext string
	label     string
	expiresAt *time.Time
}

func (f *nodeEnrollFakeStore) GetSetting(key string) (string, error) {
	return f.settings[key], nil
}

func (f *nodeEnrollFakeStore) CreateNodeEnrollToken(userID, plaintext, label string, expiresAt *time.Time) error {
	f.createCalls = append(f.createCalls, nodeEnrollCreateCall{userID, plaintext, label, expiresAt})
	return f.createErr
}

// newNodeEnrollState builds an AppState with a real *services.FeatureFlags
// backed by the fake store (feature_byon_enabled drives byonActive), plus
// the GRPC TLS fields MintToken's fingerprint-disclosure gate reads.
func newNodeEnrollState(fs *nodeEnrollFakeStore, byonEnabled, grpcTLSEnabled bool, grpcFingerprint string) *AppState {
	if fs.settings == nil {
		fs.settings = map[string]string{}
	}
	fs.settings["feature_byon_enabled"] = "false"
	if byonEnabled {
		fs.settings["feature_byon_enabled"] = "true"
	}
	return &AppState{
		Store:              fs,
		FeatureFlags:       services.NewFeatureFlags(fs),
		GRPCTLSEnabled:     grpcTLSEnabled,
		GRPCTLSFingerprint: grpcFingerprint,
	}
}

func nodeEnrollMintReq(userID string, body map[string]interface{}) *http.Request {
	var r *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest("POST", "/api/nodes/enroll-token", bytes.NewReader(b))
	} else {
		r = httptest.NewRequest("POST", "/api/nodes/enroll-token", nil)
	}
	if userID != "" {
		r = r.WithContext(context.WithValue(r.Context(), "userID", userID))
	}
	return r
}

func TestMintToken_BYONInactiveForbidden(t *testing.T) {
	fs := &nodeEnrollFakeStore{}
	state := newNodeEnrollState(fs, false, false, "")
	h := NewNodeEnrollHandler(state)
	rec := httptest.NewRecorder()

	h.MintToken(rec, nodeEnrollMintReq("u1", map[string]interface{}{}))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if len(fs.createCalls) != 0 {
		t.Fatalf("expected no CreateNodeEnrollToken call, got %+v", fs.createCalls)
	}
}

func TestMintToken_UnauthorizedWhenNoCaller(t *testing.T) {
	fs := &nodeEnrollFakeStore{}
	state := newNodeEnrollState(fs, true, false, "")
	h := NewNodeEnrollHandler(state)
	rec := httptest.NewRecorder()

	h.MintToken(rec, nodeEnrollMintReq("", map[string]interface{}{}))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if len(fs.createCalls) != 0 {
		t.Fatalf("expected no CreateNodeEnrollToken call, got %+v", fs.createCalls)
	}
}

// TestMintToken_ExpiryClamp pins the exact clamp boundaries from
// node_enroll.go: days<=0 -> 7, days>30 -> 30, and the cap itself (30) is a
// valid pass-through value (not >, so it is not re-clamped).
func TestMintToken_ExpiryClamp(t *testing.T) {
	cases := []struct {
		name     string
		days     int
		wantDays int
	}{
		{"zero clamps to the 7-day default", 0, 7},
		{"negative clamps to the 7-day default", -5, 7},
		{"1 day passes through unchanged", 1, 1},
		{"exactly at the 30-day cap passes through unchanged", 30, 30},
		{"31 (just over the cap) clamps down to 30", 31, 30},
		{"999 clamps down to the 30-day cap", 999, 30},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := &nodeEnrollFakeStore{}
			state := newNodeEnrollState(fs, true, false, "")
			h := NewNodeEnrollHandler(state)
			rec := httptest.NewRecorder()

			before := time.Now()
			h.MintToken(rec, nodeEnrollMintReq("u1", map[string]interface{}{"expiresDays": c.days}))
			after := time.Now()

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if len(fs.createCalls) != 1 {
				t.Fatalf("expected 1 CreateNodeEnrollToken call, got %d", len(fs.createCalls))
			}
			call := fs.createCalls[0]
			if call.expiresAt == nil {
				t.Fatalf("expiresAt is nil, want a clamped expiry")
			}
			wantLower := before.AddDate(0, 0, c.wantDays).Add(-2 * time.Second)
			wantUpper := after.AddDate(0, 0, c.wantDays).Add(2 * time.Second)
			if call.expiresAt.Before(wantLower) || call.expiresAt.After(wantUpper) {
				t.Fatalf("expiresAt = %v, want within [%v, %v] (now+%d days)", call.expiresAt, wantLower, wantUpper, c.wantDays)
			}
		})
	}
}

// TestMintToken_FingerprintDisclosureGate pins that the response only ever
// carries grpcTlsFingerprint when GRPCTLSEnabled is true - showing pinning
// material the control channel does not actually enforce would be
// misleading.
func TestMintToken_FingerprintDisclosureGate(t *testing.T) {
	cases := []struct {
		name           string
		grpcTLSEnabled bool
		fingerprint    string
		wantInBody     string
	}{
		{"TLS disabled: fingerprint withheld even if one is configured", false, "aa:bb:cc", ""},
		{"TLS enabled: configured fingerprint is disclosed", true, "aa:bb:cc", "aa:bb:cc"},
		{"TLS enabled but no fingerprint derived: empty string disclosed", true, "", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := &nodeEnrollFakeStore{}
			state := newNodeEnrollState(fs, true, c.grpcTLSEnabled, c.fingerprint)
			h := NewNodeEnrollHandler(state)
			rec := httptest.NewRecorder()

			h.MintToken(rec, nodeEnrollMintReq("u1", map[string]interface{}{}))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			var resp struct {
				GRPCTLSFingerprint string `json:"grpcTlsFingerprint"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.GRPCTLSFingerprint != c.wantInBody {
				t.Fatalf("grpcTlsFingerprint = %q, want %q", resp.GRPCTLSFingerprint, c.wantInBody)
			}
		})
	}
}

// TestMintToken_BodyHandling pins the two halves of the decode contract, which
// have to be distinguished rather than collapsed: BOTH fields are optional, so
// an empty body legitimately means "mint one with the defaults" (io.EOF), while
// anything else is malformed and must not mint.
//
// This previously documented the opposite - the decode error was discarded
// entirely, so a caller who sent `{"expiresDays": 30}` with a typo elsewhere in
// the JSON got a silent 7-day token and no way to tell. Rejecting outright
// would have been just as wrong, because it would break the no-body call.
func TestMintToken_BodyHandling(t *testing.T) {
	tests := []struct {
		name        string
		body        []byte
		wantStatus  int
		wantCreates int
	}{
		{"malformed", []byte("not json"), http.StatusBadRequest, 0},
		{"truncated", []byte(`{"label":`), http.StatusBadRequest, 0},
		{"empty body means defaults", nil, http.StatusOK, 1},
		{"explicit empty object", []byte(`{}`), http.StatusOK, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := &nodeEnrollFakeStore{}
			state := newNodeEnrollState(fs, true, false, "")
			h := NewNodeEnrollHandler(state)
			r := httptest.NewRequest("POST", "/api/nodes/enroll-token", bytes.NewReader(tt.body))
			r = r.WithContext(context.WithValue(r.Context(), "userID", "u1"))
			rec := httptest.NewRecorder()

			h.MintToken(rec, r)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if len(fs.createCalls) != tt.wantCreates {
				t.Fatalf("CreateNodeEnrollToken calls = %d, want %d", len(fs.createCalls), tt.wantCreates)
			}
			// A rejected body must not mint anything - the whole point of the
			// 400 is that no credential is issued for input we could not read.
			if tt.wantStatus == http.StatusOK && fs.createCalls[0].label != "" {
				t.Fatalf("label = %q, want empty (zero-value request)", fs.createCalls[0].label)
			}
		})
	}
}

func TestMintToken_StoreErrorIsInternalServerError(t *testing.T) {
	fs := &nodeEnrollFakeStore{createErr: errors.New("db down")}
	state := newNodeEnrollState(fs, true, false, "")
	h := NewNodeEnrollHandler(state)
	rec := httptest.NewRecorder()

	h.MintToken(rec, nodeEnrollMintReq("u1", map[string]interface{}{}))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
}
