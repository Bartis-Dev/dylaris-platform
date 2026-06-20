package handlers

import (
	"dylaris-core/models"
	"dylaris-core/store"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
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
func checkServerAccess(st store.Store, srv *models.Server, username string, isAdmin bool, userID string, requiredPerm string) bool {
	if isAdmin || srv.OwnerName == username {
		return true
	}
	if userID == "" {
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
	UUID       string   `json:"uuid"`
	Name       string   `json:"name"`
	NodeID     string   `json:"nodeId"`
	Region     string   `json:"region"` // optional scheduler filter (e.g. "eu-central")
	Tags       []string `json:"tags"`   // AND-filter when scheduler picks a node
	Tag        string   `json:"tag"`    // deprecated, single-tag legacy field; folded into Tags
	OwnerID    string   `json:"ownerId"`
	IsFixed    *bool    `json:"isFixed"`
	ServerType string   `json:"serverType"`
	AutoMove   bool     `json:"autoMove"` // opt-in to load-balancing migrations
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
		Type      string `json:"type"`      // "paper","vanilla","fabric","forge","neoforge","library","upload","upload-zip","modpack"
		Version   string `json:"version"`   // build/version identifier (Paper build, Forge build, etc.)
		McVersion string `json:"mcVersion"` // major MC version (e.g. "1.21.4")
		Loader    string `json:"loader"`    // Fabric loader / Forge build / NeoForge version (optional)
		URL       string `json:"url"`       // for import via URL OR .mrpack url for modpack
		Path      string `json:"path"`      // for library selection
		Structure string `json:"structure"` // "direct" or "subfolder" (for upload-zip)
		// Modpack reference; sub-server boots from a Modrinth
		// modpack and we remember which project+version so the panel can
		// later check Modrinth for newer versions and offer one-click update.
		ModrinthProjectID   string `json:"modrinthProjectId,omitempty"`
		ModrinthVersionID   string `json:"modrinthVersionId,omitempty"`
		ModrinthProjectSlug string `json:"modrinthProjectSlug,omitempty"`
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
	userID, _ := r.Context().Value("userID").(string)

	servers, err := h.state.Store.ListServersForUser(userID, isAdmin)
	if err != nil {
		sendJSONError(w, "Database error", 500)
		return
	}

	if servers == nil {
		servers = []models.Server{}
	}

	// Region filter. Admins + all-regions users pass through;
	// explicit-regions users only see servers in their allowed set.
	perms := LoadEffectivePermissions(h.state, userID)
	servers = FilterServersByRegion(servers, perms)

	// Demo servers. Mark any server already in the list that is on the demo list
	// (so its owner/admin sees the demo status in the toggle), and — for the
	// designated read-only demo account — append the showcase server(s) so the
	// public demo session has something to look at. Read access is enforced
	// server-side; the role "demo" + read-only permission set here only tells the
	// panel to render the appended ones read-only.
	// Whole demo surface only exists on the hosted (store-enabled) build; a
	// self-host build never reads a stale demo_server_uuids setting.
	if demoUUIDs := loadDemoServerUUIDs(h.state.Store); h.state.StoreEnabled && len(demoUUIDs) > 0 {
		demoSet := make(map[string]bool, len(demoUUIDs))
		for _, u := range demoUUIDs {
			demoSet[u] = true
		}
		for i := range servers {
			if demoSet[servers[i].UUID] {
				servers[i].IsDemo = true
			}
		}
		if isDemoAccount(h.state, userID) {
			for _, uuid := range demoUUIDs {
				ds, derr := h.state.Store.GetServerByUUID(uuid)
				if derr != nil || ds == nil {
					continue
				}
				ds.IsDemo = true
				ds.Role = "demo"
				ds.Permissions = &models.TabPermissions{Console: true, Files: true}
				servers = append(servers, *ds)
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"servers": servers,
	})
}
