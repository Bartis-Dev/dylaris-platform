package handlers

import (
	"testing"

	"dylaris-core/authz"
	"dylaris-core/models"
)

func capSet(ids ...string) func(string) bool {
	held := make(map[string]bool, len(ids))
	for _, id := range ids {
		held[id] = true
	}
	return func(c string) bool { return held[c] }
}

// A grant made through the Access page writes cap_overrides, never the legacy
// invite blob the panel gates its tabs on, so a member who reached the server
// through the cap model saw every tab locked while the API served them fine.
func TestMergeResolvedTabPermissions(t *testing.T) {
	tests := []struct {
		name string
		base *models.TabPermissions
		caps func(string) bool
		want models.TabPermissions
	}{
		{
			name: "no legacy invite, caps alone light the tabs",
			base: nil,
			caps: capSet("overview.read", "backups.read"),
			want: models.TabPermissions{Overview: true, Backups: true},
		},
		{
			name: "legacy bits survive when the cap set is empty",
			base: &models.TabPermissions{Console: true, Inherit: true},
			caps: capSet(),
			want: models.TabPermissions{Console: true, Inherit: true},
		},
		{
			name: "caps are OR-ed onto the legacy bits, never subtracted",
			base: &models.TabPermissions{Console: true},
			caps: capSet("files.read"),
			want: models.TabPermissions{Console: true, Files: true},
		},
		{
			name: "inherit is an invite column, no capability sets it",
			base: nil,
			caps: capSet("overview.read", "console.read", "files.read", "config.read",
				"power.start", "network.read", "members.read", "backups.read", "server.settings.write"),
			want: models.TabPermissions{
				Console: true, Files: true, Config: true, Setup: true, Overview: true,
				Power: true, Members: true, Network: true, Backups: true,
			},
		},
		{
			name: "a nil resolver leaves the blob untouched",
			base: &models.TabPermissions{Overview: true},
			caps: nil,
			want: models.TabPermissions{Overview: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeResolvedTabPermissions(tt.base, tt.caps); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Each tab bit is spelled with a real capability id. A typo here would silently
// leave that tab locked forever, which is the failure this whole mapping exists
// to fix.
func TestTabPermissionCapsAreRealCapabilities(t *testing.T) {
	for _, m := range tabPermissionCaps {
		c, ok := authz.Get(m.cap)
		if !ok {
			t.Errorf("%q is not in the capability catalog", m.cap)
			continue
		}
		if c.Scope != authz.ScopeServer {
			t.Errorf("%q has scope %v, want ScopeServer - a tab is a per-server thing", m.cap, c.Scope)
		}
	}
}
