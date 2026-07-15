package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRequireStoreKey pins the store-linking service-to-service trust gate
// (store.go: StoreHandler.requireStoreKey). It is the ONLY thing standing
// between an unauthenticated caller and the store<->core surface (link
// verify, verify-user, usage, provision), so the constant-time compare +
// StoreEnabled gate matter: wrong key, missing key, and integration-disabled
// must all reject, and an unconfigured (empty) shared key must fail closed
// rather than accept an empty header as a "match".
func TestRequireStoreKey(t *testing.T) {
	cases := []struct {
		name         string
		storeEnabled bool
		sharedKey    string
		setHeader    bool
		header       string
		want         bool
		wantStatus   int
		wantBodySub  string
	}{
		{
			name:         "store integration disabled rejects even with the correct key",
			storeEnabled: false,
			sharedKey:    "secret",
			setHeader:    true,
			header:       "secret",
			want:         false,
			wantStatus:   http.StatusNotFound,
			wantBodySub:  "Store integration not enabled",
		},
		{
			name:         "enabled with the correct key passes",
			storeEnabled: true,
			sharedKey:    "secret",
			setHeader:    true,
			header:       "secret",
			want:         true,
		},
		{
			name:         "enabled with the wrong key is rejected",
			storeEnabled: true,
			sharedKey:    "secret",
			setHeader:    true,
			header:       "wrong-key",
			want:         false,
			wantStatus:   http.StatusUnauthorized,
			wantBodySub:  "Invalid store key",
		},
		{
			name:         "enabled with a missing header is rejected",
			storeEnabled: true,
			sharedKey:    "secret",
			setHeader:    false,
			want:         false,
			wantStatus:   http.StatusUnauthorized,
			wantBodySub:  "Invalid store key",
		},
		{
			name:         "enabled with an empty configured key fails closed against an empty header",
			storeEnabled: true,
			sharedKey:    "",
			setHeader:    false,
			want:         false,
			wantStatus:   http.StatusUnauthorized,
			wantBodySub:  "Invalid store key",
		},
		{
			name:         "enabled with an empty configured key fails closed against any header value",
			storeEnabled: true,
			sharedKey:    "",
			setHeader:    true,
			header:       "anything-at-all",
			want:         false,
			wantStatus:   http.StatusUnauthorized,
			wantBodySub:  "Invalid store key",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &StoreHandler{state: &AppState{StoreEnabled: c.storeEnabled, StoreSharedKey: c.sharedKey}}
			r := httptest.NewRequest("POST", "/api/store/link/verify", nil)
			if c.setHeader {
				r.Header.Set("X-Store-Key", c.header)
			}
			rec := httptest.NewRecorder()

			got := h.requireStoreKey(rec, r)

			if got != c.want {
				t.Fatalf("requireStoreKey() = %v, want %v (body: %s)", got, c.want, rec.Body.String())
			}
			if c.want {
				// The success path writes nothing - the caller handler goes on
				// to produce the real response.
				if rec.Code != http.StatusOK {
					t.Fatalf("success path unexpectedly wrote status %d: %s", rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.wantStatus, rec.Body.String())
			}
			if c.wantBodySub != "" && !strings.Contains(rec.Body.String(), c.wantBodySub) {
				t.Fatalf("body = %s, want substring %q", rec.Body.String(), c.wantBodySub)
			}
		})
	}
}
