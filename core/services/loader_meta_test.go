package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// roundTripFunc adapts a function to http.RoundTripper so tests can stub
// loaderMetaClient without a real listener.
type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// stubTransport serves canned responses keyed by exact request URL and
// records every URL requested, so tests can assert both the response
// handling and which endpoints were (or were not) hit.
type stubTransport struct {
	byURL map[string]struct {
		status int
		body   string
	}
	calls []string
}

func (rt *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL.String()
	rt.calls = append(rt.calls, u)
	resp, ok := rt.byURL[u]
	if !ok {
		return nil, fmt.Errorf("stubTransport: unexpected request to %s", u)
	}
	return &http.Response{
		StatusCode: resp.status,
		Body:       io.NopCloser(strings.NewReader(resp.body)),
		Header:     make(http.Header),
	}, nil
}

// withLoaderMetaClient swaps the package-level HTTP client for the duration
// of a test and restores it afterwards. loaderMetaClient is a package var
// specifically so callers can bound it; using that existing seam here, not
// changing production code.
func withLoaderMetaClient(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	orig := loaderMetaClient
	loaderMetaClient = &http.Client{Transport: rt}
	t.Cleanup(func() { loaderMetaClient = orig })
}

func TestResolveLatestStableFabric_PicksFirstStable(t *testing.T) {
	st := &stubTransport{byURL: map[string]struct {
		status int
		body   string
	}{
		"https://meta.fabricmc.net/v2/versions/loader/1.20.1": {200, `[
			{"loader":{"version":"0.9.0","stable":false}},
			{"loader":{"version":"0.10.0","stable":true}},
			{"loader":{"version":"0.11.0","stable":true}}
		]`},
	}}
	withLoaderMetaClient(t, st)

	got, err := resolveLatestStableFabric(context.Background(), "1.20.1")
	if err != nil {
		t.Fatalf("resolveLatestStableFabric: %v", err)
	}
	if got != "0.10.0" {
		t.Errorf("got %q, want first stable entry 0.10.0", got)
	}
}

func TestResolveLatestStableFabric_FallsBackToFirstWhenNoneStable(t *testing.T) {
	st := &stubTransport{byURL: map[string]struct {
		status int
		body   string
	}{
		"https://meta.fabricmc.net/v2/versions/loader/1.20.1": {200, `[
			{"loader":{"version":"0.9.0","stable":false}},
			{"loader":{"version":"0.9.1","stable":false}}
		]`},
	}}
	withLoaderMetaClient(t, st)

	got, err := resolveLatestStableFabric(context.Background(), "1.20.1")
	if err != nil {
		t.Fatalf("resolveLatestStableFabric: %v", err)
	}
	if got != "0.9.0" {
		t.Errorf("got %q, want fallback to first entry 0.9.0", got)
	}
}

func TestResolveLatestStableFabric_EmptyList_Error(t *testing.T) {
	st := &stubTransport{byURL: map[string]struct {
		status int
		body   string
	}{
		"https://meta.fabricmc.net/v2/versions/loader/1.20.1": {200, `[]`},
	}}
	withLoaderMetaClient(t, st)

	if _, err := resolveLatestStableFabric(context.Background(), "1.20.1"); err == nil {
		t.Fatal("expected error for empty loader list")
	}
}

func TestResolveLatestStableFabric_HTTPError(t *testing.T) {
	st := &stubTransport{byURL: map[string]struct {
		status int
		body   string
	}{
		"https://meta.fabricmc.net/v2/versions/loader/1.20.1": {500, "boom"},
	}}
	withLoaderMetaClient(t, st)

	if _, err := resolveLatestStableFabric(context.Background(), "1.20.1"); err == nil {
		t.Fatal("expected error on non-200 status")
	}
}

func TestResolveLatestStableQuilt_PicksFirstNonBeta(t *testing.T) {
	st := &stubTransport{byURL: map[string]struct {
		status int
		body   string
	}{
		"https://meta.quiltmc.org/v3/versions/loader/1.20.1": {200, `[
			{"loader":{"version":"1.0.0-beta.1"}},
			{"loader":{"version":"1.0.0"}},
			{"loader":{"version":"1.1.0"}}
		]`},
	}}
	withLoaderMetaClient(t, st)

	got, err := resolveLatestStableQuilt(context.Background(), "1.20.1")
	if err != nil {
		t.Fatalf("resolveLatestStableQuilt: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("got %q, want first non-beta 1.0.0", got)
	}
}

func TestResolveLatestStableQuilt_CaseInsensitiveBetaMatch(t *testing.T) {
	st := &stubTransport{byURL: map[string]struct {
		status int
		body   string
	}{
		"https://meta.quiltmc.org/v3/versions/loader/1.20.1": {200, `[
			{"loader":{"version":"1.0.0-BETA.2"}},
			{"loader":{"version":"1.2.0"}}
		]`},
	}}
	withLoaderMetaClient(t, st)

	got, err := resolveLatestStableQuilt(context.Background(), "1.20.1")
	if err != nil {
		t.Fatalf("resolveLatestStableQuilt: %v", err)
	}
	if got != "1.2.0" {
		t.Errorf("got %q, want 1.2.0 (uppercase -BETA- must still be treated as unstable)", got)
	}
}

func TestResolveLatestStableQuilt_FallsBackToFirstWhenAllBeta(t *testing.T) {
	st := &stubTransport{byURL: map[string]struct {
		status int
		body   string
	}{
		"https://meta.quiltmc.org/v3/versions/loader/1.20.1": {200, `[
			{"loader":{"version":"1.0.0-beta.1"}},
			{"loader":{"version":"1.0.0-beta.2"}}
		]`},
	}}
	withLoaderMetaClient(t, st)

	got, err := resolveLatestStableQuilt(context.Background(), "1.20.1")
	if err != nil {
		t.Fatalf("resolveLatestStableQuilt: %v", err)
	}
	if got != "1.0.0-beta.1" {
		t.Errorf("got %q, want fallback to first entry 1.0.0-beta.1", got)
	}
}

func TestResolveLatestStableQuilt_EmptyList_Error(t *testing.T) {
	st := &stubTransport{byURL: map[string]struct {
		status int
		body   string
	}{
		"https://meta.quiltmc.org/v3/versions/loader/1.20.1": {200, `[]`},
	}}
	withLoaderMetaClient(t, st)

	if _, err := resolveLatestStableQuilt(context.Background(), "1.20.1"); err == nil {
		t.Fatal("expected error for empty loader list")
	}
}

func TestFetchLoaderProfile_Success(t *testing.T) {
	const url = "https://meta.fabricmc.net/v2/versions/loader/1.20.1/0.10.0/profile/json"
	st := &stubTransport{byURL: map[string]struct {
		status int
		body   string
	}{
		url: {200, `{"id":"fabric-loader-0.10.0-1.20.1"}`},
	}}
	withLoaderMetaClient(t, st)

	body, err := fetchLoaderProfile(context.Background(), url)
	if err != nil {
		t.Fatalf("fetchLoaderProfile: %v", err)
	}
	if string(body) != `{"id":"fabric-loader-0.10.0-1.20.1"}` {
		t.Errorf("body = %s, want profile JSON verbatim", body)
	}
}

func TestFetchLoaderProfile_NonOKStatus_Error(t *testing.T) {
	const url = "https://meta.fabricmc.net/v2/versions/loader/1.20.1/0.10.0/profile/json"
	st := &stubTransport{byURL: map[string]struct {
		status int
		body   string
	}{
		url: {404, "not found"},
	}}
	withLoaderMetaClient(t, st)

	if _, err := fetchLoaderProfile(context.Background(), url); err == nil {
		t.Fatal("expected error on 404 status")
	}
}

func TestTruncateProfileErr(t *testing.T) {
	short := "short body"
	long := strings.Repeat("x", 500)

	if got := truncateProfileErr([]byte(short)); got != short {
		t.Errorf("short body should be returned as-is, got %q", got)
	}
	got := truncateProfileErr([]byte(long))
	if len(got) != 300 {
		t.Errorf("long body should be truncated to 300 bytes, got %d", len(got))
	}
	if got != long[:300] {
		t.Errorf("truncated body should match the first 300 bytes exactly")
	}
}
