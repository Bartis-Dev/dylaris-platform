package main

import "testing"

func TestResolveHostPath(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		mounts []hostMount
		want   string
		wantOK bool
	}{
		{
			name:   "exact mount destination",
			path:   "/storage",
			mounts: []hostMount{{Destination: "/storage", Source: "/mnt/disk1"}},
			want:   "/mnt/disk1",
			wantOK: true,
		},
		{
			name:   "path under a mount",
			path:   "/storage/servers",
			mounts: []hostMount{{Destination: "/storage", Source: "/mnt/disk1"}},
			want:   "/mnt/disk1/servers",
			wantOK: true,
		},
		{
			// The bug this function exists for: a plain prefix test says /stor is
			// a parent of /storage, and the substitution yields /mnt/diskXage -
			// a path that exists nowhere, silently bind-mounted into a server.
			name: "a shorter sibling is not a parent",
			path: "/storage",
			mounts: []hostMount{
				{Destination: "/stor", Source: "/mnt/diskX"},
			},
			wantOK: false,
		},
		{
			name: "the sibling loses to the real parent",
			path: "/storage/servers",
			mounts: []hostMount{
				{Destination: "/stor", Source: "/mnt/wrong"},
				{Destination: "/storage", Source: "/mnt/right"},
			},
			want:   "/mnt/right/servers",
			wantOK: true,
		},
		{
			// Docker returns mounts in no meaningful order, so a first-match loop
			// resolves this through / roughly half the time.
			name: "most specific mount wins regardless of order",
			path: "/storage/servers",
			mounts: []hostMount{
				{Destination: "/", Source: "/hostroot"},
				{Destination: "/storage", Source: "/mnt/disk1"},
			},
			want:   "/mnt/disk1/servers",
			wantOK: true,
		},
		{
			name: "most specific mount wins when listed last",
			path: "/storage/servers",
			mounts: []hostMount{
				{Destination: "/storage", Source: "/mnt/disk1"},
				{Destination: "/", Source: "/hostroot"},
			},
			want:   "/mnt/disk1/servers",
			wantOK: true,
		},
		{
			name: "nested mounts pick the deepest",
			path: "/storage/fast/servers",
			mounts: []hostMount{
				{Destination: "/storage", Source: "/mnt/slow"},
				{Destination: "/storage/fast", Source: "/mnt/nvme"},
			},
			want:   "/mnt/nvme/servers",
			wantOK: true,
		},
		{
			name:   "root mount",
			path:   "/storage",
			mounts: []hostMount{{Destination: "/", Source: "/hostroot"}},
			want:   "/hostroot/storage",
			wantOK: true,
		},
		{
			// Not containerised, or a path on no mount at all: the caller falls
			// back to treating local as host, which is the pre-existing behaviour.
			name:   "no mount matches",
			path:   "/storage",
			mounts: []hostMount{{Destination: "/data", Source: "/mnt/data"}},
			wantOK: false,
		},
		{
			name:   "no mounts at all",
			path:   "/storage",
			mounts: nil,
			wantOK: false,
		},
		{
			name:   "a trailing slash on the source does not double up",
			path:   "/storage/servers",
			mounts: []hostMount{{Destination: "/storage", Source: "/mnt/disk1/"}},
			want:   "/mnt/disk1/servers",
			wantOK: true,
		},
		{
			name:   "a trailing slash on the destination still matches",
			path:   "/storage/servers",
			mounts: []hostMount{{Destination: "/storage/", Source: "/mnt/disk1"}},
			want:   "/mnt/disk1/servers",
			wantOK: true,
		},
		{
			// An incomplete entry cannot be acted on and must not win over a
			// usable one.
			name: "entries missing a source are skipped",
			path: "/storage/servers",
			mounts: []hostMount{
				{Destination: "/storage", Source: ""},
				{Destination: "/", Source: "/hostroot"},
			},
			want:   "/hostroot/storage/servers",
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := resolveHostPath(tt.path, tt.mounts)
			if ok != tt.wantOK {
				t.Fatalf("resolveHostPath(%q) ok = %v, want %v", tt.path, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("resolveHostPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestMountContains(t *testing.T) {
	tests := []struct {
		dest, path string
		want       bool
	}{
		{"/storage", "/storage", true},
		{"/storage", "/storage/servers", true},
		{"/storage", "/storageX", false},
		{"/stor", "/storage", false},
		{"/", "/anything", true},
		{"/storage/", "/storage/servers", true},
		{"/storage", "/other", false},
	}
	for _, tt := range tests {
		t.Run(tt.dest+" vs "+tt.path, func(t *testing.T) {
			if got := mountContains(tt.dest, tt.path); got != tt.want {
				t.Errorf("mountContains(%q, %q) = %v, want %v", tt.dest, tt.path, got, tt.want)
			}
		})
	}
}
