package validate

import "testing"

// Core and the node disagreed about what a mod file name is: the node reduced
// it to a basename and refused the leftovers, Core queued it unexamined and
// answered 200. Nothing escaped - the node's reduction is why - but the name
// Core recorded was not the file on disk, and uninstall aims at the recorded
// one. Both sides ask this now, so these rows are the contract between them.
func TestIsPlainFileName(t *testing.T) {
	tests := []struct {
		in   string
		want bool
		why  string
	}{
		{"fabric-api-0.158.0+26.3.jar", true, "a real mod jar: '+' and '.' are ordinary characters here"},
		{"a file with spaces.jar", true, "the alphabet is not the point; only whether it is a path"},
		{"Übersetzung.jar", true, "non-ascii is a fine file name"},
		{"../../../escape.jar", false, "sent live and accepted; it landed as escape.jar"},
		{"/etc/cron.d/pwn", false, "sent live and accepted; it landed as pwn"},
		{"sub/x.jar", false, "sent live and accepted; it landed as x.jar"},
		{`windows\path.jar`, false, "a backslash is a separator on one of the two platforms this code builds for, so it is refused on both"},
		{"..", false, ""},
		{".", false, ""},
		{"", false, ""},
		{"ok.jar\x00.txt", false, "a NUL truncates the name for whatever opens it"},
		{".hidden.jar", true, "a leading dot is not a path"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := IsPlainFileName(tt.in); got != tt.want {
				t.Errorf("IsPlainFileName(%q) = %v, want %v: %s", tt.in, got, tt.want, tt.why)
			}
		})
	}
}
