package handlers

import "testing"

func TestClassifyReachable(t *testing.T) {
	tt := []struct {
		name   string
		status int
		err    error
		want   *bool
	}{
		{"transport error is unknown", 0, errTest, nil},
		{"401 is not reachable", 401, nil, boolp(false)},
		{"403 is not reachable", 403, nil, boolp(false)},
		{"200 is reachable", 200, nil, boolp(true)},
		{"404 is reachable", 404, nil, boolp(true)},
		{"302 is reachable", 302, nil, boolp(true)},
	}
	for _, c := range tt {
		t.Run(c.name, func(t *testing.T) {
			got := classifyReachable(c.status, c.err)
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
		wantPubConfig bool
		wantPresNote  bool
		wantPubNote   bool
	}{
		{"nothing configured", false, "", nil, false, true, true},
		{"presign ok, no mirror", true, "", nil, false, false, true},
		{"valid mirror, reachable", true, "https://cdn.example.com/m", &tr, true, false, false},
		{"valid mirror, private (403)", true, "https://cdn.example.com/m", &f, true, false, true},
		{"invalid mirror url", true, "not-a-url", nil, false, false, true},
		{"valid mirror, reachability unknown", true, "https://cdn.example.com/m", nil, true, false, false},
	}
	for _, c := range tt {
		t.Run(c.name, func(t *testing.T) {
			caps := buildDeliveryCapabilities(c.canPresign, c.mirror, c.reach)
			if caps.PublicConfigured != c.wantPubConfig {
				t.Errorf("publicConfigured = %v, want %v", caps.PublicConfigured, c.wantPubConfig)
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
