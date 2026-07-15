package handlers

import "testing"

func TestAdminCreateAllowed(t *testing.T) {
	const secret = "correct-horse-battery-staple" // >= 16 chars

	cases := []struct {
		name       string
		userCount  int
		configured string
		provided   string
		want       bool
	}{
		// ADMIN_SECRET unset: only a pristine fresh install is open.
		{"unset fresh-install open", 0, "", "", true},
		{"unset fresh-install ignores provided", 0, "", "whatever", true},
		{"unset lost-admin closed", 3, "", "", false},
		{"unset complete closed", 1, "", "anything", false},

		// ADMIN_SECRET set: allowed in ANY mode iff the secret matches.
		{"set fresh-install match", 0, secret, secret, true},
		{"set fresh-install mismatch", 0, secret, "wrong", false},
		{"set fresh-install missing", 0, secret, "", false},
		{"set lost-admin match (break-glass)", 3, secret, secret, true},
		{"set lost-admin mismatch", 3, secret, "wrong", false},
		{"set complete match (break-glass)", 1, secret, secret, true},
		{"set complete mismatch", 1, secret, "wrong", false},
		// A provided value longer than the configured secret must not match.
		{"set longer provided no match", 0, secret, secret + "x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := adminCreateAllowed(tc.userCount, tc.configured, tc.provided); got != tc.want {
				t.Errorf("adminCreateAllowed(%d, configured=%t, provided=%q) = %v, want %v",
					tc.userCount, tc.configured != "", tc.provided, got, tc.want)
			}
		})
	}
}
