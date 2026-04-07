package handlers

import (
	"encoding/json"
	"net/http"
)

type SystemHandler struct{}

// NewSystemHandler creates a new handler
func NewSystemHandler() *SystemHandler {
	return &SystemHandler{}
}

// GetCapabilities returns the system capabilities
func (h *SystemHandler) GetCapabilities(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(map[string]interface{}{
		"mode":           "business",
		"max_nodes":      9999,
		"max_gates":      9999,
		"can_whitelabel": true,
		"can_custom_jars": true,
		"can_use_dns":    true,
	})
}
