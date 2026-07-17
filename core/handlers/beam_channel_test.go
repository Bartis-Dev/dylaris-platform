package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dylaris-core/store"
)

func TestBeamDevChannelAllowed(t *testing.T) {
	cases := []struct {
		name    string
		policy  string
		isAdmin bool
		want    bool
	}{
		{"disabled blocks admin", "disabled", true, false},
		{"disabled blocks user", "disabled", false, false},
		{"empty blocks (fail-safe)", "", true, false},
		{"unknown blocks (fail-safe)", "everyone!", true, false},
		{"admins-only admits admin", "admins-only", true, true},
		{"admins-only blocks user", "admins-only", false, false},
		{"all-users admits admin", "all-users", true, true},
		{"all-users admits user", "all-users", false, true},
	}
	for _, c := range cases {
		if got := beamDevChannelAllowed(c.policy, c.isAdmin); got != c.want {
			t.Errorf("%s: beamDevChannelAllowed(%q,%v) = %v want %v", c.name, c.policy, c.isAdmin, got, c.want)
		}
	}
}

func TestResolveBeamChannel(t *testing.T) {
	cases := []struct {
		name         string
		pref, policy string
		isAdmin      bool
		want         string
	}{
		{"stable pref stays stable", "stable", "all-users", true, "stable"},
		{"dev pref allowed -> dev", "dev", "all-users", false, "dev"},
		{"dev pref admin-only admin -> dev", "dev", "admins-only", true, "dev"},
		{"dev pref admin-only user -> stable", "dev", "admins-only", false, "stable"},
		{"dev pref disabled -> stable", "dev", "disabled", true, "stable"},
		{"dev pref empty policy -> stable", "dev", "", true, "stable"},
		{"garbage pref -> stable", "weird", "all-users", true, "stable"},
	}
	for _, c := range cases {
		if got := resolveBeamChannel(c.pref, c.policy, c.isAdmin); got != c.want {
			t.Errorf("%s: resolveBeamChannel(%q,%q,%v) = %q want %q", c.name, c.pref, c.policy, c.isAdmin, got, c.want)
		}
	}
}

func TestValidBeamChannel(t *testing.T) {
	for _, ch := range []string{"stable", "dev"} {
		if !validBeamChannel(ch) {
			t.Errorf("validBeamChannel(%q) = false, want true", ch)
		}
	}
	for _, ch := range []string{"", "DEV", "beta", "nightly"} {
		if validBeamChannel(ch) {
			t.Errorf("validBeamChannel(%q) = true, want false", ch)
		}
	}
}

// beamChannelFakeStore embeds store.Store (nil) and overrides only what the
// channel handlers touch: GetSetting, GetUserBeamChannel, SetUserBeamChannel.
type beamChannelFakeStore struct {
	store.Store
	policy     string
	channel    string
	setCalls   []string
	getUserErr error
	setUserErr error
}

func (f *beamChannelFakeStore) GetSetting(key string) (string, error) {
	if key == "beam.dev_channel_access" {
		return f.policy, nil
	}
	return "", nil
}

func (f *beamChannelFakeStore) GetUserBeamChannel(userID string) (string, error) {
	if f.getUserErr != nil {
		return "", f.getUserErr
	}
	if f.channel == "" {
		return "stable", nil
	}
	return f.channel, nil
}

func (f *beamChannelFakeStore) SetUserBeamChannel(userID, channel string) error {
	if f.setUserErr != nil {
		return f.setUserErr
	}
	f.setCalls = append(f.setCalls, channel)
	f.channel = channel
	return nil
}

func beamChannelReq(method string, body []byte, isAdmin bool) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, "/api/me/beam-channel", bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, "/api/me/beam-channel", nil)
	}
	ctx := context.WithValue(r.Context(), "userID", "user-1")
	ctx = context.WithValue(ctx, "isAdmin", isAdmin)
	return r.WithContext(ctx)
}

func TestSetMyBeamChannel(t *testing.T) {
	cases := []struct {
		name       string
		policy     string
		isAdmin    bool
		body       string
		wantStatus int
		wantStored string // "" = nothing stored
	}{
		{"stable always ok", "disabled", false, `{"channel":"stable"}`, 200, "stable"},
		{"dev blocked when disabled", "disabled", true, `{"channel":"dev"}`, 403, ""},
		{"dev ok for admin under admins-only", "admins-only", true, `{"channel":"dev"}`, 200, "dev"},
		{"dev blocked for user under admins-only", "admins-only", false, `{"channel":"dev"}`, 403, ""},
		{"dev ok under all-users", "all-users", false, `{"channel":"dev"}`, 200, "dev"},
		{"invalid channel rejected", "all-users", true, `{"channel":"nightly"}`, 400, ""},
		{"case-normalized dev ok", "all-users", false, `{"channel":"DEV"}`, 200, "dev"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := &beamChannelFakeStore{policy: c.policy}
			h := &BeamHandler{state: &AppState{Store: fs}}
			rec := httptest.NewRecorder()
			h.SetMyBeamChannel(rec, beamChannelReq("PUT", []byte(c.body), c.isAdmin))
			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, c.wantStatus, rec.Body.String())
			}
			if c.wantStored == "" {
				if len(fs.setCalls) != 0 {
					t.Errorf("expected no store write, got %v", fs.setCalls)
				}
			} else {
				if len(fs.setCalls) != 1 || fs.setCalls[0] != c.wantStored {
					t.Errorf("stored = %v, want [%s]", fs.setCalls, c.wantStored)
				}
			}
		})
	}
}

func TestGetMyBeamChannel(t *testing.T) {
	fs := &beamChannelFakeStore{policy: "admins-only", channel: "dev"}
	h := &BeamHandler{state: &AppState{Store: fs}}

	// Admin: dev is allowed, so effective follows the dev pref.
	rec := httptest.NewRecorder()
	h.GetMyBeamChannel(rec, beamChannelReq("GET", nil, true))
	if rec.Code != 200 {
		t.Fatalf("admin GET status = %d", rec.Code)
	}
	var adminResp struct {
		Channel          string `json:"channel"`
		EffectiveChannel string `json:"effective_channel"`
		DevAllowed       bool   `json:"dev_allowed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &adminResp); err != nil {
		t.Fatal(err)
	}
	if adminResp.Channel != "dev" || adminResp.EffectiveChannel != "dev" || !adminResp.DevAllowed {
		t.Errorf("admin resp = %+v, want channel dev / effective dev / allowed true", adminResp)
	}

	// Non-admin under admins-only: pref stays 'dev' but effective clamps to stable
	// and dev is not allowed.
	rec2 := httptest.NewRecorder()
	h.GetMyBeamChannel(rec2, beamChannelReq("GET", nil, false))
	var userResp struct {
		Channel          string `json:"channel"`
		EffectiveChannel string `json:"effective_channel"`
		DevAllowed       bool   `json:"dev_allowed"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &userResp); err != nil {
		t.Fatal(err)
	}
	if userResp.Channel != "dev" || userResp.EffectiveChannel != "stable" || userResp.DevAllowed {
		t.Errorf("user resp = %+v, want channel dev / effective stable / allowed false", userResp)
	}
}
