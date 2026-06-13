package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

// GetServerStoragePath returns the current storage path for a server and all available
// storage paths on its node (from the node's Redis heartbeat).
// GET /api/servers/{id}/storage-path  (admin only)
func (h *ServerHandler) GetServerStoragePath(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil || srv == nil {
		sendJSONError(w, "Server not found", 404)
		return
	}

	node, err := h.state.Store.GetNodeByID(srv.NodeID)
	if err != nil || node == nil {
		sendJSONError(w, "Node not found", 404)
		return
	}

	// Current storage path from Redis
	currentPath := ""
	if h.state.Redis != nil {
		key := fmt.Sprintf("node:%s:server:%s:storage", node.Token, srv.UUID)
		if val, redisErr := h.state.Redis.Get(r.Context(), key).Result(); redisErr == nil {
			currentPath = val
		}
	}

	// Available storage paths from node heartbeat
	var storagePaths interface{} = []interface{}{}
	if h.state.Redis != nil {
		heartbeatKey := "dylaris:discovery:" + node.Token
		if val, redisErr := h.state.Redis.Get(r.Context(), heartbeatKey).Result(); redisErr == nil {
			var heartbeat map[string]interface{}
			if json.Unmarshal([]byte(val), &heartbeat) == nil {
				if s, ok := heartbeat["storage"]; ok {
					storagePaths = s
				}
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"currentPath":  currentPath,
		"storagePaths": storagePaths,
	})
}

// MigrateServerStorage queues a migrate_storage command on the server's node.
// POST /api/servers/{id}/migrate-storage  (admin only)
func (h *ServerHandler) MigrateServerStorage(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return
	}

	var req struct {
		TargetPath string `json:"targetPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TargetPath == "" {
		sendJSONError(w, "targetPath is required", 400)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil || srv == nil {
		sendJSONError(w, "Server not found", 404)
		return
	}

	node, err := h.state.Store.GetNodeByID(srv.NodeID)
	if err != nil || node == nil {
		sendJSONError(w, "Node not found", 404)
		return
	}

	if h.state.Queue == nil {
		sendJSONError(w, "Queue not available", 503)
		return
	}

	if err := h.state.Queue.SendMigrateCommand(r.Context(), node.Token, srv.UUID, req.TargetPath); err != nil {
		log.Printf("Failed to queue migrate_storage for server %d: %v", serverID, err)
		sendJSONError(w, "Failed to queue migration", 500)
		return
	}

	log.Printf("migrate_storage queued for server %d (%s) → %s", serverID, srv.UUID, req.TargetPath)
	h.state.Events.Publish(r.Context(), "servers.changed", nil)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Migration queued. Server will be stopped and data moved to the new path.",
	})
}
