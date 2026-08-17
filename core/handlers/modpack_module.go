package handlers

import (
	"dylaris-core/models"
)

// modpackModuleName is the modules-table row that puts Modpacks in the navbar.
const modpackModuleName = "Modpacks"

// modpackModulePosition is where a freshly created row lands: after Tickets (5).
// Only the initial value; the admin reorders by drag-and-drop and that position
// is never overwritten by a sync.
const modpackModulePosition = 6

// modpackModuleRole derives the row's audience from the two flags, so the navbar
// cannot disagree with what the gate actually allows: admin-only while the
// subsystem is on but authoring is closed, everyone once authoring opens.
func modpackModuleRole(authoringOn bool) string {
	if authoringOn {
		return "all"
	}
	return "admin"
}

// syncModpackModule brings the Modpacks navbar row in line with the two feature
// flags, creating it if an install predates it.
//
// is_enabled and access_role are DERIVED here and therefore not meant to be
// edited in Settings -> Modules: a manual change there would be silently undone
// the next time the flags are saved, which is worse than not offering it. The
// panel renders them read-only for this row. Position stays the admin's, so
// ordering still works.
//
// Errors are returned, not logged, so the caller decides how fatal a stale navbar
// row is. Every caller so far treats it as cosmetic: the flags are the gate.
func syncModpackModule(state *AppState, modpacksOn, authoringOn bool) error {
	if state == nil || state.Store == nil {
		return nil
	}
	// Authoring without the subsystem is not a reachable state (the settings
	// handler folds it down), but deriving the role from a stale value here would
	// mislabel the row, so fold it again rather than trust the caller.
	if !modpacksOn {
		authoringOn = false
	}
	role := modpackModuleRole(authoringOn)

	modules, err := state.Store.ListModules()
	if err != nil {
		return err
	}
	for i := range modules {
		if modules[i].Name != modpackModuleName {
			continue
		}
		m := modules[i]
		if m.IsEnabled != modpacksOn {
			if err := state.Store.UpdateModuleStatus(m.ID, modpacksOn); err != nil {
				return err
			}
		}
		if m.AccessRole != role {
			if err := state.Store.SetModuleAccessRole(m.ID, role); err != nil {
				return err
			}
		}
		return nil
	}

	_, err = state.Store.CreateModule(&models.Module{
		Name:       modpackModuleName,
		Type:       "internal",
		Icon:       "package",
		URL:        "/modpacks",
		IsEnabled:  modpacksOn,
		IsSystem:   false,
		Position:   modpackModulePosition,
		AccessRole: role,
	})
	return err
}
