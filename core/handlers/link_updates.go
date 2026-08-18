package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"dylaris-core/services"
)

// Link sidecar update policy.
//
// A node's Link is replaced by the node itself; Core only carries the operator's
// preference and the manual trigger. The policy deliberately does NOT reach
// external/BYON nodes: node/link_update.go forces "auto" there regardless,
// because there is no operator on that machine to react to a notification and a
// stale Link fails subtly rather than loudly. Changing this setting therefore
// affects datacenter nodes only, and the panel says so.

const (
	linkUpdatePolicyKey   = "link_update_policy"
	linkUpdateIntervalKey = "link_update_interval_min"

	defaultLinkUpdatePolicy   = "auto_idle"
	defaultLinkUpdateInterval = 15

	// A check below a minute is registry traffic for nothing; a day is long
	// enough that "it will sort itself out" stops being true.
	minLinkUpdateInterval = 1
	maxLinkUpdateInterval = 1440
)

func validLinkUpdatePolicy(p string) bool {
	return p == "notify" || p == "auto_idle" || p == "auto"
}

type linkUpdateSettings struct {
	Policy          string `json:"policy"`
	IntervalMinutes int    `json:"intervalMinutes"`
}

// GetLinkUpdateSettings returns the current Link update policy and check interval.
func (h *SettingsHandler) GetLinkUpdateSettings(w http.ResponseWriter, r *http.Request) {
	out := linkUpdateSettings{
		Policy:          defaultLinkUpdatePolicy,
		IntervalMinutes: defaultLinkUpdateInterval,
	}
	if v, err := h.state.Store.GetSetting(linkUpdatePolicyKey); err == nil && validLinkUpdatePolicy(v) {
		out.Policy = v
	}
	if v, err := h.state.Store.GetSetting(linkUpdateIntervalKey); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= minLinkUpdateInterval && n <= maxLinkUpdateInterval {
			out.IntervalMinutes = n
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// UpdateLinkUpdateSettings persists the policy + interval and mirrors both into
// Redis, which is where nodes read them (node/main.go, loadModesFromRedis).
// Persisting without mirroring would leave the panel showing a setting no node
// ever acts on.
func (h *SettingsHandler) UpdateLinkUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req linkUpdateSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if !validLinkUpdatePolicy(req.Policy) {
		sendJSONError(w, "policy must be one of: notify, auto_idle, auto", http.StatusBadRequest)
		return
	}
	if req.IntervalMinutes < minLinkUpdateInterval || req.IntervalMinutes > maxLinkUpdateInterval {
		sendJSONError(w, "intervalMinutes must be between 1 and 1440", http.StatusBadRequest)
		return
	}

	if err := h.state.Store.SetSetting(linkUpdatePolicyKey, req.Policy); err != nil {
		sendJSONError(w, "Failed to save the Link update policy", http.StatusInternalServerError)
		return
	}
	interval := strconv.Itoa(req.IntervalMinutes)
	if err := h.state.Store.SetSetting(linkUpdateIntervalKey, interval); err != nil {
		sendJSONError(w, "Failed to save the Link update interval", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	h.state.Redis.Set(ctx, "dylaris:link_update_policy", req.Policy, 0)
	h.state.Redis.Set(ctx, "dylaris:link_update_interval_min", interval, 0)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(req)
}

// TriggerLinkUpdate queues the node-level "link_update" command. With no node id
// it targets every node that manages its own Link and has drift pending.
//
// Deliberately no dry-run and no confirmation flag: replacing the Link drops that
// node's player sessions for 10-30 seconds, and the warning belongs in the panel
// next to the button, not as a second round-trip.
func (h *NodeHandler) TriggerLinkUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID string `json:"nodeId"`
	}
	// An empty body means "all"; a decode failure on a present body does not.
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	ctx := r.Context()
	states := h.linkStates(ctx)
	nodes, err := h.state.Store.ListNodes()
	if err != nil {
		sendJSONError(w, "Failed to load nodes", http.StatusInternalServerError)
		return
	}

	queued := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if req.NodeID != "" && n.Token != req.NodeID {
			continue
		}
		st, ok := states[n.Token]
		// Never queue for a node whose Link an operator deploys, or one that is
		// not reporting at all: the command would be consumed and do nothing,
		// which reads as a broken button.
		if !ok || !st.Managed {
			continue
		}
		// Targeting everything applies only where an update is actually pending;
		// naming a node explicitly is an operator overriding that judgement.
		if req.NodeID == "" && !st.UpdateAvailable {
			continue
		}
		if err := h.state.Queue.SendCommand(ctx, n.Token, "link_update", nil, nil); err != nil {
			log.Printf("link-update: failed to queue for node %s: %v", n.Token, err)
			continue
		}
		queued = append(queued, n.Token)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"queued": queued, "count": len(queued)})
}

// GetLinkUpdateStates returns the per-node Link image status published by the
// discovery sweep. A node missing from the map is not reporting; the panel shows
// that as unknown rather than as up to date.
func (h *NodeHandler) GetLinkUpdateStates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.linkStates(r.Context()))
}

func (h *NodeHandler) linkStates(ctx context.Context) map[string]services.NodeLinkState {
	out := map[string]services.NodeLinkState{}
	if h.state.Redis == nil {
		return out
	}
	raw, err := h.state.Redis.Get(ctx, services.NodeLinkStateKey).Bytes()
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}
