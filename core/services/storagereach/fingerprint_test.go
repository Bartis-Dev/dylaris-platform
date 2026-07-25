package storagereach

import (
	"strings"
	"testing"
)

func TestFingerprint_StableAcrossCalls(t *testing.T) {
	cfg := Config{Backend: "path", Path: "/mnt/shared"}
	// Separate variables, not a direct self-comparison: staticcheck SA4000
	// rejects identical expressions around != and the CI gate runs SA*.
	first := Fingerprint(cfg)
	second := Fingerprint(cfg)
	if first != second {
		t.Fatalf("Fingerprint is not deterministic: %q vs %q", first, second)
	}
}

func TestFingerprint_DistinguishesBackends(t *testing.T) {
	tests := []struct {
		name string
		a, b Config
		want bool // true => the two must produce the SAME fingerprint
	}{
		{
			name: "same path",
			a:    Config{Backend: "path", Path: "/mnt/shared"},
			b:    Config{Backend: "path", Path: "/mnt/shared"},
			want: true,
		},
		{
			// "local" is the legacy alias for "path"; a Core configured with
			// either must agree with its peers, or every mixed-vintage
			// deployment reports a false fingerprint-mismatch.
			name: "local is an alias for path",
			a:    Config{Backend: "path", Path: "/mnt/shared"},
			b:    Config{Backend: "local", Path: "/mnt/shared"},
			want: true,
		},
		{
			name: "trailing slash is not a different path",
			a:    Config{Backend: "path", Path: "/mnt/shared"},
			b:    Config{Backend: "path", Path: "/mnt/shared/"},
			want: true,
		},
		{
			name: "different path",
			a:    Config{Backend: "path", Path: "/mnt/shared"},
			b:    Config{Backend: "path", Path: "/mnt/other"},
			want: false,
		},
		{
			name: "path vs s3",
			a:    Config{Backend: "path", Path: "/mnt/shared"},
			b:    Config{Backend: "s3", S3Bucket: "shared"},
			want: false,
		},
		{
			name: "same bucket and prefix",
			a:    Config{Backend: "s3", S3Endpoint: "https://s3.example", S3Bucket: "b", S3Prefix: "p"},
			b:    Config{Backend: "s3", S3Endpoint: "https://s3.example", S3Bucket: "b", S3Prefix: "p"},
			want: true,
		},
		{
			name: "different prefix",
			a:    Config{Backend: "s3", S3Endpoint: "https://s3.example", S3Bucket: "b", S3Prefix: "p"},
			b:    Config{Backend: "s3", S3Endpoint: "https://s3.example", S3Bucket: "b", S3Prefix: "q"},
			want: false,
		},
		{
			name: "different endpoint",
			a:    Config{Backend: "s3", S3Endpoint: "https://s3.example", S3Bucket: "b"},
			b:    Config{Backend: "s3", S3Endpoint: "https://s3.other", S3Bucket: "b"},
			want: false,
		},
		{
			// The secret is a credential, not an identity: two Cores holding
			// the same bucket under different keys still share the backend.
			name: "credentials are not part of the identity",
			a:    Config{Backend: "s3", S3Bucket: "b", S3AccessKey: "AK1", S3SecretKey: "s1"},
			b:    Config{Backend: "s3", S3Bucket: "b", S3AccessKey: "AK2", S3SecretKey: "s2"},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			same := Fingerprint(tc.a) == Fingerprint(tc.b)
			if same != tc.want {
				t.Fatalf("same = %v, want %v (a=%q b=%q)", same, tc.want, Fingerprint(tc.a), Fingerprint(tc.b))
			}
		})
	}
}

func TestFingerprint_CarriesNoSecret(t *testing.T) {
	cfg := Config{
		Backend:     "s3",
		S3Endpoint:  "https://s3.example.com",
		S3Bucket:    "backups-prod",
		S3Prefix:    "cluster-a",
		S3AccessKey: "AKIAIOSFODNN7EXAMPLE",
		S3SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	got := Fingerprint(cfg)

	if strings.Contains(got, cfg.S3AccessKey) || strings.Contains(got, cfg.S3SecretKey) {
		t.Fatalf("Fingerprint = %q, contains a credential", got)
	}

	// The load-bearing check: swapping credentials must leave the
	// fingerprint byte-identical, while swapping an identity field (the
	// bucket) must change it. Without this pair, the test above would still
	// pass even if a credential were folded into the hash input.
	sameIdentityOtherCreds := cfg
	sameIdentityOtherCreds.S3AccessKey = "AKIAI44QH8DHBEXAMPLE"
	sameIdentityOtherCreds.S3SecretKey = "je7MtGbClwBF/2Zp9utk/h3yCo8nvbEXAMPLEKEY"
	if want, other := Fingerprint(cfg), Fingerprint(sameIdentityOtherCreds); want != other {
		t.Fatalf("Fingerprint changed when only credentials changed: %q vs %q", want, other)
	}

	differentBucket := cfg
	differentBucket.S3Bucket = "backups-staging"
	if got, changed := Fingerprint(cfg), Fingerprint(differentBucket); got == changed {
		t.Fatalf("Fingerprint = %q, did not change when the bucket changed", got)
	}
}
