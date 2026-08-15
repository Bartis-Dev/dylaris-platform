package main

import (
	"strings"
	"testing"
)

func TestParseContainerIDFromMountinfo(t *testing.T) {
	const id = "761af55891deaea74c31be471091bae05a9bdf0237deb888a9ff9f9be1fac89f"

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			// Docker Desktop: /etc/hostname bind-mounted from the container dir.
			name: "docker desktop hostname bind",
			in: "1500 1400 8:32 /containers/" + id + "/hostname /etc/hostname rw,relatime shared:1 - ext4 /dev/sdc rw\n" +
				"1501 1400 8:32 /containers/" + id + "/hosts /etc/hosts rw,relatime shared:1 - ext4 /dev/sdc rw",
			want: id,
		},
		{
			// Standard Docker: /var/lib/docker/containers/<id>/...
			name: "standard docker resolv.conf bind",
			in:   "600 500 0:40 /var/lib/docker/containers/" + id + "/resolv.conf /etc/resolv.conf rw - ext4 /dev/sda1 rw",
			want: id,
		},
		{
			// The data volume mount has no /containers/ id and must not match.
			name: "only unrelated mounts",
			in: "700 500 0:41 / /app/dylaris_data rw - ext4 /dev/sda1 rw\n" +
				"701 500 0:42 / /var/run/docker.sock rw - ext4 /dev/sda1 rw",
			want: "",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
		{
			// /containers/ present but the segment is not a 64-hex id.
			name: "containers path but not an id",
			in:   "700 500 0:41 /var/lib/docker/containers/not-a-real-id/hostname /etc/hostname rw - ext4 /dev/sda1 rw",
			want: "",
		},
		{
			// A too-short hex segment must be rejected (guards the length check).
			name: "short hex is rejected",
			in:   "700 500 0:41 /containers/abc123/hostname /etc/hostname rw - ext4 /dev/sda1 rw",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseContainerIDFromMountinfo(strings.NewReader(tc.in)); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsHex64(t *testing.T) {
	const good = "761af55891deaea74c31be471091bae05a9bdf0237deb888a9ff9f9be1fac89f"
	if !isHex64(good) {
		t.Fatalf("valid 64-hex id rejected")
	}
	bad := []string{
		"",
		good[:63],                // 63 chars
		good + "0",               // 65 chars
		strings.ToUpper(good),    // uppercase is not [0-9a-f]
		"761af55891deaea74c31be471091bae05a9bdf0237deb888a9ff9f9be1fac89g", // trailing g
	}
	for _, s := range bad {
		if isHex64(s) {
			t.Fatalf("isHex64(%q) = true, want false", s)
		}
	}
}
