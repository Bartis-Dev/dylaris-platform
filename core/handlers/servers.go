package handlers

import (
	"context"
	"dylaris-core/models"
	"dylaris-core/store"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// defaultJvmFlags are always injected into the start command but not stored in extra_jvm_flags.
const defaultJvmFlags = "-Dterminal.ansi=true -Djline.terminal=jline.UnsupportedTerminal"

// aikarsFlags are Aikar's optimized G1GC flags for Minecraft servers (standard RAM).
const aikarsFlags = "-XX:+UseG1GC -XX:+ParallelRefProcEnabled -XX:MaxGCPauseMillis=200 " +
	"-XX:+UnlockExperimentalVMOptions -XX:+DisableExplicitGC -XX:+AlwaysPreTouch " +
	"-XX:G1NewSizePercent=30 -XX:G1MaxNewSizePercent=40 -XX:G1HeapRegionSize=8M " +
	"-XX:G1ReservePercent=20 -XX:G1HeapWastePercent=5 -XX:G1MixedGCCountTarget=4 " +
	"-XX:InitiatingHeapOccupancyPercent=15 -XX:G1MixedGCLiveThresholdPercent=90 " +
	"-XX:G1RSetUpdatingPauseTimePercent=5 -XX:SurvivorRatio=32 " +
	"-XX:+PerfDisableSharedMem -XX:MaxTenuringThreshold=1"

// aikarsHighMemFlags are Aikar's flags tuned for 12GB+ RAM servers.
const aikarsHighMemFlags = "-XX:+UseG1GC -XX:+ParallelRefProcEnabled -XX:MaxGCPauseMillis=200 " +
	"-XX:+UnlockExperimentalVMOptions -XX:+DisableExplicitGC -XX:+AlwaysPreTouch " +
	"-XX:G1NewSizePercent=40 -XX:G1MaxNewSizePercent=50 -XX:G1HeapRegionSize=16M " +
	"-XX:G1ReservePercent=20 -XX:G1HeapWastePercent=5 -XX:G1MixedGCCountTarget=4 " +
	"-XX:InitiatingHeapOccupancyPercent=15 -XX:G1MixedGCLiveThresholdPercent=90 " +
	"-XX:G1RSetUpdatingPauseTimePercent=5 -XX:SurvivorRatio=32 " +
	"-XX:+PerfDisableSharedMem -XX:MaxTenuringThreshold=1"

// checkServerAccess verifies that the user has access to the server.
// Owner and Admin always have full access. Invited users need the specific permission.
func checkServerAccess(st store.Store, srv *models.Server, username string, isAdmin bool, userID int, requiredPerm string) bool {
	if isAdmin || srv.OwnerName == username {
		return true
	}
	if userID == 0 {
		return false
	}

	// Check direct invite first
	invite, err := st.GetInvite(srv.ID, userID)
	if err != nil {
		// No direct invite — check inherited access via proxy
		if srv.ProxyID != nil {
			proxyInvite, proxyErr := st.GetInvite(*srv.ProxyID, userID)
			if proxyErr == nil && proxyInvite.Permissions.Inherit {
				return checkPerm(proxyInvite.Permissions, requiredPerm)
			}
		}
		return false
	}
	return checkPerm(invite.Permissions, requiredPerm)
}

func checkPerm(perms models.TabPermissions, requiredPerm string) bool {
	switch requiredPerm {
	case "console":
		return perms.Console
	case "files":
		return perms.Files
	case "config":
		return perms.Config
	case "setup":
		return perms.Setup
	case "power":
		return perms.Power
	case "members":
		return perms.Members
	case "network":
		return perms.Network
	case "overview":
		return true // Overview is always accessible for any invited user
	}
	return false
}

// sanitizeServerName allows only a-z A-Z 0-9 - + _  (spaces are replaced with _)
var serverNameRegex = regexp.MustCompile(`[^a-zA-Z0-9\-_+]`)

func sanitizeServerName(name string) string {
	name = strings.ReplaceAll(name, " ", "_")
	return serverNameRegex.ReplaceAllString(name, "")
}

// ==========================================
// REQUEST TYPES
// ==========================================

type CreateServerRequest struct {
	UUID       string `json:"uuid"`
	Name       string `json:"name"`
	NodeID     string `json:"nodeId"`
	OwnerID    int    `json:"ownerId"`
	IsFixed    *bool  `json:"isFixed"`
	ServerType string `json:"serverType"`
	Docker     struct {
		RAM       int     `json:"ram"`
		CPULimit  float64 `json:"cpuLimit"`
		DiskLimit int64   `json:"diskLimit"`
	} `json:"docker"`
}

type SetupServerRequest struct {
	SubServerName string `json:"subServerName"`
	JavaImage     string `json:"javaImage"`
	ExtraJvmFlags string `json:"extraJvmFlags"`
	Installer     struct {
		Type      string `json:"type"`      // "paper", "vanilla", "forge", "library", "upload", "upload-zip"
		Version   string `json:"version"`   // build/version identifier
		McVersion string `json:"mcVersion"` // major MC version (e.g. "1.21.4")
		URL       string `json:"url"`       // for import via URL
		Path      string `json:"path"`      // for library selection
		Structure string `json:"structure"` // "direct" or "subfolder" (for upload-zip)
	} `json:"installer"`
}

type SwitchSubServerRequest struct {
	SubServerName string `json:"subServerName"`
}

type PowerActionRequest struct {
	Action string `json:"action"` // "start", "stop", "restart", "kill"
}

// ==========================================
// HANDLER
// ==========================================

type ServerHandler struct {
	state *AppState
}

func NewServerHandler(state *AppState) *ServerHandler {
	return &ServerHandler{state: state}
}

func (h *ServerHandler) GetServers(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	isAdmin := r.Context().Value("isAdmin").(bool)
	userID, _ := r.Context().Value("userID").(int)

	servers, err := h.state.Store.ListServersForUser(userID, isAdmin)
	if err != nil {
		sendJSONError(w, "Database error", 500)
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

// CreateServer (Step 1): Creates container with resources, status=pending_setup
func (h *ServerHandler) CreateServer(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	var req CreateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	var nodeIDInt int
	fmt.Sscanf(req.NodeID, "%d", &nodeIDInt)

	node, err := h.state.Store.GetNodeByID(nodeIDInt)
	if err != nil {
		sendJSONError(w, "Node not found", 404)
		return
	}

	// Resolve owner username for container naming
	owner, err := h.state.Store.GetUserByID(req.OwnerID)
	if err != nil {
		sendJSONError(w, "Owner not found", 404)
		return
	}

	// Container-Name: Username-Server-N
	count, _ := h.state.Store.CountServersByOwner(req.OwnerID)
	containerName := fmt.Sprintf("%s-Server-%02d", owner.Username, count+1)

	isFixedVal := true
	if req.IsFixed != nil {
		isFixedVal = *req.IsFixed
	}

	// Default server type to "game" if not specified
	serverType := req.ServerType
	if serverType != "proxy" {
		serverType = "game"
	}

	// Block proxy creation if feature is disabled
	if serverType == "proxy" {
		val, _ := h.state.Store.GetSetting("feature_proxy_enabled")
		if val == "false" {
			sendJSONError(w, "Proxy feature is disabled", 403)
			return
		}
	}

	// RAM is stored without buffer (buffer is added on the Node side)
	srv := &models.Server{
		UUID:            req.UUID,
		Name:            containerName,
		NodeID:          nodeIDInt,
		OwnerID:         req.OwnerID,
		GameImage:       "ghcr.io/bartis-dev/dylaris-mc-java21:latest", // Default, overridden during setup
		Port:            25565,
		Memory:          req.Docker.RAM,
		CPULimit:        req.Docker.CPULimit,
		StartCommand:    "",
		Status:          "pending_setup",
		IsFixed:         isFixedVal,
		ActiveSubServer: "",
		ExtraJvmFlags:   "",
		DiskLimit:       req.Docker.DiskLimit,
		ServerType:      serverType,
	}

	serverID, err := h.state.Store.CreateServer(srv)
	if err != nil {
		log.Printf("Create Server DB Error: %v", err)
		sendJSONError(w, "Failed to save server", 500)
		return
	}

	// Node: Only create directory + container (no install)
	if h.state.Queue != nil {
		configPayload := map[string]interface{}{
			"uuid": req.UUID,
			"docker": map[string]interface{}{
				"image":      srv.GameImage,
				"ram":        req.Docker.RAM,
				"cpuLimit":   req.Docker.CPULimit,
				"cpusetCpus": node.CpusetCpus,
				"diskLimit":  req.Docker.DiskLimit,
				"command":    "",
			},
		}

		if err := h.state.Queue.SendCommand(context.Background(), node.Token, "create", configPayload, nil); err != nil {
			log.Printf("Redis Queue Failed: %v", err)
		} else {
			log.Printf("Create command queued for %s", node.Token)
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"server_id": serverID,
		"message":   "Server container creation queued",
	})
}

// SetupServer (Step 2): User configures the server
func (h *ServerHandler) SetupServer(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return
	}

	var req SetupServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", 404)
		return
	}

	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)

	if !isAdmin && srv.OwnerName != username {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	// Check install cooldown (30s between installs, admins bypass)
	cooldownKey := fmt.Sprintf("dylaris:server:%s:install-start", srv.UUID)
	if !isAdmin {
		if ttl, err := h.state.Redis.TTL(context.Background(), cooldownKey).Result(); err == nil && ttl > 0 {
			sendJSONError(w, fmt.Sprintf("Please wait %d seconds before installing again", int(ttl.Seconds())), 429)
			return
		}
	}

	subName := sanitizeServerName(req.SubServerName)
	if subName == "" {
		sendJSONError(w, "Invalid server name", 400)
		return
	}

	// Enforce sub-server limit (skip for first setup / admins)
	if srv.Status != "pending_setup" {
		maxSub := 3 // default
		if val, err := h.state.Store.GetSetting("srv.max_sub_servers"); err == nil && val != "" {
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
				maxSub = n
			}
		}
		if maxSub > 0 && h.state.Redis != nil {
			diskKey := fmt.Sprintf("dylaris:server:%s:stats:disk", srv.UUID)
			if data, err := h.state.Redis.Get(context.Background(), diskKey).Result(); err == nil {
				var diskPayload struct {
					SubServers map[string]int64 `json:"subServers"`
				}
				if json.Unmarshal([]byte(data), &diskPayload) == nil && len(diskPayload.SubServers) >= maxSub {
					sendJSONError(w, fmt.Sprintf("Sub-server limit reached (%d). Increase the limit in Settings → Servers.", maxSub), 400)
					return
				}
			}
		}
	}

	// Build start command: RAM from DB (without buffer), ExtraFlags from user
	extraFlags := strings.TrimSpace(req.ExtraJvmFlags)
	if extraFlags == "" {
		if srv.Memory >= 12288 {
			extraFlags = aikarsHighMemFlags
		} else {
			extraFlags = aikarsFlags
		}
	}
	startCommand := fmt.Sprintf("java -Xms%dM -Xmx%dM %s %s -jar server.jar nogui",
		srv.Memory, srv.Memory, defaultJvmFlags, extraFlags)
	startCommand = strings.Join(strings.Fields(startCommand), " ") // Remove extra spaces

	// Update DB
	if err := h.state.Store.UpdateServerSetup(serverID, req.JavaImage, startCommand, subName, extraFlags, req.Installer.Type, req.Installer.McVersion, req.Installer.Version); err != nil {
		sendJSONError(w, "Failed to update server", 500)
		return
	}
	h.state.Store.UpdateServerStatus(serverID, "installing")

	// Node command
	if h.state.Queue != nil {
		node, err := h.state.Store.GetNodeByID(srv.NodeID)
		if err != nil {
			sendJSONError(w, "Node not found", 404)
			return
		}

		configPayload := map[string]interface{}{
			"uuid": srv.UUID,
			"docker": map[string]interface{}{
				"image":      req.JavaImage,
				"ram":        srv.Memory,
				"cpuLimit":   srv.CPULimit,
				"cpusetCpus": node.CpusetCpus,
				"command":    startCommand,
			},
			"activeSubServer": subName,
		}
		installerPayload := map[string]interface{}{
			"type":      req.Installer.Type,
			"version":   req.Installer.Version,
			"url":       req.Installer.URL,
			"path":      req.Installer.Path,
			"structure": req.Installer.Structure,
		}

		if err := h.state.Queue.SendCommand(context.Background(), node.Token, "setup", configPayload, installerPayload); err != nil {
			log.Printf("Redis Queue Failed: %v", err)
			sendJSONError(w, "Failed to queue setup", 500)
			return
		}

		// Set install cooldown
		h.state.Redis.Set(context.Background(), cooldownKey, "1", 30*time.Second)

		// Setup installs and starts the server — mark desired state as online
		h.state.Store.UpdateServerDesiredState(srv.ID, "online")
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Server setup queued",
	})
}

// ReinstallServer: Reinstalls the active sub-server (version update)
func (h *ServerHandler) ReinstallServer(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return
	}

	var req struct {
		Installer struct {
			Type      string `json:"type"`
			Version   string `json:"version"`
			McVersion string `json:"mcVersion"`
			URL       string `json:"url"`
			Path      string `json:"path"`
			Structure string `json:"structure"`
		} `json:"installer"`
		JavaImage     string `json:"javaImage"`
		ExtraJvmFlags string `json:"extraJvmFlags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", 404)
		return
	}

	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)
	if !isAdmin && srv.OwnerName != username {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	if srv.Status == "pending_setup" {
		sendJSONError(w, "Server must be set up before reinstalling", 400)
		return
	}

	// Check install cooldown (30s, admins bypass)
	cooldownKey := fmt.Sprintf("dylaris:server:%s:install-start", srv.UUID)
	if !isAdmin {
		if ttl, err := h.state.Redis.TTL(context.Background(), cooldownKey).Result(); err == nil && ttl > 0 {
			sendJSONError(w, fmt.Sprintf("Please wait %d seconds before reinstalling", int(ttl.Seconds())), 429)
			return
		}
	}

	subName := srv.ActiveSubServer
	if subName == "" {
		sendJSONError(w, "No active sub-server to reinstall", 400)
		return
	}

	// Update image if provided
	javaImage := req.JavaImage
	if javaImage == "" {
		javaImage = srv.GameImage
	}

	extraFlags := strings.TrimSpace(req.ExtraJvmFlags)
	if extraFlags == "" {
		extraFlags = srv.ExtraJvmFlags
	}

	startCommand := fmt.Sprintf("java -Xms%dM -Xmx%dM %s %s -jar server.jar nogui", srv.Memory, srv.Memory, defaultJvmFlags, extraFlags)
	startCommand = strings.Join(strings.Fields(startCommand), " ")

	// Update DB
	installerType := req.Installer.Type
	if installerType == "" {
		installerType = srv.InstallerType
	}
	mcVersion := req.Installer.McVersion
	if mcVersion == "" {
		mcVersion = srv.MinecraftVersion
	}
	buildNumber := req.Installer.Version
	if buildNumber == "" {
		buildNumber = srv.BuildNumber
	}

	if err := h.state.Store.UpdateServerSetup(serverID, javaImage, startCommand, subName, extraFlags, installerType, mcVersion, buildNumber); err != nil {
		sendJSONError(w, "Failed to update server", 500)
		return
	}
	h.state.Store.UpdateServerStatus(serverID, "installing")

	if h.state.Queue != nil {
		node, err := h.state.Store.GetNodeByID(srv.NodeID)
		if err != nil {
			sendJSONError(w, "Node not found", 404)
			return
		}

		configPayload := map[string]interface{}{
			"uuid": srv.UUID,
			"docker": map[string]interface{}{
				"image":      javaImage,
				"ram":        srv.Memory,
				"cpuLimit":   srv.CPULimit,
				"cpusetCpus": node.CpusetCpus,
				"command":    startCommand,
			},
			"activeSubServer": subName,
		}
		installerPayload := map[string]interface{}{
			"type":      req.Installer.Type,
			"version":   req.Installer.Version,
			"url":       req.Installer.URL,
			"path":      req.Installer.Path,
			"structure": req.Installer.Structure,
		}

		if err := h.state.Queue.SendCommand(context.Background(), node.Token, "reinstall", configPayload, installerPayload); err != nil {
			log.Printf("Redis Queue Failed: %v", err)
			sendJSONError(w, "Failed to queue reinstall", 500)
			return
		}

		// Set install cooldown
		h.state.Redis.Set(context.Background(), cooldownKey, "1", 30*time.Second)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Server reinstall queued",
	})
}

// SwitchSubServer: Switches the active MC server in the container
func (h *ServerHandler) SwitchSubServer(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return
	}

	var req SwitchSubServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", 404)
		return
	}

	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)
	if !isAdmin && srv.OwnerName != username {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	subName := sanitizeServerName(req.SubServerName)
	if subName == "" {
		sendJSONError(w, "Invalid server name", 400)
		return
	}

	if err := h.state.Store.UpdateServerActiveSubServer(serverID, subName); err != nil {
		sendJSONError(w, "Failed to update server", 500)
		return
	}

	if h.state.Queue != nil {
		node, err := h.state.Store.GetNodeByID(srv.NodeID)
		if err == nil {
			switchPayload := map[string]interface{}{
				"uuid":            srv.UUID,
				"activeSubServer": subName,
				"docker": map[string]interface{}{
					"image":      srv.GameImage,
					"ram":        srv.Memory,
					"cpuLimit":   srv.CPULimit,
					"cpusetCpus": node.CpusetCpus,
					"command":    strings.Join(strings.Fields(fmt.Sprintf("java -Xms%dM -Xmx%dM %s %s -jar server.jar nogui", srv.Memory, srv.Memory, defaultJvmFlags, strings.TrimSpace(srv.ExtraJvmFlags))), " "),
				},
			}
			h.state.Queue.SendCommand(context.Background(), node.Token, "switch_server", switchPayload, nil)
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Sub-server switch queued",
	})
}


// ServerPowerHandler: Controls Start, Stop, Kill and Restart
func (h *ServerHandler) ServerPowerHandler(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return
	}

	var req PowerActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	validActions := map[string]bool{"start": true, "stop": true, "restart": true, "kill": true}
	if !validActions[req.Action] {
		sendJSONError(w, "Invalid action", 400)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", 404)
		return
	}

	// Server must be set up before it can be started
	if srv.Status == "pending_setup" {
		sendJSONError(w, "Server is not set up yet", 400)
		return
	}

	// Block start/restart when disk quota is full
	if srv.Status == "disk_full" && (req.Action == "start" || req.Action == "restart") {
		sendJSONError(w, "Server kann nicht gestartet werden — Speicherlimit erreicht. Dateien loeschen oder Limit erhoehen.", 400)
		return
	}

	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)
	userID, _ := r.Context().Value("userID").(int)

	if !isAdmin && srv.OwnerName != username {
		// Invited users need the "power" permission
		invite, err := h.state.Store.GetInvite(srv.ID, userID)
		if err != nil || !invite.Permissions.Power {
			sendJSONError(w, "Forbidden", 403)
			return
		}
	}

	if srv.Status == "suspended" && !isAdmin {
		sendJSONError(w, "Server is suspended. Action blocked.", 403)
		return
	}

	node, err := h.state.Store.GetNodeByID(srv.NodeID)
	if err != nil {
		sendJSONError(w, "Node not found", 404)
		return
	}

	newStatus := ""
	switch req.Action {
	case "start", "restart":
		newStatus = "starting"
		h.state.Store.UpdateServerDesiredState(srv.ID, "online")
	case "stop", "kill":
		newStatus = "stopping"
		h.state.Store.UpdateServerDesiredState(srv.ID, "stopped")
	}
	h.state.Store.UpdateServerStatus(srv.ID, newStatus)

	if h.state.Queue != nil {
		configPayload := map[string]interface{}{
			"uuid": srv.UUID,
		}
		if err := h.state.Queue.SendCommand(context.Background(), node.Token, req.Action, configPayload, nil); err != nil {
			log.Printf("Redis Queue Failed: %v", err)
			sendJSONError(w, "Failed to queue command", 500)
			return
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Action " + req.Action + " queued successfully",
	})
}

// UpdateServerName: PATCH /api/servers/{id}/name
func (h *ServerHandler) UpdateServerName(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	// Sanitize: only a-zA-Z0-9 - + space, collapse multiple spaces
	name := strings.Join(strings.Fields(req.Name), " ")
	validName := regexp.MustCompile(`^[a-zA-Z0-9\-+ ]{1,50}$`)
	if !validName.MatchString(name) {
		sendJSONError(w, "Invalid name: use a-z A-Z 0-9 - + and single spaces, max 50 chars", 400)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", 404)
		return
	}

	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)
	if !isAdmin && srv.OwnerName != username {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	if err := h.state.Store.UpdateServerName(serverID, name); err != nil {
		sendJSONError(w, "Failed to update name", 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// UpdateServerResources: PATCH /api/servers/{id}/resources
func (h *ServerHandler) UpdateServerResources(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return
	}

	var req struct {
		RAM       int     `json:"ram"`
		CPULimit  float64 `json:"cpuLimit"`
		DiskLimit int64   `json:"diskLimit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	if req.RAM < 256 {
		sendJSONError(w, "RAM must be at least 256 MB", 400)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", 404)
		return
	}

	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)
	if !isAdmin && srv.OwnerName != username {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	if err := h.state.Store.UpdateServerResources(serverID, req.RAM, req.CPULimit, req.DiskLimit); err != nil {
		sendJSONError(w, "Failed to update resources", 500)
		return
	}

	// Regenerate start_command with the new RAM value
	newStartCommand := fmt.Sprintf("java -Xms%dM -Xmx%dM %s %s -jar server.jar nogui",
		req.RAM, req.RAM, defaultJvmFlags, strings.TrimSpace(srv.ExtraJvmFlags))
	newStartCommand = strings.Join(strings.Fields(newStartCommand), " ")

	// Persist the updated start_command in the DB
	h.state.Store.UpdateServerSetup(serverID, srv.GameImage, newStartCommand,
		srv.ActiveSubServer, srv.ExtraJvmFlags,
		srv.InstallerType, srv.MinecraftVersion, srv.BuildNumber)

	// Inform node to recreate container with new resources
	if h.state.Queue != nil {
		node, err := h.state.Store.GetNodeByID(srv.NodeID)
		if err == nil {
			payload := map[string]interface{}{
				"uuid":            srv.UUID,
				"activeSubServer": srv.ActiveSubServer,
				"docker": map[string]interface{}{
					"ram":        req.RAM,
					"cpuLimit":   req.CPULimit,
					"cpusetCpus": node.CpusetCpus,
					"diskLimit":  req.DiskLimit,
					"image":      srv.GameImage,
					"command":    newStartCommand,
				},
			}
			h.state.Queue.SendCommand(context.Background(), node.Token, "update_resources", payload, nil)
		}
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// DeleteSubServer: Delete a single sub-server
func (h *ServerHandler) DeleteSubServer(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return
	}
	subServerName := vars["subServerName"]
	if subServerName == "" {
		sendJSONError(w, "Sub-server name required", 400)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", 404)
		return
	}

	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)

	if !isAdmin && srv.OwnerName != username {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	node, err := h.state.Store.GetNodeByID(srv.NodeID)
	if err == nil && h.state.Queue != nil {
		configPayload := map[string]interface{}{
			"uuid":            srv.UUID,
			"activeSubServer": subServerName,
		}
		h.state.Queue.SendCommand(context.Background(), node.Token, "delete_sub_server", configPayload, nil)
	}

	// If deleting the active sub-server, reset to pending_setup
	if srv.ActiveSubServer == subServerName {
		h.state.Store.UpdateServerStatus(serverID, "pending_setup")
		h.state.Store.UpdateServerActiveSubServer(serverID, "")
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// DeleteServer: Completely delete a server
func (h *ServerHandler) DeleteServer(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", 404)
		return
	}

	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)

	if !isAdmin && srv.OwnerName != username {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	node, err := h.state.Store.GetNodeByID(srv.NodeID)
	if err == nil && h.state.Queue != nil {
		configPayload := map[string]interface{}{
			"uuid": srv.UUID,
		}
		h.state.Queue.SendCommand(context.Background(), node.Token, "delete", configPayload, nil)
	}

	if err := h.state.Store.DeleteServer(serverID); err != nil {
		sendJSONError(w, "Delete failed", 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// LinkServerToProxy: Link a game server to a proxy server
func (h *ServerHandler) LinkServerToProxy(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	// Check if proxy feature is enabled
	val, _ := h.state.Store.GetSetting("feature_proxy_enabled")
	if val == "false" {
		sendJSONError(w, "Proxy feature is disabled", 403)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return
	}

	var req struct {
		ProxyID int `json:"proxyId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProxyID == 0 {
		sendJSONError(w, "proxyId required", 400)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", 404)
		return
	}

	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)

	if !isAdmin && srv.OwnerName != username {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	if srv.ServerType == "proxy" {
		sendJSONError(w, "Cannot link a proxy to another proxy", 400)
		return
	}

	proxy, err := h.state.Store.GetServerByID(req.ProxyID)
	if err != nil {
		sendJSONError(w, "Proxy not found", 404)
		return
	}

	if proxy.ServerType != "proxy" {
		sendJSONError(w, "Target server is not a proxy", 400)
		return
	}

	proxyID := req.ProxyID
	if err := h.state.Store.UpdateServerProxyID(serverID, &proxyID); err != nil {
		sendJSONError(w, "Failed to link server", 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// UnlinkServerFromProxy: Remove a server's proxy link
func (h *ServerHandler) UnlinkServerFromProxy(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	// Check if proxy feature is enabled
	val, _ := h.state.Store.GetSetting("feature_proxy_enabled")
	if val == "false" {
		sendJSONError(w, "Proxy feature is disabled", 403)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", 404)
		return
	}

	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)

	if !isAdmin && srv.OwnerName != username {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	if err := h.state.Store.UpdateServerProxyID(serverID, nil); err != nil {
		sendJSONError(w, "Failed to unlink server", 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

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
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Migration queued. Server will be stopped and data moved to the new path.",
	})
}
