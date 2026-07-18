package handlers

import "testing"

func TestValidateCoreStorageConfig(t *testing.T) {
	cases := []struct {
		name    string
		cfg     CoreStorageConfig
		wantErr bool
	}{
		{"path ok when absolute and confirmed", CoreStorageConfig{Backend: "path", Path: "/mnt/shared", PathConfirmed: true}, false},
		{"path rejected when not confirmed", CoreStorageConfig{Backend: "path", Path: "/mnt/shared", PathConfirmed: false}, true},
		{"path rejected when relative", CoreStorageConfig{Backend: "path", Path: "relative/dir", PathConfirmed: true}, true},
		{"path rejected when empty", CoreStorageConfig{Backend: "path", Path: "", PathConfirmed: true}, true},
		{"local alias treated like path", CoreStorageConfig{Backend: "local", Path: "/mnt/shared", PathConfirmed: true}, false},
		{"s3 ok with bucket+creds", CoreStorageConfig{Backend: "s3", S3Bucket: "b", S3AccessKey: "k", S3SecretKey: "s"}, false},
		{"s3 rejected without bucket", CoreStorageConfig{Backend: "s3", S3AccessKey: "k", S3SecretKey: "s"}, true},
		{"s3 rejected without access key", CoreStorageConfig{Backend: "s3", S3Bucket: "b", S3SecretKey: "s"}, true},
		{"s3 rejected without secret", CoreStorageConfig{Backend: "s3", S3Bucket: "b", S3AccessKey: "k"}, true},
		{"unknown backend rejected", CoreStorageConfig{Backend: "ftp"}, true},
		{"empty backend rejected (unconfigured)", CoreStorageConfig{Backend: ""}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateCoreStorageConfig(c.cfg)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateCoreStorageConfig(%+v) err = %v, wantErr %v", c.cfg, err, c.wantErr)
			}
		})
	}
}
