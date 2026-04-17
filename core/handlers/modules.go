package handlers

import (
	"dylaris-core/models"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

type ModuleHandler struct {
	state *AppState
}

func NewModuleHandler(state *AppState) *ModuleHandler {
	return &ModuleHandler{state: state}
}

func (h *ModuleHandler) GetModulesHandler(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	modules, err := h.state.Store.ListModules()
	if err != nil {
		sendJSONError(w, "Failed to load modules", 500)
		return
	}

	// === AUTO-SEED LOGIK ===
	if len(modules) == 0 {
		defaultModules := []models.Module{
			{Name: "Servers", Type: "internal", Icon: "server", URL: "/servers", IsEnabled: true, IsSystem: true, Position: 1},
			{Name: "Admin", Type: "internal", Icon: "shield-check", URL: "/admin", IsEnabled: true, IsSystem: true, Position: 2},
			{Name: "Infrastructure", Type: "internal", Icon: "cpu", URL: "/infrastructure", IsEnabled: true, IsSystem: true, Position: 3},
			{Name: "Gateway", Type: "internal", Icon: "globe", URL: "/gateway", IsEnabled: true, IsSystem: false, Position: 4},
			{Name: "Library", Type: "internal", Icon: "folder-open", URL: "/library", IsEnabled: false, IsSystem: false, Position: 5},
		}

		for _, m := range defaultModules {
			mod := m // Pointer copy
			h.state.Store.CreateModule(&mod)
		}

		// Reload list after insert
		modules, _ = h.state.Store.ListModules()
	}

	if modules == nil {
		modules = []models.Module{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"modules": modules,
	})
}

func (h *ModuleHandler) CreateModuleHandler(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	var req models.Module
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	if req.IsSystem {
		sendJSONError(w, "System modules cannot be created via API", 403)
		return
	}

	id, err := h.state.Store.CreateModule(&req)
	if err != nil {
		sendJSONError(w, "Create failed", 500)
		return
	}

	req.ID = id
	req.IsEnabled = true
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "module": req})
}

func (h *ModuleHandler) DeleteModuleHandler(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])

	if err := h.state.Store.DeleteModule(id); err != nil {
		sendJSONError(w, "Delete failed", 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// UpdateModulePositionHandler PATCH /modules/{id}/position
func (h *ModuleHandler) UpdateModulePositionHandler(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])

	var req struct {
		Position int `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	if err := h.state.Store.UpdateModulePosition(id, req.Position); err != nil {
		sendJSONError(w, "Update failed", 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// ToggleModuleHandler for enabling/disabling modules
func (h *ModuleHandler) ToggleModuleHandler(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])

	var req struct {
		IsEnabled bool `json:"isEnabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	// Prevent disabling the Servers module (default system module)
	mod, err := h.state.Store.GetModuleByID(id)
	if err != nil {
		sendJSONError(w, "Module not found", 404)
		return
	}
	if mod.IsSystem && !req.IsEnabled {
		sendJSONError(w, mod.Name+" module cannot be disabled", 400)
		return
	}

	if err := h.state.Store.UpdateModuleStatus(id, req.IsEnabled); err != nil {
		sendJSONError(w, "Update failed", 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
