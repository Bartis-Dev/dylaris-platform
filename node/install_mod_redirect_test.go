package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Core pins the download host to a Modrinth CDN before this command is queued.
// With Go's default redirect policy that pin covered the first hop only, so a
// single 302 off cdn.modrinth.com would have fetched from anywhere - and the
// sha512 is only checked when the caller supplied one, which is optional.
func TestDownloadAndVerify_RedirectPolicy(t *testing.T) {
	payload := []byte("jar-bytes")

	t.Run("a redirect to another host is refused", func(t *testing.T) {
		elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("attacker-bytes"))
		}))
		defer elsewhere.Close()

		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL+"/x.jar", http.StatusFound)
		}))
		defer origin.Close()

		dest := filepath.Join(t.TempDir(), "x.jar")
		err := downloadAndVerify(origin.URL+"/x.jar", dest, "")
		if err == nil {
			t.Fatal("the download followed a redirect off the pinned host")
		}
		if !strings.Contains(err.Error(), "different host") {
			t.Errorf("err = %v, want it to name the reason", err)
		}
		if b, rerr := os.ReadFile(dest); rerr == nil && strings.Contains(string(b), "attacker") {
			t.Error("the attacker's bytes were written to disk")
		}
	})

	t.Run("a redirect on the same host still works", func(t *testing.T) {
		// CDNs rewrite paths; that must keep working or this guard would be
		// traded for an outage the first time one does.
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/x.jar" {
				http.Redirect(w, r, srv.URL+"/real/x.jar", http.StatusFound)
				return
			}
			w.Write(payload)
		}))
		defer srv.Close()

		dest := filepath.Join(t.TempDir(), "x.jar")
		if err := downloadAndVerify(srv.URL+"/x.jar", dest, ""); err != nil {
			t.Fatalf("a same-host redirect failed: %v", err)
		}
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(got) != string(payload) {
			t.Errorf("content = %q, want %q", got, payload)
		}
	})

	t.Run("no redirect at all is the normal case", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(payload)
		}))
		defer srv.Close()

		dest := filepath.Join(t.TempDir(), "x.jar")
		if err := downloadAndVerify(srv.URL+"/x.jar", dest, ""); err != nil {
			t.Fatalf("a direct download failed: %v", err)
		}
	})
}
