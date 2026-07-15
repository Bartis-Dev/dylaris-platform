package handlers

import (
	"errors"

	"dylaris-core/authz"
	"dylaris-core/store"
)

// PermissionsMode reads the permissions_mode setting (level-2 delegation
// surface), normalized. Unset or unknown -> simple (the fresh-install default).
// Scaffolding for the phase-6 off/simple/advanced enforcement + UI; phase 3
// builds no enforcement against it.
func PermissionsMode(st store.Store) string {
	v, err := st.GetSetting(authz.PermissionsModeSettingKey)
	if err != nil {
		return authz.ModeSimple
	}
	return authz.NormalizeMode(v)
}

// SetPermissionsMode validates the enum then persists it. Errors on an unknown
// mode rather than silently coercing, so a bad admin write is surfaced.
func SetPermissionsMode(st store.Store, mode string) error {
	if !authz.ValidMode(mode) {
		return errors.New("invalid permissions_mode: " + mode)
	}
	return st.SetSetting(authz.PermissionsModeSettingKey, mode)
}
