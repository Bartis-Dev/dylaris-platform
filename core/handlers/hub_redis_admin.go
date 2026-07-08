package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"dylaris-core/services/redisacl"

	"github.com/redis/go-redis/v9"
)

const (
	// hubAdminUser is the NAMED Redis ACL user this feature creates. The button
	// never touches the target's default user, so a shared platform Redis keeps
	// working in "same" mode.
	hubAdminUser = "gw-hub-admin"
	// hubRedisStatusKey holds the small NON-secret status JSON in the settings
	// table. No password, no external admin password, ever.
	hubRedisStatusKey = "gw_hub_redis_admin_status"
)

// HubRedisAdminHandler provisions / tests / rolls the gw-hub-admin Redis ACL
// user on a target Redis. All endpoints are admin-only.
type HubRedisAdminHandler struct {
	state *AppState
}

func NewHubRedisAdminHandler(state *AppState) *HubRedisAdminHandler {
	return &HubRedisAdminHandler{state: state}
}

// hubRedisStatus is the persisted, non-secret record of the last provision.
type hubRedisStatus struct {
	Mode          string `json:"mode"`
	Addr          string `json:"addr"`
	DB            int    `json:"db"`
	AdminUser     string `json:"adminUser,omitempty"` // external mode only
	ProvisionedAt string `json:"provisionedAt"`
	LastRolledAt  string `json:"lastRolledAt,omitempty"`
}

// externalTarget is the wire shape for an external Redis target.
type externalTarget struct {
	Addr     string `json:"addr"`
	DB       int    `json:"db"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// GetStatus GET /api/settings/gateway/hub-redis-admin - non-secret status only.
func (h *HubRedisAdminHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	raw, err := h.state.Store.GetSetting(hubRedisStatusKey)
	if err != nil || raw == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     true,
			"provisioned": false,
		})
		return
	}
	var st hubRedisStatus
	if uerr := json.Unmarshal([]byte(raw), &st); uerr != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     true,
			"provisioned": false,
		})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"provisioned":   true,
		"mode":          st.Mode,
		"addr":          st.Addr,
		"db":            st.DB,
		"adminUser":     st.AdminUser,
		"provisionedAt": st.ProvisionedAt,
		"lastRolledAt":  st.LastRolledAt,
	})
}

// TestConnection POST /api/settings/gateway/hub-redis-admin/test-connection -
// reachability + auth against an external target (Ping + ACL WHOAMI). Does NOT
// pre-check the SETUSER right; that is proven at provision time.
func (h *HubRedisAdminHandler) TestConnection(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var req externalTarget
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if !validAddr(req.Addr) {
		sendJSONError(w, "Address must be host:port", http.StatusBadRequest)
		return
	}
	if req.DB < 0 || req.DB > 15 {
		sendJSONError(w, "DB must be between 0 and 15", http.StatusBadRequest)
		return
	}
	if req.Username == "" {
		sendJSONError(w, "Username is required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{
		Addr: req.Addr, Username: req.Username, Password: req.Password, DB: req.DB,
	})
	defer client.Close()
	if err := client.Ping(ctx).Err(); err != nil {
		sendJSONError(w, "Connection failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	whoami, err := client.Do(ctx, "ACL", "WHOAMI").Text()
	if err != nil {
		sendJSONError(w, "Auth check failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"ok":      true,
		"whoami":  whoami,
	})
}

// Provision POST /api/settings/gateway/hub-redis-admin - create gw-hub-admin on
// the chosen target and return the generated password ONCE.
func (h *HubRedisAdminHandler) Provision(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var req struct {
		Mode     string          `json:"mode"`
		DB       int             `json:"db"`
		External *externalTarget `json:"external"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.DB < 0 || req.DB > 15 {
		sendJSONError(w, "DB must be between 0 and 15", http.StatusBadRequest)
		return
	}
	pw, err := genHubPassword()
	if err != nil {
		sendJSONError(w, "Failed to generate password", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)

	switch req.Mode {
	case "manual":
		h.persist(hubRedisStatus{Mode: "manual", Addr: "", DB: req.DB, ProvisionedAt: now})
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  true,
			"username": hubAdminUser,
			"password": pw,
			"hubEnv": map[string]interface{}{
				"REDIS_USER": hubAdminUser, "REDIS_PASS": pw, "REDIS_DB": req.DB,
			},
			"aclCommand": manualACLCommand(pw),
		})
		return

	case "same":
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if perr := h.provisionOnClient(ctx, h.state.Redis, pw); perr != nil {
			sendJSONError(w, "Provision failed: "+perr.Error(), http.StatusBadGateway)
			return
		}
		addr := h.state.Redis.Options().Addr
		h.persist(hubRedisStatus{Mode: "same", Addr: addr, DB: req.DB, ProvisionedAt: now})
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  true,
			"username": hubAdminUser,
			"password": pw,
			"hubEnv": map[string]interface{}{
				"REDIS_ADDR": addr, "REDIS_USER": hubAdminUser, "REDIS_PASS": pw, "REDIS_DB": req.DB,
			},
		})
		return

	case "external":
		if req.External == nil {
			sendJSONError(w, "External target is required", http.StatusBadRequest)
			return
		}
		if !validAddr(req.External.Addr) {
			sendJSONError(w, "Address must be host:port", http.StatusBadRequest)
			return
		}
		if req.External.DB < 0 || req.External.DB > 15 {
			sendJSONError(w, "DB must be between 0 and 15", http.StatusBadRequest)
			return
		}
		if req.External.Username == "" {
			sendJSONError(w, "Username is required", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		client := redis.NewClient(&redis.Options{
			Addr: req.External.Addr, Username: req.External.Username,
			Password: req.External.Password, DB: req.External.DB,
		})
		defer client.Close()
		if perr := client.Ping(ctx).Err(); perr != nil {
			sendJSONError(w, "Connection failed: "+perr.Error(), http.StatusBadGateway)
			return
		}
		if perr := h.provisionOnClient(ctx, client, pw); perr != nil {
			sendJSONError(w, "Provision failed: "+perr.Error(), http.StatusBadGateway)
			return
		}
		h.persist(hubRedisStatus{
			Mode: "external", Addr: req.External.Addr, DB: req.External.DB,
			AdminUser: req.External.Username, ProvisionedAt: now,
		})
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  true,
			"username": hubAdminUser,
			"password": pw,
			"hubEnv": map[string]interface{}{
				"REDIS_ADDR": req.External.Addr, "REDIS_USER": hubAdminUser,
				"REDIS_PASS": pw, "REDIS_DB": req.External.DB,
			},
		})
		return

	default:
		sendJSONError(w, "Invalid mode", http.StatusBadRequest)
		return
	}
}

// Roll POST /api/settings/gateway/hub-redis-admin/roll - re-mint the password on
// the recorded target. External re-prompts the admin password (never stored).
func (h *HubRedisAdminHandler) Roll(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	raw, gerr := h.state.Store.GetSetting(hubRedisStatusKey)
	if gerr != nil || raw == "" {
		sendJSONError(w, "Not provisioned yet", http.StatusBadRequest)
		return
	}
	var st hubRedisStatus
	if uerr := json.Unmarshal([]byte(raw), &st); uerr != nil {
		sendJSONError(w, "Stored status is corrupt", http.StatusInternalServerError)
		return
	}
	var req struct {
		External *struct {
			Password string `json:"password"`
		} `json:"external"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	pw, perr := genHubPassword()
	if perr != nil {
		sendJSONError(w, "Failed to generate password", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)

	switch st.Mode {
	case "manual":
		st.LastRolledAt = now
		h.persist(st)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  true,
			"username": hubAdminUser,
			"password": pw,
			"hubEnv": map[string]interface{}{
				"REDIS_USER": hubAdminUser, "REDIS_PASS": pw, "REDIS_DB": st.DB,
			},
			"aclCommand": manualACLCommand(pw),
		})
		return

	case "same":
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := h.provisionOnClient(ctx, h.state.Redis, pw); err != nil {
			sendJSONError(w, "Roll failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		st.LastRolledAt = now
		h.persist(st)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  true,
			"username": hubAdminUser,
			"password": pw,
			"hubEnv": map[string]interface{}{
				"REDIS_ADDR": st.Addr, "REDIS_USER": hubAdminUser, "REDIS_PASS": pw, "REDIS_DB": st.DB,
			},
		})
		return

	case "external":
		if req.External == nil || req.External.Password == "" {
			sendJSONError(w, "External admin password is required to roll", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		client := redis.NewClient(&redis.Options{
			Addr: st.Addr, Username: st.AdminUser, Password: req.External.Password, DB: st.DB,
		})
		defer client.Close()
		if err := client.Ping(ctx).Err(); err != nil {
			sendJSONError(w, "Connection failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		if err := h.provisionOnClient(ctx, client, pw); err != nil {
			sendJSONError(w, "Roll failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		st.LastRolledAt = now
		h.persist(st)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  true,
			"username": hubAdminUser,
			"password": pw,
			"hubEnv": map[string]interface{}{
				"REDIS_ADDR": st.Addr, "REDIS_USER": hubAdminUser, "REDIS_PASS": pw, "REDIS_DB": st.DB,
			},
		})
		return

	default:
		sendJSONError(w, "Unknown stored mode", http.StatusInternalServerError)
		return
	}
}

// persist writes the non-secret status JSON. Best-effort: a write failure is
// logged, not surfaced (the ACL user is already created).
func (h *HubRedisAdminHandler) persist(st hubRedisStatus) {
	b, err := json.Marshal(st)
	if err != nil {
		log.Printf("hub-redis-admin: marshal status: %v", err)
		return
	}
	if serr := h.state.Store.SetSetting(hubRedisStatusKey, string(b)); serr != nil {
		log.Printf("hub-redis-admin: persist status: %v", serr)
	}
}

// provisionOnClient runs ACL SETUSER gw-hub-admin (full rights) then ACL SAVE on
// the given client. SETUSER failure (e.g. NOPERM) fails the request; SAVE is
// best-effort (only persists when an aclfile is configured), mirroring the node
// provisioner.
func (h *HubRedisAdminHandler) provisionOnClient(ctx context.Context, client *redis.Client, pw string) error {
	args := redisacl.SetUserArgs(hubAdminUser, fullAdminRules(pw))
	if err := client.Do(ctx, args...).Err(); err != nil {
		return err
	}
	if err := client.Do(ctx, "ACL", "SAVE").Err(); err != nil {
		log.Printf("hub-redis-admin: ACL SAVE failed (aclfile configured?): %v", err)
	}
	return nil
}

// fullAdminRules is the full-rights ACL rule slice (owner's "alle Rechte").
func fullAdminRules(pw string) []interface{} {
	return []interface{}{"on", ">" + pw, "~*", "&*", "+@all"}
}

// manualACLCommand is the ready-to-paste command for "show command only" mode.
func manualACLCommand(pw string) string {
	return "ACL SETUSER " + hubAdminUser + " on >" + pw + " ~* &* +@all\nACL SAVE"
}

// genHubPassword returns a fresh URL-safe password (32 random bytes,
// base64url, no padding). Safe both as a go-redis arg and inside the pasted
// redis-cli command.
func genHubPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// validAddr checks host:port shape with a numeric 1..65535 port.
func validAddr(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return false
	}
	p, perr := strconv.Atoi(port)
	return perr == nil && p > 0 && p <= 65535
}
