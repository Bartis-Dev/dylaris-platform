package handlers

import (
	"dylaris-core/models"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type NodeHandler struct {
	state *AppState
}

func NewNodeHandler(state *AppState) *NodeHandler {
	return &NodeHandler{state: state}
}

func (h *NodeHandler) GetNodes(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "DB error", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	nodes, err := h.state.Store.ListNodes()
	if err != nil {
		sendJSONError(w, "Failed to load nodes", 500)
		return
	}
	if nodes == nil {
		nodes = []models.Node{}
	}

	// FIX: Return as object
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"nodes":   nodes,
	})
}

func (h *NodeHandler) CreateNode(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "DB error", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	var req models.Node
	json.NewDecoder(r.Body).Decode(&req)

	req.Token = uuid.New().String()
	if req.Address == "" {
		req.Address = "0.0.0.0"
	}
	req.Status = "offline"
	req.CreatedAt = time.Now()

	if err := h.state.Store.CreateNode(&req); err != nil {
		sendJSONError(w, "Failed to create node", 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true, "message": "Node created", "node": req,
	})
}

func (h *NodeHandler) UpdateNode(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "DB error", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])

	var req struct {
		CpusetCpus *string `json:"cpusetCpus"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.CpusetCpus != nil {
		if err := h.state.Store.UpdateNodeCpusetCpus(id, *req.CpusetCpus); err != nil {
			sendJSONError(w, "Failed to update node", 500)
			return
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true, "message": "Node updated",
	})
}

func (h *NodeHandler) DeleteNode(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "DB error", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])

	if err := h.state.Store.DeleteNode(id); err != nil {
		sendJSONError(w, "Delete failed", 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// GetNodeServers returns all servers assigned to a node
func (h *NodeHandler) GetNodeServers(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])

	servers, err := h.state.Store.ListServersByNode(id)
	if err != nil {
		sendJSONError(w, "Failed to load servers", 500)
		return
	}
	if servers == nil {
		servers = []models.Server{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"servers": servers,
	})
}

// ForceDeleteNode deletes an offline node and all its servers
func (h *NodeHandler) ForceDeleteNode(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])

	node, err := h.state.Store.GetNodeByID(id)
	if err != nil {
		sendJSONError(w, "Node not found", 404)
		return
	}

	if node.Status == "online" {
		sendJSONError(w, "Cannot force-delete an online node", 400)
		return
	}

	// Get servers for response before deleting
	servers, _ := h.state.Store.ListServersByNode(id)

	// Delete all servers on this node first (FK constraint)
	if err := h.state.Store.DeleteServersByNode(id); err != nil {
		sendJSONError(w, "Failed to delete servers on node", 500)
		return
	}

	// Delete the node itself
	if err := h.state.Store.DeleteNode(id); err != nil {
		sendJSONError(w, "Failed to delete node", 500)
		return
	}

	deletedNames := make([]string, 0, len(servers))
	for _, s := range servers {
		deletedNames = append(deletedNames, s.Name)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"deletedServers": deletedNames,
	})
}

// GetNodeStorage returns storage path info from the node's Redis heartbeat.
// GET /api/nodes/{id}/storage
func (h *NodeHandler) GetNodeStorage(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])

	node, err := h.state.Store.GetNodeByID(id)
	if err != nil || node == nil {
		sendJSONError(w, "Node not found", 404)
		return
	}

	// Read storage info from Redis discovery heartbeat
	if h.state.Redis == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"storage": []interface{}{},
		})
		return
	}

	key := "dylaris:discovery:" + node.Token
	val, err := h.state.Redis.Get(r.Context(), key).Result()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"storage": []interface{}{},
			"message": "Node heartbeat not found (offline?)",
		})
		return
	}

	var heartbeat map[string]interface{}
	if err := json.Unmarshal([]byte(val), &heartbeat); err != nil {
		sendJSONError(w, "Failed to parse heartbeat", 500)
		return
	}

	storage, ok := heartbeat["storage"]
	if !ok {
		storage = []interface{}{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"storage": storage,
	})
}
