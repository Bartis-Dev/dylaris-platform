package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

func sendJSONError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"message": message,
	})
}

func IsAdmin(r *http.Request) bool {
	isAdmin, ok := r.Context().Value("isAdmin").(bool)
	return ok && isAdmin
}

// parseUserID extracts a UUID-typed user ID from a mux path variable. By
// default it reads mux.Vars(r)["id"]; pass an alternate var name (e.g.
// "userId") when the route uses one. On failure it writes a 400 response
// and returns ok=false so the caller can early-out.
func parseUserID(w http.ResponseWriter, r *http.Request, varName ...string) (string, bool) {
	key := "id"
	if len(varName) > 0 && varName[0] != "" {
		key = varName[0]
	}
	id := mux.Vars(r)[key]
	if _, err := uuid.Parse(id); err != nil {
		sendJSONError(w, "Invalid user ID", http.StatusBadRequest)
		return "", false
	}
	return id, true
}
