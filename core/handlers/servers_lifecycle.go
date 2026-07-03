package handlers

import (
	"context"
	"dylaris-core/models"
	"dylaris-core/services"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"
)

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

	// Auto-placement: when no explicit nodeId is given, defer to the
	// scheduler. Region and tags (AND-filtered) narrow the candidate
	// pool. With both empty the scheduler considers every online node.
	hasFilters := strings.TrimSpace(req.Region) != "" || len(req.Tags) > 0 || strings.TrimSpace(req.Tag) != ""
	if nodeIDInt == 0 && hasFilters {
		pickReq := PickNodeRequest{
			Region:   req.Region,
			Tags:     req.Tags,
			Tag:      req.Tag,
			RAMMB:    req.Docker.RAM,
			CPUCores: req.Docker.CPULimit,
			DiskGB:   int(req.Docker.DiskLimit / 1024),
		}
		// BYON: scope auto-placement to nodes this tenant may use (platform or
		// own), so the scheduler never picks a foreign node only to be rejected
		// by the ownership gate below. No-op when BYON is off or caller is admin.
		if byonActive(h.state, r) && !IsAdmin(r) {
			uid := byonCallerID(r)
			pickReq.OwnerScope = &uid
		}
		pick := (&PlacementHandler{state: h.state}).pickNode(r.Context(), pickReq)
		if !pick.Success || pick.Picked == nil {
			sendJSONError(w, "No node available: "+pick.Reason, http.StatusServiceUnavailable)
			return
		}
		nodeIDInt = pick.Picked.NodeID
		log.Printf("Placement: picked node %d (%s) region=%q tags=%v — %s",
			nodeIDInt, pick.Picked.NodeName, req.Region, req.Tags, pick.Picked.Reason)
	}

	node, err := h.state.Store.GetNodeByID(nodeIDInt)
	if err != nil {
		sendJSONError(w, "Node not found", 404)
		return
	}

	// BYON placement scoping: a tenant may only deploy on their OWN node. Gated by
	// feature_byon_enabled, so with BYON off this is a no-op and placement behaves
	// as today. Auto-placement is already owner-scoped above; this gate covers the
	// explicit-nodeId path and is belt-and-suspenders.
	if byonActive(h.state, r) && !canPlaceOnNode(h.state, r, node) {
		sendJSONError(w, "You can only deploy on your own nodes", http.StatusForbidden)
		return
	}

	// BYON abuse guard: cap the number of servers a tenant can stack on one of
	// their own nodes. The limit protects the shared control plane (every
	// container is a DB row + Redis keys + heartbeat/stats work), not the node's
	// own hardware. Scales with the node's thread count. Admins are exempt.
	if byonActive(h.state, r) && !IsAdmin(r) {
		if reached, capN := h.byonNodeServerCapReached(r.Context(), node); reached {
			sendJSONError(w, fmt.Sprintf("This node's server limit (%d) is reached. Stop/delete a server or add another node.", capN), http.StatusForbidden)
			return
		}
	}

	// Non-admins can only create servers they own. Only admins may set an
	// arbitrary OwnerID; otherwise any authenticated user could create
	// servers under another user's identity and burn their quotas.
	if isAdmin, _ := r.Context().Value("isAdmin").(bool); !isAdmin {
		userID, _ := r.Context().Value("userID").(string)
		if userID == "" {
			sendJSONError(w, "Unauthorized", 401)
			return
		}
		req.OwnerID = userID
	}

	// Validate owner exists before going further.
	if _, err := h.state.Store.GetUserByID(req.OwnerID); err != nil {
		sendJSONError(w, "Owner not found", 404)
		return
	}

	// Container name: Heroku-style slug like "crimson-otter-7a3f". The 4-hex
	// suffix gives ~65k entropy per adj+noun pair, collisions are vanishingly
	// rare; DB uniqueness still enforces final correctness on insert.
	containerName := services.GenerateContainerSlug()

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
		GameImage:       "ghcr.io/bartis-dev/dylaris-platform-mc-java21:latest", // Default, overridden during setup
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
		AutoMove:        req.AutoMove,
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
				"cpusetCpus": effectiveCpuset(srv.CPUPinningMode, srv.Cpuset, node.CpusetCpus),
				"diskLimit":  req.Docker.DiskLimit,
				"command":    "",
			},
		}

		if err := h.state.Queue.SendCommand(context.Background(), node.Token, "create", configPayload, nil); err != nil {
			log.Printf("Redis Queue Failed: %v", err)
		} else {
			log.Printf("Create command queued for %s", node.Token)
		}

		// For proxy-type servers, pre-create the proxy-isolation network so
		// subsequent LinkServerToProxy can attach game-servers immediately.
		if serverType == "proxy" {
			if err := h.state.Queue.SendProxyNetworkCommand(context.Background(), node.Token, "proxy_network_create", req.UUID, ""); err != nil {
				log.Printf("proxy_network_create queue failed: %v", err)
			}
		}
	}

	h.state.Events.Publish(r.Context(), "servers.changed", nil)

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

	// Atomically claim the install cooldown (30s, admins bypass). SetNX closes
	// the race where two concurrent requests both saw an expired TTL and both
	// enqueued a setup.
	cooldownKey := fmt.Sprintf("dylaris:server:%s:install-start", srv.UUID)
	if !isAdmin {
		acquired, err := h.state.Redis.SetNX(context.Background(), cooldownKey, "1", 30*time.Second).Result()
		if err == nil && !acquired {
			ttl, _ := h.state.Redis.TTL(context.Background(), cooldownKey).Result()
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

	// Build JVM flags: default/Aikar flags followed by server-specific extra flags.
	// These are forwarded to the node as a dedicated field; the node builds the
	// full java start command itself via buildStartCommand.
	extraFlags := strings.TrimSpace(req.ExtraJvmFlags)
	if extraFlags == "" {
		if srv.Memory >= 12288 {
			extraFlags = aikarsHighMemFlags
		} else {
			extraFlags = aikarsFlags
		}
	}
	combinedJvmFlags := strings.TrimSpace(defaultJvmFlags + " " + extraFlags)

	// Update DB (start_command is display-only; store combined flags in extra_jvm_flags)
	if err := h.state.Store.UpdateServerSetup(serverID, req.JavaImage, "", subName, extraFlags, req.Installer.Type, req.Installer.McVersion, req.Installer.Version); err != nil {
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

		// Unified pack install: authorize + materialize the build's .mrpack,
		// then rewrite req.Installer in place so the Node only ever sees the
		// existing "modpack" installer path. A foreign pack must never reach
		// the dispatch below.
		if req.Installer.Type == "pack" {
			if !h.state.FeatureFlags.IsModpacksEnabled(r.Context()) {
				sendJSONError(w, "Modpacks are disabled", http.StatusForbidden)
				return
			}

			pack, err := h.state.Store.GetPack(req.Installer.PackID)
			if err != nil || pack == nil {
				sendJSONError(w, "Pack not found", 404)
				return
			}
			packUserID, _ := r.Context().Value("userID").(string)
			if pack.OwnerID != packUserID && !isAdmin {
				sendJSONError(w, "Forbidden", 403)
				return
			}

			build, err := h.state.Store.GetPackBuild(req.Installer.BuildID)
			if err != nil || build == nil || build.PackID != pack.ID {
				sendJSONError(w, "Build not found", 404)
				return
			}

			ph := NewPacksHandler(h.state)
			key, err := ph.ensureInstallMrpack(pack, build)
			if err != nil {
				log.Printf("ensureInstallMrpack failed for pack %d build %d: %v", pack.ID, build.ID, err)
				sendJSONError(w, "Failed to prepare pack for install", 500)
				return
			}
			base, err := solderMirrorBase(h.state.Store.GetSetting)
			if err != nil {
				log.Printf("solderMirrorBase failed: %v", err)
				sendJSONError(w, "Failed to prepare pack for install", 500)
				return
			}

			req.Installer.Type = "modpack"
			req.Installer.URL = base + key
			req.Installer.Loader = build.Loader
			req.Installer.McVersion = build.Minecraft
			req.Installer.ModrinthProjectID = ""
			req.Installer.ModrinthVersionID = ""
			req.Installer.ModrinthProjectSlug = ""
		}

		configPayload := map[string]interface{}{
			"uuid": srv.UUID,
			"docker": map[string]interface{}{
				"image":         req.JavaImage,
				"ram":           srv.Memory,
				"cpuLimit":      srv.CPULimit,
				"cpusetCpus":    effectiveCpuset(srv.CPUPinningMode, srv.Cpuset, node.CpusetCpus),
				"extraJvmFlags": combinedJvmFlags,
			},
			"activeSubServer": subName,
		}
		installerPayload := map[string]interface{}{
			"type":      req.Installer.Type,
			"version":   req.Installer.Version,
			"loader":    req.Installer.Loader,
			"url":       req.Installer.URL,
			"path":      req.Installer.Path,
			"structure": req.Installer.Structure,
			// Modpack metadata forwarded to the node installer.
			"modrinthProjectId":   req.Installer.ModrinthProjectID,
			"modrinthVersionId":   req.Installer.ModrinthVersionID,
			"modrinthProjectSlug": req.Installer.ModrinthProjectSlug,
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

	h.state.Events.Publish(r.Context(), "servers.changed", nil)

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
			Loader    string `json:"loader"`
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

	// Atomically claim the install cooldown (30s, admins bypass) — SetNX closes
	// the concurrent-request race.
	cooldownKey := fmt.Sprintf("dylaris:server:%s:install-start", srv.UUID)
	if !isAdmin {
		acquired, err := h.state.Redis.SetNX(context.Background(), cooldownKey, "1", 30*time.Second).Result()
		if err == nil && !acquired {
			ttl, _ := h.state.Redis.TTL(context.Background(), cooldownKey).Result()
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
	combinedJvmFlags := strings.TrimSpace(defaultJvmFlags + " " + extraFlags)

	// Update DB (start_command is display-only; node builds the real command)
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

	if err := h.state.Store.UpdateServerSetup(serverID, javaImage, "", subName, extraFlags, installerType, mcVersion, buildNumber); err != nil {
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
				"image":         javaImage,
				"ram":           srv.Memory,
				"cpuLimit":      srv.CPULimit,
				"cpusetCpus":    effectiveCpuset(srv.CPUPinningMode, srv.Cpuset, node.CpusetCpus),
				"extraJvmFlags": combinedJvmFlags,
			},
			"activeSubServer": subName,
		}
		installerPayload := map[string]interface{}{
			"type":      req.Installer.Type,
			"version":   req.Installer.Version,
			"loader":    req.Installer.Loader,
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

	h.state.Events.Publish(r.Context(), "servers.changed", nil)

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
			combinedJvmFlags := strings.TrimSpace(defaultJvmFlags + " " + strings.TrimSpace(srv.ExtraJvmFlags))
			switchPayload := map[string]interface{}{
				"uuid":            srv.UUID,
				"activeSubServer": subName,
				"docker": map[string]interface{}{
					"image":         srv.GameImage,
					"ram":           srv.Memory,
					"cpuLimit":      srv.CPULimit,
					"cpusetCpus":    effectiveCpuset(srv.CPUPinningMode, srv.Cpuset, node.CpusetCpus),
					"extraJvmFlags": combinedJvmFlags,
				},
			}
			h.state.Queue.SendCommand(context.Background(), node.Token, "switch_server", switchPayload, nil)
		}
	}

	h.state.Events.Publish(r.Context(), "servers.changed", nil)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Sub-server switch queued",
	})
}

// GetInstallCooldown returns how many seconds remain on the post-install
// cooldown for a server, so the UI can disable power actions and show a
// countdown instead of letting the user click and get a 429. Returns 0
// when no cooldown is active. Admins still see the real number; the gate
// in ServerPowerHandler is the one that exempts them.
func (h *ServerHandler) GetInstallCooldown(w http.ResponseWriter, r *http.Request) {
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
	seconds := 0
	cooldownKey := fmt.Sprintf("dylaris:server:%s:install-start", srv.UUID)
	if ttl, err := h.state.Redis.TTL(context.Background(), cooldownKey).Result(); err == nil && ttl > 0 {
		seconds = int(ttl.Seconds())
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"seconds": seconds,
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
	userID, _ := r.Context().Value("userID").(string)

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

	// BYON billing suspend: a suspended tenant keeps read access but cannot
	// start/restart their servers until payment is settled (an operator can
	// reactivate). past_due (grace) is unaffected. Admins bypass.
	if (req.Action == "start" || req.Action == "restart") && !isAdmin {
		if b, err := h.state.Store.GetUserBilling(srv.OwnerID); err == nil && b.Status == "suspended" {
			sendJSONError(w, "Account suspended for non-payment. Settle payment to start your servers.", 403)
			return
		}
	}

	// Install cooldown: 30s after setup/reinstall the node sets a Redis key
	// with TTL so the freshly-installed server can boot to a stable state
	// without a foot-gun start/stop/kill during world generation. The same
	// key is checked on setup + reinstall to debounce double-clicks; we
	// gate power actions on it too. Admins bypass but the frontend prompts
	// for an explicit confirmation.
	if !isAdmin {
		cooldownKey := fmt.Sprintf("dylaris:server:%s:install-start", srv.UUID)
		if ttl, err := h.state.Redis.TTL(context.Background(), cooldownKey).Result(); err == nil && ttl > 0 {
			sendJSONError(w, fmt.Sprintf("Server is finishing install — please wait %d seconds", int(ttl.Seconds())), 429)
			return
		}
	}

	// Status-transition sanity: the frontend already disables nonsensical
	// transitions (Start on an online server, Stop on an offline one) but
	// the API was happy to forward them to the node anyway -- a misbehaving
	// client could spam Stop on a stopped server, queueing N redundant
	// commands the node has to chew through. Reject them at the boundary.
	isOffline := srv.Status == "stopped" || srv.Status == "offline" || srv.Status == "disk_full"
	isOnline := srv.Status == "online"
	switch req.Action {
	case "start":
		if isOnline {
			sendJSONError(w, "Server is already running", 409)
			return
		}
	case "stop", "kill", "restart":
		if isOffline {
			sendJSONError(w, "Server is not running", 409)
			return
		}
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

	LogServerAudit(h.state, r, serverID, ServerAuditEventPowerAction, userID, "", map[string]interface{}{
		"action": req.Action,
	})

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

	previousName := srv.Name
	if err := h.state.Store.UpdateServerName(serverID, name); err != nil {
		sendJSONError(w, "Failed to update name", 500)
		return
	}

	actorID, _ := r.Context().Value("userID").(string)
	LogServerAudit(h.state, r, serverID, ServerAuditEventNameChanged, actorID, "", map[string]interface{}{
		"from": previousName,
		"to":   name,
	})

	h.state.Events.Publish(r.Context(), "servers.changed", nil)

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// UpdateServerResources: PATCH /api/servers/{id}/resources
func (h *ServerHandler) UpdateServerResources(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	// Capability gate. Admins always pass; non-admins need the
	// can_change_resources flag set by an admin in the user-settings UI.
	userID, _ := r.Context().Value("userID").(string)
	perms := LoadEffectivePermissions(h.state, userID)
	if !perms.IsAdmin && !perms.CanChangeResources {
		sendJSONError(w, "Changing server resources requires elevated permissions — contact an administrator", 403)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return
	}

	var req struct {
		RAM           int     `json:"ram"`
		CPULimit      float64 `json:"cpuLimit"`
		DiskLimit     int64   `json:"diskLimit"`
		HostPort      int     `json:"hostPort"`      // admin-only
		ContainerPort int     `json:"containerPort"` // admin-only
		// Optional CPU pinning change. Omitted (nil) = leave pinning unchanged.
		CPUPinningMode *string `json:"cpuPinningMode"`
		Cpuset         *string `json:"cpuset"`
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

	// Optional per-server CPU pinning change. Resolved into an effective cpuset
	// and persisted here; it is applied to the container by the recreate below.
	if req.CPUPinningMode != nil && h.state.CPUPinning != nil {
		mode := strings.TrimSpace(*req.CPUPinningMode)
		pinNode, perr := h.state.Store.GetNodeByID(srv.NodeID)
		if perr != nil {
			sendJSONError(w, "Node not found", 404)
			return
		}
		var newCpuset string
		switch mode {
		case "shared":
			newCpuset = ""
		case "manual":
			cs := ""
			if req.Cpuset != nil {
				cs = strings.TrimSpace(*req.Cpuset)
			}
			if verr := h.state.CPUPinning.ValidateCpuset(r.Context(), pinNode.Token, cs, pinNode.CpusetCpus); verr != nil {
				sendJSONError(w, "Invalid cpuset: "+verr.Error(), 400)
				return
			}
			newCpuset = cs
		case "auto":
			// "" when the node has not reported a topology yet; mode stays 'auto'
			// and the effective cpuset falls back to the node default this time.
			newCpuset, _ = h.state.CPUPinning.AutoCpuset(r.Context(), pinNode.Token, srv.NodeID, srv.ID, req.CPULimit, pinNode.CpusetCpus)
		default:
			sendJSONError(w, "Invalid cpuPinningMode (shared|auto|manual)", 400)
			return
		}
		if err := h.state.Store.UpdateServerCPUPinning(srv.ID, mode, newCpuset); err != nil {
			sendJSONError(w, "Failed to save CPU pinning", 500)
			return
		}
		srv.CPUPinningMode = mode
		srv.Cpuset = newCpuset
	}

	// Port changes: admin-only
	portChanged := false
	if isAdmin && (req.HostPort > 0 || req.ContainerPort > 0) {
		newHostPort := req.HostPort
		if newHostPort == 0 {
			newHostPort = srv.HostPort
		}
		newContainerPort := req.ContainerPort
		if newContainerPort == 0 {
			newContainerPort = srv.ContainerPort
		}
		if newContainerPort == 0 {
			newContainerPort = 25565
		}
		// Conflict check: ensure no other server on this node uses the same host port
		if req.HostPort > 0 && req.HostPort != srv.HostPort {
			usedPorts, _ := h.state.Store.GetUsedHostPortsOnNode(srv.NodeID)
			for _, p := range usedPorts {
				if p == req.HostPort {
					sendJSONError(w, "Host port already in use on this node", 409)
					return
				}
			}
		}
		if err := h.state.Store.UpdateServerPorts(serverID, newHostPort, newContainerPort); err != nil {
			sendJSONError(w, "Failed to update ports", 500)
			return
		}
		portChanged = req.HostPort > 0 && req.HostPort != srv.HostPort
		srv.HostPort = newHostPort
		srv.ContainerPort = newContainerPort
	}

	// Regenerate start_command with the new RAM value
	newStartCommand := fmt.Sprintf("java -Xms%dM -Xmx%dM %s %s -jar server.jar nogui",
		req.RAM, req.RAM, defaultJvmFlags, strings.TrimSpace(srv.ExtraJvmFlags))
	newStartCommand = strings.Join(strings.Fields(newStartCommand), " ")

	// Persist the updated start_command in the DB
	h.state.Store.UpdateServerSetup(serverID, srv.GameImage, newStartCommand,
		srv.ActiveSubServer, srv.ExtraJvmFlags,
		srv.InstallerType, srv.MinecraftVersion, srv.BuildNumber)

	// Inform node to recreate container with new resources (and port if changed)
	if h.state.Queue != nil {
		node, err := h.state.Store.GetNodeByID(srv.NodeID)
		if err == nil {
			dockerPayload := map[string]interface{}{
				"ram":        req.RAM,
				"cpuLimit":   req.CPULimit,
				"cpusetCpus": effectiveCpuset(srv.CPUPinningMode, srv.Cpuset, node.CpusetCpus),
				"diskLimit":  req.DiskLimit,
				"image":      srv.GameImage,
				"command":    newStartCommand,
			}
			if portChanged {
				dockerPayload["hostPort"] = srv.HostPort
				dockerPayload["containerPort"] = srv.ContainerPort
			}
			payload := map[string]interface{}{
				"uuid":            srv.UUID,
				"activeSubServer": srv.ActiveSubServer,
				"docker":          dockerPayload,
			}
			h.state.Queue.SendCommand(context.Background(), node.Token, "update_resources", payload, nil)
		}
	}

	actorID, _ := r.Context().Value("userID").(string)
	LogServerAudit(h.state, r, serverID, ServerAuditEventResourcesChanged, actorID, "", map[string]interface{}{
		"ram":       req.RAM,
		"cpuLimit":  req.CPULimit,
		"diskLimit": req.DiskLimit,
	})

	h.state.Events.Publish(r.Context(), "servers.changed", nil)

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// SetServerAutoMove PATCH /api/servers/{id}/automove
// Flips the auto-move opt-in flag. Migration itself is handled by the
// rebalance worker — this endpoint only stores intent.
func (h *ServerHandler) SetServerAutoMove(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}
	if err := h.state.Store.SetServerAutoMove(serverID, req.Enabled); err != nil {
		sendJSONError(w, "Failed to update auto-move", 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// MoveServer (admin) queues a manual node-to-node migration of a server to a
// target node. Async: it only enqueues; the leader-elected Core runs the
// migration step machine and the panel polls the orchestration status key.
// The route is gateway-gated (migration is gateway-only) via RequireGatewayEnabled.
func (h *ServerHandler) MoveServer(w http.ResponseWriter, r *http.Request) {
	if h.state.Migration == nil {
		sendJSONError(w, "Migration orchestrator not available", 503)
		return
	}
	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", 400)
		return
	}
	isAdmin := r.Context().Value("isAdmin").(bool)
	if !isAdmin {
		sendJSONError(w, "Admin only", 403)
		return
	}
	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", 404)
		return
	}
	var req struct {
		TargetNodeID int `json:"targetNodeId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}
	target, err := h.state.Store.GetNodeByID(req.TargetNodeID)
	if err != nil {
		sendJSONError(w, "Target node not found", 404)
		return
	}
	username, _ := r.Context().Value("username").(string)
	h.queueMigration(w, r, srv, target, "manual", username)
}

// TransferServer (tenant) queues a node-to-node migration of a server the caller
// OWNS to a target node they may place on. BYON-only: outside BYON mode, moves
// stay admin-only (use MoveServer). The target picker is the tenant-scoped node
// list, so in practice this covers moving between the user's own nodes (and
// pulling a platform-hosted server onto their own hardware). Same orchestrator +
// status as the admin move; gateway-gated (migration is gateway-only).
func (h *ServerHandler) TransferServer(w http.ResponseWriter, r *http.Request) {
	if h.state.Migration == nil {
		sendJSONError(w, "Migration orchestrator not available", 503)
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
	// Authz: owner-or-admin on the server; BYON must be on for a non-admin tenant.
	username, _ := r.Context().Value("username").(string)
	if !IsAdmin(r) {
		if !byonActive(h.state, r) {
			sendJSONError(w, "Server transfer is not enabled", 403)
			return
		}
		if srv.OwnerName != username {
			sendJSONError(w, "Forbidden", 403)
			return
		}
	}
	var req struct {
		TargetNodeID int `json:"targetNodeId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}
	target, err := h.state.Store.GetNodeByID(req.TargetNodeID)
	if err != nil {
		sendJSONError(w, "Target node not found", 404)
		return
	}
	// Placement authz: the caller must be allowed to place on the target node
	// (their own BYON node, or a platform node in BYON mode). Admins always may.
	if !canPlaceOnNode(h.state, r, target) {
		sendJSONError(w, "You cannot place servers on the target node", 403)
		return
	}
	h.queueMigration(w, r, srv, target, "transfer", username)
}

// queueMigration runs the shared validation + enqueue for both the admin move
// and the tenant transfer: rejects a same-node target, requires the target be
// online, then enqueues onto the orchestrator (async) and returns 202. Callers
// own the access-control decision before calling this.
func (h *ServerHandler) queueMigration(w http.ResponseWriter, r *http.Request, srv *models.Server, target *models.Node, reason, requestedBy string) {
	if target.ID == srv.NodeID {
		sendJSONError(w, "Target node equals current node", 400)
		return
	}
	if target.Status != "online" {
		sendJSONError(w, "Target node is not online", 409)
		return
	}
	if err := h.state.Migration.EnqueueMigration(r.Context(), srv.ID, target.ID, reason, requestedBy); err != nil {
		sendJSONError(w, "Failed to queue migration", 500)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "migration queued"})
}

// byonNodeServerFallbackCap bounds servers-per-node when the node has not yet
// reported its CPU topology (so we can't scale by thread count). Generous enough
// not to block a legitimate first few servers, low enough to stop runaway abuse.
const byonNodeServerFallbackCap = 16

// byonNodeServerCapReached reports whether a tenant has hit the per-node server
// cap on one of their own BYON nodes, plus the resolved cap (for the message).
// Cap = factor x logical threads (setting byon.max_servers_per_core, default 2);
// when the topology is unknown it falls back to a fixed ceiling. Fail-open on a
// store error so a transient DB blip never blocks a legitimate create.
func (h *ServerHandler) byonNodeServerCapReached(ctx context.Context, node *models.Node) (bool, int) {
	factor := 2
	if v, _ := h.state.Store.GetSetting("byon.max_servers_per_core"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			factor = n
		}
	}

	cores := 0
	if h.state.CPUPinning != nil {
		if topo, _ := h.state.CPUPinning.GetNodeTopology(ctx, node.Token); topo != nil {
			cores = topo.LogicalCount
			if cores == 0 {
				cores = len(topo.Cores)
			}
		}
	}

	capN := factor * cores
	if capN <= 0 {
		capN = byonNodeServerFallbackCap // topology not reported yet
	}

	count, err := h.state.Store.CountServersByNode(node.ID)
	if err != nil {
		return false, capN // fail-open: never block on a store error
	}
	return count >= capN, capN
}

// GetMigrationStatus GET /api/servers/{id}/migration-status
// Returns the orchestrator-owned progress record the migration worker writes
// to dylaris:migration:<uuid>:orchestration, so the panel can poll it while a
// move is in flight. When the key is absent (no migration has ever run, or its
// TTL expired) we return {phase:"none"} so the caller has a stable terminal
// shape to stop polling on. Read-only; owner-or-admin like SetServerAutoMove.
func (h *ServerHandler) GetMigrationStatus(w http.ResponseWriter, r *http.Request) {
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
	if h.state.Redis == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": map[string]string{"phase": "none"}})
		return
	}
	key := fmt.Sprintf("dylaris:migration:%s:orchestration", srv.UUID)
	raw, err := h.state.Redis.Get(context.Background(), key).Result()
	if err == redis.Nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": map[string]string{"phase": "none"}})
		return
	}
	if err != nil {
		sendJSONError(w, "Failed to read migration status", 500)
		return
	}
	var status map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		sendJSONError(w, "Failed to parse migration status", 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": status})
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

	// If deleting the active sub-server, reset to pending_setup AND
	// flip desired_state to "stopped". Without the desired_state flip
	// the Node reconciler races against the delete: it sees the
	// container missing, reads desired_state="online", and recreates
	// the container from the saved config -- which still references
	// the just-deleted sub-server, so Docker auto-creates an empty
	// bind dir and the user's deleted slot resurrects empty. Setting
	// desired_state="stopped" tells the reconciler to leave it alone.
	if srv.ActiveSubServer == subServerName {
		h.state.Store.UpdateServerStatus(serverID, "pending_setup")
		h.state.Store.UpdateServerActiveSubServer(serverID, "")
		h.state.Store.UpdateServerDesiredState(serverID, "stopped")
	}

	h.state.Events.Publish(r.Context(), "servers.changed", nil)

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// DeleteServer: Completely delete a server
func (h *ServerHandler) DeleteServer(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	// Capability gate. Admins always pass; non-admins need the
	// can_delete_servers flag. This is in addition to the ownership check
	// that already exists further down — both gates must be open.
	userID, _ := r.Context().Value("userID").(string)
	perms := LoadEffectivePermissions(h.state, userID)
	if !perms.IsAdmin && !perms.CanDeleteServers {
		sendJSONError(w, "Deleting servers requires elevated permissions — contact an administrator", 403)
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

		// Proxy servers own a dedicated overlay network — drop it once no
		// game-servers are attached anymore (linked servers have been
		// detached via their own delete paths already, or we accept the
		// remove-error and clean up later).
		if srv.ServerType == "proxy" {
			h.state.Queue.SendProxyNetworkCommand(context.Background(), node.Token, "proxy_network_destroy", srv.UUID, "")
		}
	}

	// Gateway routes are stored independently from the DB row (in Redis +
	// Hub's own DB), so they don't cascade with DeleteServer. Two layers
	// of cleanup, because both have failure modes:
	//
	//   1. Push delete_route to the Hub queue (Gateway.DeleteRoute). This
	//      is the source of truth -- Hub soft-deletes the row in its DB
	//      so SyncData doesn't re-publish the route to Redis on its next
	//      tick. If the Hub queue worker is wedged this step is a no-op
	//      and the route would otherwise come back from Hub's DB.
	//   2. Delete the cache keys directly in Redis. Immediate effect on
	//      traffic routing while we wait for Hub to catch up. Belt-and-
	//      suspenders -- harmless if Hub is healthy (the keys would
	//      vanish from the next SyncData anyway), and the only thing
	//      that works if it isn't.
	//
	// The previous version only did step 1, which is why the route kept
	// reappearing in Redis: when Hub's queue lag bumped into Hub's
	// SyncData cadence, the route was re-published before the delete
	// landed.
	//
	// Delete the authoritative server row FIRST. If it fails we return having
	// touched nothing, so Core and Hub stay consistent — the route cleanup
	// below only runs once the server is actually gone.
	if err := h.state.Store.DeleteServer(serverID); err != nil {
		sendJSONError(w, "Delete failed", 500)
		return
	}

	if h.state.Redis != nil {
		ctx := context.Background()
		routes := services.GetRoutesFromRedis(ctx, h.state.Redis)
		matched := 0
		targetIP := fmt.Sprintf("mc_%s", srv.UUID)
		for _, rt := range routes {
			// Match on either field. Older routes (pre `server_uuid`
			// column being persisted) only have target_ip = "mc_<uuid>";
			// newer ones have both. Match-or means the cleanup catches
			// every shape we've ever written.
			if rt.ServerUUID != srv.UUID && rt.TargetIP != targetIP {
				continue
			}
			matched++
			// Tell Hub to hard-delete the row (source of truth).
			if h.state.Gateway != nil {
				if delErr := h.state.Gateway.DeleteRoute(rt.Domain); delErr != nil {
					log.Printf("DeleteServer: gateway DeleteRoute %s for %s failed: %v", rt.Domain, srv.UUID, delErr)
				}
			}
			// Drop the Redis cache entry immediately so the route stops
			// resolving while Hub processes the queue message.
			pipe := h.state.Redis.Pipeline()
			pipe.Del(ctx, "route:"+rt.Domain)
			pipe.SRem(ctx, "sys:index:routes", rt.Domain)
			if _, err := pipe.Exec(ctx); err != nil {
				log.Printf("DeleteServer: redis cache drop for route %s failed: %v", rt.Domain, err)
			}
		}
		log.Printf("DeleteServer: server %s — cleaned up %d route(s)", srv.UUID, matched)
	}

	h.state.Events.Publish(r.Context(), "servers.changed", nil)

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
