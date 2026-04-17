package handlers

import (
	"dylaris-core/models"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	pb "dylaris-proto/node"

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

// storageHeartbeatEntry is used to parse storage entries from the node discovery heartbeat.
type storageHeartbeatEntry struct {
	Path        string   `json:"path"`
	ServerUUIDs []string `json:"server_uuids"`
}

// GetDiskAnalysis cross-references disk folders on a node with DB servers.
// GET /api/admin/nodes/{id}/disk-analysis
func (h *NodeHandler) GetDiskAnalysis(w http.ResponseWriter, r *http.Request) {
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

	dbServers, err := h.state.Store.ListServersByNode(id)
	if err != nil {
		sendJSONError(w, "Failed to load servers", 500)
		return
	}

	dbByUUID := make(map[string]models.Server)
	for _, s := range dbServers {
		dbByUUID[s.UUID] = s
	}

	diskUUIDs := make(map[string]bool)
	if h.state.Redis != nil {
		key := "dylaris:discovery:" + node.Token
		if val, redisErr := h.state.Redis.Get(r.Context(), key).Result(); redisErr == nil {
			var hb struct {
				Storage []storageHeartbeatEntry `json:"storage"`
			}
			if json.Unmarshal([]byte(val), &hb) == nil {
				for _, entry := range hb.Storage {
					for _, u := range entry.ServerUUIDs {
						diskUUIDs[u] = true
					}
				}
			}
		}
	}

	type matchedEntry struct {
		UUID   string `json:"uuid"`
		Name   string `json:"serverName"`
		Owner  string `json:"ownerName"`
		Status string `json:"status"`
	}
	type orphanedEntry struct {
		UUID string `json:"uuid"`
	}
	type missingEntry struct {
		UUID string `json:"uuid"`
		Name string `json:"serverName"`
	}

	matched := []matchedEntry{}
	orphaned := []orphanedEntry{}
	missing := []missingEntry{}

	for u := range diskUUIDs {
		if srv, ok := dbByUUID[u]; ok {
			matched = append(matched, matchedEntry{UUID: u, Name: srv.Name, Owner: srv.OwnerName, Status: srv.Status})
		} else {
			orphaned = append(orphaned, orphanedEntry{UUID: u})
		}
	}
	for u, srv := range dbByUUID {
		if !diskUUIDs[u] {
			missing = append(missing, missingEntry{UUID: u, Name: srv.Name})
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"matched":  matched,
		"orphaned": orphaned,
		"missing":  missing,
	})
}

// DeleteOrphanedFolder deletes an orphaned UUID folder from a node via gRPC.
// DELETE /api/admin/nodes/{id}/orphan?uuid=
func (h *NodeHandler) DeleteOrphanedFolder(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	vars := mux.Vars(r)
	nodeID, _ := strconv.Atoi(vars["id"])

	orphanUUID := r.URL.Query().Get("uuid")
	if orphanUUID == "" {
		sendJSONError(w, "uuid query param required", 400)
		return
	}
	if _, err := uuid.Parse(orphanUUID); err != nil {
		sendJSONError(w, "Invalid UUID format", 400)
		return
	}

	// Safety check: ensure it is NOT in the DB
	srv, _ := h.state.Store.GetServerByUUID(orphanUUID)
	if srv != nil {
		sendJSONError(w, "Server exists in database — use normal delete", 400)
		return
	}

	if h.state.GRPCRegistry == nil {
		sendJSONError(w, "gRPC not available", 503)
		return
	}

	resp, err := h.state.GRPCRegistry.SendRequest(nodeID, &pb.NodeMessage{
		RequestId:  fmt.Sprintf("orphan-del-%s", orphanUUID),
		ServerUuid: orphanUUID,
		Payload:    &pb.NodeMessage_DeleteReq{DeleteReq: &pb.DeleteFileReq{Path: "."}},
	}, 30*time.Second)
	if err != nil {
		sendJSONError(w, fmt.Sprintf("Node communication error: %v", err), 502)
		return
	}
	if errResp := resp.GetError(); errResp != nil {
		sendJSONError(w, errResp.Message, 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Orphaned folder %s deleted", orphanUUID),
	})
}
