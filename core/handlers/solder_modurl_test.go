package handlers

import (
	"context"
	"errors"
	"testing"
)

func settingsGetter(kv map[string]string) func(string) (string, error) {
	return func(k string) (string, error) { return kv[k], nil }
}

func TestSolderModURL(t *testing.T) {
	const key = "solder/mods/o/slug/slug-1.0.zip"
	cases := []struct {
		name       string
		kv         map[string]string
		presignURL string
		presignErr error
		gated      bool
		want       string
		wantErr    bool
	}{
		{"core builds core mirror url", map[string]string{"solder_delivery_mode": "core", "core_public_url": "https://p.example.com/"}, "", nil, false,
			"https://p.example.com/solder/mirror/" + key, false},
		{"core without core_public_url errors", map[string]string{"solder_delivery_mode": "core"}, "", nil, false, "", true},
		{"empty mode defaults to core", map[string]string{"core_public_url": "https://p.example.com"}, "", nil, false,
			"https://p.example.com/solder/mirror/" + key, false},
		{"public builds public base url", map[string]string{"solder_delivery_mode": "public", "solder_mirror_url": "https://cdn.example.com/m/"}, "", nil, false,
			"https://cdn.example.com/m/" + key, false},
		{"public without mirror url errors", map[string]string{"solder_delivery_mode": "public"}, "", nil, false, "", true},
		{"presigned returns presigned url", map[string]string{"solder_delivery_mode": "presigned"}, "https://r2.example/presigned?sig=x", nil, false,
			"https://r2.example/presigned?sig=x", false},
		{"presigned empty url errors", map[string]string{"solder_delivery_mode": "presigned"}, "", nil, false, "", true},
		{"presigned provider error errors", map[string]string{"solder_delivery_mode": "presigned"}, "", errors.New("boom"), false, "", true},
		{"public+gated+can-presign downgrades to presigned", map[string]string{"solder_delivery_mode": "public", "solder_mirror_url": "https://cdn.example.com/m/"}, "https://r2.example/pre?sig=y", nil, true,
			"https://r2.example/pre?sig=y", false},
		{"public+gated+cannot-presign downgrades to core", map[string]string{"solder_delivery_mode": "public", "solder_mirror_url": "https://cdn.example.com/m/", "core_public_url": "https://p.example.com"}, "", nil, true,
			"https://p.example.com/solder/mirror/" + key, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prov := &serveFakeProvider{t: t, presignURL: c.presignURL, presignErr: c.presignErr}
			got, err := solderModURL(context.Background(), settingsGetter(c.kv), prov, key, c.gated)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got url %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("url = %q, want %q", got, c.want)
			}
		})
	}
}
