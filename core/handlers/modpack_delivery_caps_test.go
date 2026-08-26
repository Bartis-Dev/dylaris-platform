package handlers

import "testing"

func TestClassifyReachable(t *testing.T) {
	tt := []struct {
		name     string
		status   int
		err      error
		isObject bool
		want     *bool
	}{
		// Probing the BASE: only an unambiguous refusal counts against it,
		// because a correctly configured public bucket 404s on its base path.
		{"transport error is unknown", 0, errTest, false, nil},
		{"base: 401 is not reachable", 401, nil, false, boolp(false)},
		{"base: 403 is not reachable", 403, nil, false, boolp(false)},
		{"base: 200 is reachable", 200, nil, false, boolp(true)},
		{"base: 404 is reachable", 404, nil, false, boolp(true)},
		{"base: 302 is reachable", 302, nil, false, boolp(true)},

		// Probing a REAL published mod file answers the actual question - can a
		// player download this - so only a 2xx is a yes. R2's S3 endpoint
		// answers 400 to any unauthenticated request, measured against the real
		// bucket, and the old base-only rule called that reachable.
		{"object: 200 is reachable", 200, nil, true, boolp(true)},
		{"object: 206 is reachable", 206, nil, true, boolp(true)},
		{"object: 400 is NOT reachable", 400, nil, true, boolp(false)},
		{"object: 401 is NOT reachable", 401, nil, true, boolp(false)},
		{"object: 403 is NOT reachable", 403, nil, true, boolp(false)},
		{"object: 404 is NOT reachable", 404, nil, true, boolp(false)},
		{"object: 302 is NOT reachable", 302, nil, true, boolp(false)},
		{"object: transport error stays unknown", 0, errTest, true, nil},
	}
	for _, c := range tt {
		t.Run(c.name, func(t *testing.T) {
			got := classifyReachable(c.status, c.err, c.isObject)
			if (got == nil) != (c.want == nil) {
				t.Fatalf("nil mismatch: got %v want %v", got, c.want)
			}
			if got != nil && *got != *c.want {
				t.Errorf("got %v want %v", *got, *c.want)
			}
		})
	}
}

func TestBuildDeliveryCapabilities(t *testing.T) {
	f, tr := false, true
	tt := []struct {
		name          string
		canPresign    bool
		mirror        string
		reach         *bool
		privatePacks  int
		wantPubConfig bool
		wantPresNote  bool
		wantPubNote   bool
	}{
		{"nothing configured", false, "", nil, 0, false, true, true},
		{"presign ok, no mirror", true, "", nil, 0, false, false, true},
		{"valid mirror, reachable", true, "https://cdn.example.com/m", &tr, 0, true, false, false},
		{"valid mirror, private (403)", true, "https://cdn.example.com/m", &f, 0, true, false, true},
		{"invalid mirror url", true, "not-a-url", nil, 0, false, false, true},
		{"valid mirror, reachability unknown", true, "https://cdn.example.com/m", nil, 0, true, false, false},
		{"private pack count passed through", true, "https://cdn.example.com/m", &tr, 3, true, false, false},
	}
	for _, c := range tt {
		t.Run(c.name, func(t *testing.T) {
			// storageConfigured is exercised by its own test below; these cases
			// are about the mirror and presign notes.
			caps := buildDeliveryCapabilities(c.canPresign, c.mirror, c.reach, c.privatePacks, true)
			if caps.PublicConfigured != c.wantPubConfig {
				t.Errorf("publicConfigured = %v, want %v", caps.PublicConfigured, c.wantPubConfig)
			}
			if caps.PrivatePackCount != c.privatePacks {
				t.Errorf("privatePackCount = %d, want %d", caps.PrivatePackCount, c.privatePacks)
			}
			if _, ok := caps.Notes["presigned"]; ok != c.wantPresNote {
				t.Errorf("presigned note present = %v, want %v", ok, c.wantPresNote)
			}
			if _, ok := caps.Notes["public"]; ok != c.wantPubNote {
				t.Errorf("public note present = %v, want %v", ok, c.wantPubNote)
			}
		})
	}
}

var errTest = errString("boom")

type errString string

func (e errString) Error() string { return string(e) }

func boolp(b bool) *bool { return &b }

// The rule this decides: an operator learns that modpack storage is unconfigured
// from the settings screen, not from a 424 at the end of building a pack.
//
// Every write path already calls buildModpackStorageProvider and turns a nil
// provider into an HTTP 424. Nothing surfaced that beforehand, so the failure
// arrived after the work rather than instead of it.
func TestDeliveryCapabilitiesReportUnconfiguredStorage(t *testing.T) {
	caps := buildDeliveryCapabilities(false, "", nil, 0, false)
	if caps.StorageConfigured {
		t.Error("storageConfigured = true with no provider")
	}
	if _, ok := caps.Notes["storage"]; !ok {
		t.Error("no note said what to do about it")
	}

	caps = buildDeliveryCapabilities(true, "", nil, 0, true)
	if !caps.StorageConfigured {
		t.Error("storageConfigured = false with a working provider")
	}
	if _, ok := caps.Notes["storage"]; ok {
		t.Error("a configured backend still carried the unconfigured note")
	}
}
