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
	"strings"
	"time"

	"dylaris-core/services/redisacl"

	"github.com/redis/go-redis/v9"
)

const (
	// hubAdminUser is the NAMED Redis ACL user this feature creates. The button
	// never touches Core's default user, so the shared platform Redis keeps
	// working while gw-hub-admin is (re)provisioned.
	hubAdminUser = "gw-hub-admin"
	// hubRedisStatusKey holds the small NON-secret status JSON in the settings
	// table. No password, ever.
	hubRedisStatusKey = "gw_hub_redis_admin_status"
)

// HubRedisAdminHandler provisions / rolls the gw-hub-admin Redis ACL user on
// Core's own Redis (the one instance shared with the Hub). All endpoints are
// admin-only.
type HubRedisAdminHandler struct {
	state *AppState
}

func NewHubRedisAdminHandler(state *AppState) *HubRedisAdminHandler {
	return &HubRedisAdminHandler{state: state}
}

// hubRedisStatus is the persisted, non-secret record of the last provision. Addr
// records only how the Hub reaches the ONE shared Redis; it is never dialled by Core.
type hubRedisStatus struct {
	Mode          string `json:"mode"`
	Addr          string `json:"addr"`
	DB            int    `json:"db"`
	ProvisionedAt string `json:"provisionedAt"`
	LastRolledAt  string `json:"lastRolledAt,omitempty"`
}

// GetStatus GET /api/settings/gateway/hub-redis-admin - non-secret status only.
// PANEL settings.read (RequireCap at the route).
func (h *HubRedisAdminHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
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
		"provisionedAt": st.ProvisionedAt,
		"lastRolledAt":  st.LastRolledAt,
	})
}

// Provision POST /api/settings/gateway/hub-redis-admin - create gw-hub-admin on
// Core's own Redis (the ONE shared instance) and return the generated password
// ONCE, or in manual mode return the ready-to-paste command. hubAddr only records
// how the Hub reaches that instance; Core never dials it. PANEL settings.write
// (RequireCap at the route).
func (h *HubRedisAdminHandler) Provision(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode    string `json:"mode"`
		DB      int    `json:"db"`
		HubAddr string `json:"hubAddr"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.DB < 0 || req.DB > 15 {
		sendJSONError(w, "DB must be between 0 and 15", http.StatusBadRequest)
		return
	}
	hubAddr := strings.TrimSpace(req.HubAddr)
	if hubAddr != "" && !validAddr(hubAddr) {
		sendJSONError(w, "Address must be host:port", http.StatusBadRequest)
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
		h.persist(hubRedisStatus{Mode: "manual", Addr: hubAddr, DB: req.DB, ProvisionedAt: now})
		hubEnv := map[string]interface{}{
			"REDIS_USER": hubAdminUser, "REDIS_PASS": pw, "REDIS_DB": req.DB,
		}
		if hubAddr != "" {
			hubEnv["REDIS_ADDR"] = hubAddr
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    true,
			"username":   hubAdminUser,
			"password":   pw,
			"hubEnv":     hubEnv,
			"aclCommand": manualACLCommand(pw),
		})
		return

	case "auto":
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if perr := h.provisionOnClient(ctx, h.state.Redis, pw); perr != nil {
			sendJSONError(w, "Provision failed: "+perr.Error(), http.StatusBadGateway)
			return
		}
		// Default the recorded address to Core's own view when the admin did not
		// override it. This is only the Hub's REDIS_ADDR; Core provisions on its
		// own client regardless.
		if hubAddr == "" {
			hubAddr = h.state.Redis.Options().Addr
		}
		h.persist(hubRedisStatus{Mode: "auto", Addr: hubAddr, DB: req.DB, ProvisionedAt: now})
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  true,
			"username": hubAdminUser,
			"password": pw,
			"hubEnv": map[string]interface{}{
				"REDIS_ADDR": hubAddr, "REDIS_USER": hubAdminUser, "REDIS_PASS": pw, "REDIS_DB": req.DB,
			},
		})
		return

	default:
		sendJSONError(w, "Invalid mode", http.StatusBadRequest)
		return
	}
}

// Roll POST /api/settings/gateway/hub-redis-admin/roll - re-mint the password on
// the recorded target. No request body: auto rolls on Core's Redis, manual just
// re-shows the command. PANEL settings.write (RequireCap at the route).
func (h *HubRedisAdminHandler) Roll(w http.ResponseWriter, r *http.Request) {
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
		hubEnv := map[string]interface{}{
			"REDIS_USER": hubAdminUser, "REDIS_PASS": pw, "REDIS_DB": st.DB,
		}
		if st.Addr != "" {
			hubEnv["REDIS_ADDR"] = st.Addr
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    true,
			"username":   hubAdminUser,
			"password":   pw,
			"hubEnv":     hubEnv,
			"aclCommand": manualACLCommand(pw),
		})
		return

	case "auto":
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
