package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
)

type RouteHandler struct {
	state *AppState
}

func NewRouteHandler(state *AppState) *RouteHandler {
	return &RouteHandler{state: state}
}

type CreateRouteReq struct {
	Domain     string `json:"domain"`
	ServerUUID string `json:"server_uuid"`
}

// CreateRoute saves the domain in Postgres AND writes it to Redis for the Gate
func (h *RouteHandler) CreateRoute(w http.ResponseWriter, r *http.Request) {
	var req CreateRouteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	// TODO: Save the route in your Postgres database here (via h.state.Store)
	// Example: err := h.state.Store.CreateRoute(req.Domain, req.ServerUUID)

	// IMPORTANT: Write the route to Redis so the GATE sees it immediately!
	ctx := context.Background()
	redisKey := fmt.Sprintf("gate:routes:%s", req.Domain)

	// Sets the key without expiration (0)
	if err := h.state.Redis.Set(ctx, redisKey, req.ServerUUID, 0).Err(); err != nil {
		http.Error(w, "Failed to sync route to Redis", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Route created successfully",
		"domain":  req.Domain,
	})
}

// DeleteRoute removes the domain from Postgres AND Redis
func (h *RouteHandler) DeleteRoute(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	domain := vars["domain"]

	// TODO: Delete from Postgres here
	// h.state.Store.DeleteRoute(domain)

	// Delete from Redis so the Gate stops routing traffic
	ctx := context.Background()
	redisKey := fmt.Sprintf("gate:routes:%s", domain)
	h.state.Redis.Del(ctx, redisKey)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Route deleted"})
}
