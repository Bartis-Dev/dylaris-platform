package handlers

import (
	"bytes"
	"dylaris-core/models"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	pb "dylaris-proto/node"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"
)

// orphanNameRegex restricts orphan folder names to safe filename chars.
// Permits standard UUIDs and legacy folder names (e.g. "test-server-1")
// while rejecting path traversal attempts ('/', '..', etc.).
var orphanNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func isSafeOrphanName(name string) bool {
	return orphanNameRegex.MatchString(name)
}

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
	// Node list is admin-only today. In BYON mode a non-admin tenant may list
	// THEIR OWN nodes; everything else stays admin-only.
	admin := IsAdmin(r)
	byon := byonActive(h.state, r)
	if !admin && !byon {
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

	// BYON tenant: scope the list to nodes they own.
	if !admin && byon {
		uid := byonCallerID(r)
		owned := make([]models.Node, 0, len(nodes))
		for _, n := range nodes {
			if n.OwnerID != nil && *n.OwnerID == uid {
				owned = append(owned, n)
			}
		}
		nodes = owned
	}

	// Derive the unusable flag at response time (no DB column needed): an
	// external/home node only routes via gateway+beam, so while the platform
	// is in ip_port mode it has no reachable path. Panel uses this to show a
	// "requires gateway" badge instead of letting admins place servers there.
	gatewayOn := h.state.gatewayEnabled()
	for i := range nodes {
		if nodes[i].IsExternal() && !gatewayOn {
			nodes[i].Unusable = true
			nodes[i].UnusableReason = "requires_gateway"
		}
		// A node with no region was booted with only a CLUSTER_SECRET (no
		// DYLARIS_REGION) and never adopted — flag it so the panel can prompt
		// an admin to configure name/region/tags.
		if nodes[i].Region == "" {
			nodes[i].NeedsConfiguration = true
		}
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

// ConfigureNode adopts an auto-discovered node: an admin sets its name, region
// and tags, which are persisted to the DB and marked configured=true so the
// heartbeat env stops overwriting them.
// PATCH /api/nodes/{id}/config (admin-only)
func (h *NodeHandler) ConfigureNode(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "DB error", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid node id", 400)
		return
	}

	node, err := h.state.Store.GetNodeByID(id)
	if err != nil || node == nil {
		sendJSONError(w, "Node not found", 404)
		return
	}

	var req struct {
		Name   string `json:"name"`   // optional; keeps current name when empty
		Region string `json:"region"` // required — clears the needs-configuration state
		Tags   string `json:"tags"`   // optional, comma-separated
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON body", 400)
		return
	}

	region := strings.TrimSpace(req.Region)
	if region == "" {
		sendJSONError(w, "region is required", 400)
		return
	}
	// Region must be a known region so placement stays consistent.
	if _, err := h.state.Store.GetRegion(region); err != nil {
		sendJSONError(w, "unknown region", 400)
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = node.Name // keep current display name
	} else if name != node.Name {
		// Node names are unique (idx_nodes_name_unique). Reject a rename that
		// would collide with a different node up-front for a clean message.
		if existing, nameErr := h.state.Store.GetNodeByName(name); nameErr == nil && existing != nil && existing.ID != node.ID {
			sendJSONError(w, "a node with this name already exists", 409)
			return
		}
	}

	tags := strings.TrimSpace(req.Tags)

	if err := h.state.Store.SetNodeConfig(id, name, region, tags); err != nil {
		log.Printf("ConfigureNode: SetNodeConfig failed (id=%d): %v", id, err)
		sendJSONError(w, "Failed to save node configuration (name may already be in use)", 500)
		return
	}

	updated, _ := h.state.Store.GetNodeByID(id)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Node configured",
		"node":    updated,
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

	servers, _ := h.state.Store.ListServersByNode(id)

	// Delete all servers on this node first (FK constraint)
	if err := h.state.Store.DeleteServersByNode(id); err != nil {
		sendJSONError(w, "Failed to delete servers on node", 500)
		return
	}

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
	nodeOnline := false
	if h.state.Redis != nil {
		key := "dylaris:discovery:" + node.Token
		if val, redisErr := h.state.Redis.Get(r.Context(), key).Result(); redisErr == nil {
			nodeOnline = true
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
		ID   int    `json:"id"`
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
	// Only compute the missing list against an online node — an offline node has no
	// fresh disk view and would mislabel every server as missing.
	if nodeOnline {
		for u, srv := range dbByUUID {
			if !diskUUIDs[u] {
				missing = append(missing, missingEntry{ID: srv.ID, UUID: u, Name: srv.Name})
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"nodeOnline": nodeOnline,
		"matched":    matched,
		"orphaned":   orphaned,
		"missing":    missing,
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
	// Path-traversal guard: orphan folder names must be safe filename chars only.
	// Standard UUIDs match, but so do legacy/test folders (e.g. "test-server-1").
	// We don't strictly require UUIDv4 since orphans are scanned from disk and
	// may have non-UUID names from manual setups.
	if !isSafeOrphanName(orphanUUID) {
		sendJSONError(w, "invalid uuid: only a-z, A-Z, 0-9, '-' and '_' allowed, max 64 chars", 400)
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

// ListOrphanFiles lists the files inside an orphaned folder on a node.
// GET /api/disk/orphans/{nodeId}/{uuid}/files?path=...
// Admin-only, read-only. No DB servers row required.
func (h *NodeHandler) ListOrphanFiles(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	nodeID, _ := strconv.Atoi(vars["nodeId"])
	orphanUUID := vars["uuid"]

	if !isSafeOrphanName(orphanUUID) {
		sendJSONError(w, "invalid uuid: only a-z, A-Z, 0-9, '-' and '_' allowed, max 64 chars", 400)
		return
	}

	pathParam := r.URL.Query().Get("path")
	// Reject any path containing ".." or starting with "/" to prevent traversal outside the orphan root.
	if strings.Contains(pathParam, "..") || (len(pathParam) > 0 && pathParam[0] == '/') {
		sendJSONError(w, "Path must not contain '..' or start with '/'", 400)
		return
	}
	// Default to root when path is empty.
	if pathParam == "" {
		pathParam = "/"
	}

	if h.state.GRPCRegistry == nil {
		sendJSONError(w, "gRPC not available", 503)
		return
	}

	resp, err := h.state.GRPCRegistry.SendRequest(nodeID, &pb.NodeMessage{
		RequestId:  uuid.NewString(),
		ServerUuid: orphanUUID,
		Payload:    &pb.NodeMessage_ListReq{ListReq: &pb.ListFilesReq{Path: pathParam}},
	}, 30*time.Second)
	if err != nil {
		sendJSONError(w, fmt.Sprintf("Node communication error: %v", err), 502)
		return
	}
	if errResp := resp.GetError(); errResp != nil {
		sendJSONError(w, errResp.Message, int(errResp.Code))
		return
	}

	listResp := resp.GetListResp()
	if listResp == nil {
		sendJSONError(w, "Unexpected response from node", 500)
		return
	}

	type fileEntry struct {
		Name  string `json:"name"`
		IsDir bool   `json:"is_dir"`
		Size  int64  `json:"size"`
	}
	files := make([]fileEntry, 0, len(listResp.Files))
	for _, f := range listResp.Files {
		files = append(files, fileEntry{Name: f.Name, IsDir: f.IsDir, Size: f.Size})
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"files":   files,
	})
}

// GetOrphanFileContent returns the text content of a file inside an orphaned folder.
// GET /api/disk/orphans/{nodeId}/{uuid}/content?path=...
// Admin-only, read-only. No DB servers row required.
func (h *NodeHandler) GetOrphanFileContent(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	nodeID, _ := strconv.Atoi(vars["nodeId"])
	orphanUUID := vars["uuid"]

	if !isSafeOrphanName(orphanUUID) {
		sendJSONError(w, "invalid uuid: only a-z, A-Z, 0-9, '-' and '_' allowed, max 64 chars", 400)
		return
	}

	pathParam := r.URL.Query().Get("path")
	if strings.Contains(pathParam, "..") || (len(pathParam) > 0 && pathParam[0] == '/') {
		sendJSONError(w, "Path must not contain '..' or start with '/'", 400)
		return
	}
	if pathParam == "" {
		sendJSONError(w, "path query param required", 400)
		return
	}

	if h.state.GRPCRegistry == nil {
		sendJSONError(w, "gRPC not available", 503)
		return
	}

	reqID := uuid.NewString()
	msg := &pb.NodeMessage{
		RequestId:  reqID,
		ServerUuid: orphanUUID,
		Payload: &pb.NodeMessage_ReadReq{
			ReadReq: &pb.ReadFileReq{Path: pathParam, ZipIfDir: false},
		},
	}

	ch, err := h.state.GRPCRegistry.SendRequestStreaming(nodeID, msg)
	if err != nil {
		sendJSONError(w, fmt.Sprintf("Node communication error: %v", err), 502)
		return
	}
	defer h.state.GRPCRegistry.CleanupRequest(nodeID, reqID)

	var buf bytes.Buffer
	for resp := range ch {
		if errResp := resp.GetError(); errResp != nil {
			sendJSONError(w, errResp.Message, int(errResp.Code))
			return
		}
		if chunk := resp.GetChunk(); chunk != nil {
			buf.Write(chunk.Data)
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"content": buf.String(),
	})
}

// AssignOrphanRequest is the body for POST /api/disk/orphans/assign.
type AssignOrphanRequest struct {
	NodeID      int     `json:"node_id"`
	UUID        string  `json:"uuid"`
	Name        string  `json:"name"`
	OwnerUserID *string `json:"owner_user_id"` // existing user, or nil
	NewUser     *struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"new_user"` // create a new user, or nil
	MemoryMB int     `json:"memory_mb"`
	CPULimit float64 `json:"cpu_limit"`
}

// dylarisMetadata mirrors the relevant fields of .dylaris.json on disk.
type dylarisMetadata struct {
	Name            string  `json:"name"`
	MemoryMB        int     `json:"memory_mb"`
	CPULimit        float64 `json:"cpu_limit"`
	ActiveSubServer string  `json:"active_sub_server"`
	SubServers      []struct {
		Name             string `json:"name"`
		Type             string `json:"type"`
		MinecraftVersion string `json:"minecraft_version"`
		Build            string `json:"build"`
	} `json:"sub_servers"`
}

// inspectOrphanOnNode sends an inspect_orphan gRPC request to nodeID for orphanUUID
// and returns the parsed response. Returns nil, error on gRPC failure or missing orphan.
func (h *NodeHandler) inspectOrphanOnNode(nodeID int, orphanUUID string) (*pb.InspectOrphanResp, error) {
	resp, err := h.state.GRPCRegistry.SendRequest(nodeID, &pb.NodeMessage{
		RequestId:  uuid.NewString(),
		ServerUuid: orphanUUID,
		Payload:    &pb.NodeMessage_InspectOrphanReq{InspectOrphanReq: &pb.InspectOrphanReq{}},
	}, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("node communication error: %w", err)
	}
	if errResp := resp.GetError(); errResp != nil {
		code := int(errResp.Code)
		if code == 0 {
			code = 500
		}
		return nil, fmt.Errorf("node error %d: %s", code, errResp.Message)
	}
	orphanInfo := resp.GetInspectOrphanResp()
	if orphanInfo == nil {
		return nil, fmt.Errorf("orphan folder not found or inspect failed")
	}
	return orphanInfo, nil
}

// InspectOrphan returns metadata about an orphaned folder without assigning it.
// GET /api/disk/orphans/{nodeId:[0-9]+}/{uuid}/inspect  (admin-only)
func (h *NodeHandler) InspectOrphan(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	nodeID, _ := strconv.Atoi(vars["nodeId"])
	orphanUUID := vars["uuid"]

	if !isSafeOrphanName(orphanUUID) {
		sendJSONError(w, "invalid uuid: only a-z, A-Z, 0-9, '-' and '_' allowed, max 64 chars", 400)
		return
	}

	if h.state.GRPCRegistry == nil {
		sendJSONError(w, "gRPC not available", 503)
		return
	}

	orphanInfo, err := h.inspectOrphanOnNode(nodeID, orphanUUID)
	if err != nil {
		sendJSONError(w, err.Error(), 502)
		return
	}

	// Parse metadata JSON server-side so the panel receives a clean object.
	var metadataObj interface{}
	if orphanInfo.HasMetadata && orphanInfo.MetadataJson != "" {
		var meta dylarisMetadata
		if jsonErr := json.Unmarshal([]byte(orphanInfo.MetadataJson), &meta); jsonErr == nil {
			metadataObj = meta
		}
	}

	type subServerInfo struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	subServers := make([]subServerInfo, 0, len(orphanInfo.SubServers))
	for _, ss := range orphanInfo.SubServers {
		subServers = append(subServers, subServerInfo{Name: ss.Name, Type: ss.Type})
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"metadata":          metadataObj,
		"active_sub_server": orphanInfo.ActiveSubServer,
		"sub_servers":       subServers,
	})
}

// AssignOrphan adopts an on-disk orphan folder into a proper DB server row.
// POST /api/disk/orphans/assign  (admin-only)
//
// It:
//  1. Validates the request.
//  2. Rejects if a servers row for the UUID already exists (409).
//  3. Optionally creates a new owner user (bcrypt password).
//  4. Asks the node to inspect the orphan to discover the active sub-server
//     and its installer type / minecraft version.
//  5. Inserts the servers row (status=stopped) + calls UpdateServerSetup to
//     persist installer_type, minecraft_version, active_sub_server.
//  6. Returns the created server as JSON.
func (h *NodeHandler) AssignOrphan(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	if h.state.Store == nil {
		sendJSONError(w, "DB error", 503)
		return
	}

	var req AssignOrphanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON body", 400)
		return
	}

	// --- Input validation ---
	if !isSafeOrphanName(req.UUID) {
		sendJSONError(w, "invalid uuid: only a-z, A-Z, 0-9, '-' and '_' allowed, max 64 chars", 400)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		sendJSONError(w, "name is required", 400)
		return
	}
	if req.MemoryMB <= 0 {
		sendJSONError(w, "memory_mb must be > 0", 400)
		return
	}
	if req.NodeID <= 0 {
		sendJSONError(w, "node_id is required", 400)
		return
	}
	// Exactly one owner source must be provided.
	if req.OwnerUserID == nil && req.NewUser == nil {
		sendJSONError(w, "exactly one of owner_user_id or new_user must be set", 400)
		return
	}
	if req.OwnerUserID != nil && req.NewUser != nil {
		sendJSONError(w, "exactly one of owner_user_id or new_user must be set, not both", 400)
		return
	}
	if req.NewUser != nil {
		if strings.TrimSpace(req.NewUser.Username) == "" || req.NewUser.Password == "" {
			sendJSONError(w, "new_user.username and new_user.password are required", 400)
			return
		}
	}

	// --- Duplicate check ---
	existing, err := h.state.Store.GetServerByUUID(req.UUID)
	if err != nil {
		sendJSONError(w, "database error checking existing server", 500)
		return
	}
	if existing != nil {
		sendJSONError(w, "A server with this UUID already exists in the database", 409)
		return
	}

	// --- gRPC availability check (before any user creation) ---
	if h.state.GRPCRegistry == nil {
		sendJSONError(w, "gRPC not available", 503)
		return
	}

	// --- Resolve owner ---
	ownerID := ""
	if req.OwnerUserID != nil {
		user, err := h.state.Store.GetUserByID(*req.OwnerUserID)
		if err != nil || user == nil {
			sendJSONError(w, fmt.Sprintf("User with id %s not found", *req.OwnerUserID), 400)
			return
		}
		ownerID = user.ID
	} else {
		// Create a new non-admin user.
		hashed, err := bcrypt.GenerateFromPassword([]byte(req.NewUser.Password), bcrypt.DefaultCost)
		if err != nil {
			sendJSONError(w, "Failed to hash password", 500)
			return
		}
		newUser := &models.User{
			Username: req.NewUser.Username,
			Password: string(hashed),
			IsAdmin:  false,
		}
		if err := h.state.Store.CreateUser(newUser); err != nil {
			log.Printf("AssignOrphan: CreateUser failed for username=%q: %v", req.NewUser.Username, err)
			sendJSONError(w, "Failed to create user (username may already exist)", 409)
			return
		}
		ownerID = newUser.ID
	}

	// --- Inspect the orphan on the node ---
	orphanInfo, err := h.inspectOrphanOnNode(req.NodeID, req.UUID)
	if err != nil {
		sendJSONError(w, fmt.Sprintf("Node communication error: %v", err), 502)
		return
	}

	activeSubServer := orphanInfo.ActiveSubServer
	installerType := ""
	minecraftVersion := ""
	buildNumber := ""

	if orphanInfo.HasMetadata && orphanInfo.MetadataJson != "" {
		var meta dylarisMetadata
		if err := json.Unmarshal([]byte(orphanInfo.MetadataJson), &meta); err == nil {
			for _, ss := range meta.SubServers {
				if ss.Name == activeSubServer {
					installerType = ss.Type
					minecraftVersion = ss.MinecraftVersion
					buildNumber = ss.Build
					break
				}
			}
		}
	}
	// Fall back to SubServers list from the proto response if metadata was absent
	// or if the active sub-server wasn't found in metadata.
	if installerType == "" {
		for _, ss := range orphanInfo.SubServers {
			if ss.Name == activeSubServer {
				installerType = ss.Type
				// SubServerInfo carries no minecraft_version; leave it empty.
				break
			}
		}
	}

	// --- Create the servers DB row ---
	srv := &models.Server{
		UUID:            req.UUID,
		Name:            strings.TrimSpace(req.Name),
		NodeID:          req.NodeID,
		OwnerID:         ownerID,
		Memory:          req.MemoryMB,
		CPULimit:        req.CPULimit,
		Status:          "stopped",
		ActiveSubServer: activeSubServer,
		ServerType:      "game",
	}

	newID, err := h.state.Store.CreateServer(srv)
	if err != nil {
		log.Printf("AssignOrphan: CreateServer failed (uuid=%s, owner=%s): %v", req.UUID, ownerID, err)
		if req.NewUser != nil {
			log.Printf("AssignOrphan: WARNING — new user (id=%s username=%q) was created but server insert failed; manual cleanup may be required", ownerID, req.NewUser.Username)
		}
		sendJSONError(w, "Failed to create server record", 500)
		return
	}
	srv.ID = int(newID)

	// Persist installer_type, minecraft_version, active_sub_server, build via UpdateServerSetup.
	// game_image and start_command are empty for adopted servers — the node already
	// knows the real command from its own .dylaris.json / active-server file.
	if err := h.state.Store.UpdateServerSetup(srv.ID, "", "", activeSubServer, "", installerType, minecraftVersion, buildNumber); err != nil {
		log.Printf("AssignOrphan: UpdateServerSetup failed (id=%d): %v", srv.ID, err)
		// Non-fatal: the row exists; setup fields just remain empty.
	}

	srv.InstallerType = installerType
	srv.MinecraftVersion = minecraftVersion
	srv.BuildNumber = buildNumber

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"server":  srv,
	})
}
