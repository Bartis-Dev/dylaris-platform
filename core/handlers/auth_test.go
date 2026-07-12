package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// newTestAuthHandler builds an AuthHandler with only the jwtKey field set.
// IssueToken and IsAdminToken never touch h.state, so state stays nil.
func newTestAuthHandler() *AuthHandler {
	return &AuthHandler{jwtKey: []byte("test-jwt-secret-value-not-a-real-secret")}
}

func TestIssueToken_HS256(t *testing.T) {
	h := newTestAuthHandler()
	tokenString, err := h.IssueToken("alice", true)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	claims := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenString, claims, func(*jwt.Token) (interface{}, error) {
		return h.jwtKey, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !tok.Valid {
		t.Fatalf("token did not verify: err=%v valid=%v", err, tok.Valid)
	}
	if tok.Method.Alg() != "HS256" {
		t.Errorf("alg = %q, want HS256", tok.Method.Alg())
	}
	if claims.Username != "alice" {
		t.Errorf("Username = %q, want alice", claims.Username)
	}
	if !claims.IsAdmin {
		t.Error("IsAdmin = false, want true")
	}
	if claims.ExpiresAt == nil || claims.ExpiresAt.Before(time.Now().Add(23*time.Hour)) {
		t.Errorf("ExpiresAt = %v, want ~24h from now", claims.ExpiresAt)
	}
}

func TestIssueToken_NonAdmin(t *testing.T) {
	h := newTestAuthHandler()
	tokenString, err := h.IssueToken("bob", false)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	claims := &Claims{}
	if _, err := jwt.ParseWithClaims(tokenString, claims, func(*jwt.Token) (interface{}, error) {
		return h.jwtKey, nil
	}, jwt.WithValidMethods([]string{"HS256"})); err != nil {
		t.Fatalf("token did not verify: %v", err)
	}
	if claims.IsAdmin {
		t.Error("IsAdmin = true, want false")
	}
}

func TestIsAdminToken(t *testing.T) {
	h := newTestAuthHandler()
	adminTok, err := h.IssueToken("admin-user", true)
	if err != nil {
		t.Fatalf("IssueToken(admin): %v", err)
	}
	userTok, err := h.IssueToken("regular-user", false)
	if err != nil {
		t.Fatalf("IssueToken(user): %v", err)
	}

	cases := []struct {
		name    string
		makeReq func() *http.Request
		want    bool
	}{
		{"valid admin bearer token", func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			r.Header.Set("Authorization", "Bearer "+adminTok)
			return r
		}, true},
		{"valid non-admin bearer token", func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			r.Header.Set("Authorization", "Bearer "+userTok)
			return r
		}, false},
		{"admin token via query param", func() *http.Request {
			return httptest.NewRequest(http.MethodGet, "/x?token="+adminTok, nil)
		}, true},
		{"no token at all", func() *http.Request {
			return httptest.NewRequest(http.MethodGet, "/x", nil)
		}, false},
		{"garbage bearer token", func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			r.Header.Set("Authorization", "Bearer not-a-jwt")
			return r
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.IsAdminToken(tc.makeReq()); got != tc.want {
				t.Errorf("IsAdminToken = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIsAdminToken_RejectsNonHS256Alg proves the alg allowlist
// (jwt.WithValidMethods(["HS256"])) is doing real work: a token forged with
// a DIFFERENT signing algorithm but the SAME key bytes must be rejected,
// even though the signature is technically valid under that other
// algorithm. This pairs with the identical guard in AuthMiddleware.
func TestIsAdminToken_RejectsNonHS256Alg(t *testing.T) {
	h := newTestAuthHandler()
	claims := &Claims{
		Username:         "attacker",
		IsAdmin:          true,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}
	forged := jwt.NewWithClaims(jwt.SigningMethodHS384, claims)
	forgedString, err := forged.SignedString(h.jwtKey)
	if err != nil {
		t.Fatalf("sign forged token: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set("Authorization", "Bearer "+forgedString)
	if h.IsAdminToken(r) {
		t.Fatal("IsAdminToken must reject a non-HS256 token even with a valid signature under the same key")
	}
}
