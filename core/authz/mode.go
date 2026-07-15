package authz

// permissions_mode controls the level-2 delegation surface (spec: off | simple
// | advanced). Phase 3 ships only the vocabulary + validation; the off/simple/
// advanced enforcement + UI is phase 6. The value lives in the settings table
// under PermissionsModeSettingKey (seeded 'simple' by the phase-1 foundation).
const (
	ModeOff      = "off"
	ModeSimple   = "simple"
	ModeAdvanced = "advanced"

	PermissionsModeSettingKey = "permissions_mode"
)

// ValidMode reports whether m is one of the three known modes.
func ValidMode(m string) bool {
	return m == ModeOff || m == ModeSimple || m == ModeAdvanced
}

// NormalizeMode returns m when valid, else the fresh-install default (simple).
func NormalizeMode(m string) string {
	if ValidMode(m) {
		return m
	}
	return ModeSimple
}
