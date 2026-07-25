package storagereach

import "testing"

func TestFingerprint_StableAcrossCalls(t *testing.T) {
	cfg := Config{Backend: "path", Path: "/mnt/shared"}
	if Fingerprint(cfg) != Fingerprint(cfg) {
		t.Fatal("Fingerprint is not deterministic")
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
	cfg := Config{Backend: "s3", S3Bucket: "b", S3SecretKey: "super-secret-value"}
	if got := Fingerprint(cfg); len(got) != 16 {
		t.Fatalf("Fingerprint = %q, want a 16-char hex digest", got)
	}
}
