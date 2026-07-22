package validate

import "testing"

func TestUsername(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"alice", true},
		{"a_b.c-d", true},
		{"abc", true},
		{"A1b2c3", true},
		{"ab", false},   // too short
		{"a:b", false},  // colon - the Redis-key-injection case
		{"a b", false},  // space
		{"_abc", false}, // leading non-alnum
		{".abc", false}, // leading dot
		{"a@b", false},  // special char
		{"", false},     // empty
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false}, // 33 chars
	}
	for _, c := range cases {
		if got := IsUsername(c.in); got != c.want {
			t.Errorf("IsUsername(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestServerName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"My Server", true},
		{"srv_1", true},
		{"a", true},
		{"a-b+c_d 1", true},
		{" leading", false}, // leading space
		{"a/b", false},      // slash
		{"a:b", false},      // colon
		{"", false},
	}
	for _, c := range cases {
		if got := IsServerName(c.in); got != c.want {
			t.Errorf("IsServerName(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	// length: exactly 50 ok, 51 not
	n50 := "a" + string(make([]byte, 0))
	for len(n50) < 50 {
		n50 += "a"
	}
	if !IsServerName(n50) {
		t.Errorf("IsServerName(50 chars) = false, want true")
	}
	if IsServerName(n50 + "a") {
		t.Errorf("IsServerName(51 chars) = true, want false")
	}
}

func TestSubServerName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"survival", true},
		{"world_1", true},
		{"a-b+c", true},
		{"a b", false},      // space not allowed for a dir name
		{"a/b", false},      // slash
		{".dylaris", false}, // dot not allowed
		{"", false},
	}
	for _, c := range cases {
		if got := IsSubServerName(c.in); got != c.want {
			t.Errorf("IsSubServerName(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestServerUUID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"84087ad8-e332-44f2-adec-75869b8c1ee1", true},
		{"84087AD8-E332-44F2-ADEC-75869B8C1EE1", true},
		{"not-a-uuid", false},
		{"84087ad8e33244f2adec75869b8c1ee1", false}, // no dashes
		{"a:b", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsServerUUID(c.in); got != c.want {
			t.Errorf("IsServerUUID(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSlugLabelEmailMcVersion(t *testing.T) {
	if !IsSlug("my-pack_1") || IsSlug("My Pack") || IsSlug("-x") {
		t.Error("Slug cases failed")
	}
	if !IsLabel("eu-central") || IsLabel("EU") || IsLabel("a b") {
		t.Error("Label cases failed")
	}
	if !IsEmail("a@b.co") || IsEmail("no-at") || IsEmail("a @b.co") {
		t.Error("Email cases failed")
	}
	if !IsMcVersion("1.21") || !IsMcVersion("1.21.4") || IsMcVersion("1") || IsMcVersion("1.x") {
		t.Error("McVersion cases failed")
	}
	if !IsMinecraftUsername("Notch_99") || IsMinecraftUsername("ab") || IsMinecraftUsername("has space") || IsMinecraftUsername("has:colon") {
		t.Error("MinecraftUsername cases failed")
	}
}

func TestSanitizeServerName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"My Server!", "My Server"},
		{"  ---abc", "abc"},
		{"a/b*c", "abc"},
		{"", ""},
		{"!!!", ""},
	}
	for _, c := range cases {
		if got := SanitizeServerName(c.in); got != c.want {
			t.Errorf("SanitizeServerName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// over-length is trimmed to 50
	long := ""
	for i := 0; i < 60; i++ {
		long += "a"
	}
	if got := SanitizeServerName(long); len(got) != 50 {
		t.Errorf("SanitizeServerName(60 chars) len = %d, want 50", len(got))
	}
}

func TestIsSafeRelPath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"survival/world.zip", true},
		{"", true}, // server root
		{"a/b/c.txt", true},
		{"/etc/passwd", false}, // absolute
		{"../secret", false},   // traversal
		{"a/../../b", false},   // traversal mid-path
		{`C:\Windows`, false},  // drive
		{`a\..\b`, false},      // backslash traversal
	}
	for _, c := range cases {
		if got := IsSafeRelPath(c.in); got != c.want {
			t.Errorf("IsSafeRelPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestIsInstallerType(t *testing.T) {
	for _, ok := range []string{"paper", "vanilla", "upload-zip", "modpack"} {
		if !IsInstallerType(ok) {
			t.Errorf("IsInstallerType(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "rm -rf", "PAPER", "ftp"} {
		if IsInstallerType(bad) {
			t.Errorf("IsInstallerType(%q) = true, want false", bad)
		}
	}
}

func TestResourceBounds(t *testing.T) {
	if r := ResourceBounds(1024, 2, 5120, 8); r != "" {
		t.Errorf("valid resources rejected: %q", r)
	}
	if ResourceBounds(128, 2, 5120, 8) == "" {
		t.Error("RAM 128 should be rejected")
	}
	if ResourceBounds(1024, 0, 5120, 8) == "" {
		t.Error("CPU 0 should be rejected")
	}
	if ResourceBounds(1024, 16, 5120, 8) == "" {
		t.Error("CPU over host cap should be rejected")
	}
	if ResourceBounds(1024, 2, -1, 8) == "" {
		t.Error("negative disk should be rejected")
	}
	// hostCPU 0 -> CPU ceiling skipped
	if ResourceBounds(1024, 999, 5120, 0) != "" {
		t.Error("hostCPU 0 should skip the CPU ceiling")
	}
}
