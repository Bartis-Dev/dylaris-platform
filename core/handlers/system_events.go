package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"dylaris-core/authz"
	"dylaris-core/services"
)

// SystemEventsHandler exposes the SSE side of the system events channel.
// One handler per Core process — the panel opens one stream per session and
// receives broadcasts about regions/modules/features/maintenance/server-list
// changes so it can refresh cached state without polling.
type SystemEventsHandler struct {
	state *AppState
}

// mayReceive decides whether one broadcast frame goes to THIS subscriber.
//
// The channel is a single fleet-wide fan-out, so everything published on it
// reached every authenticated session. Most of that is harmless by design -
// the events are cache-invalidation signals whose payload is usually empty, and
// the panel re-fetches through the normal authorized routes. But a few name a
// serverId, and those were being delivered to people with no access to that
// server: measured on a live instance, a non-admin who owns one server received
// server_tabs.changed for a server belonging to someone else. No content and no
// credentials, but other tenants' identifiers and the timing of their activity.
//
// Fails OPEN, which is the opposite of the rule everywhere else in this file's
// neighbourhood and is deliberate. This is not an access decision - the data
// behind every one of these events is fetched over a route that authorizes it
// properly. Dropping a frame we could not classify would silently stop a panel
// from refreshing; forwarding one leaks an integer. So only a resolution that
// actually says "no" drops the frame.
func (h *SystemEventsHandler) mayReceive(r *http.Request, payload string) bool {
	serverID := eventServerID(payload)
	if serverID == 0 {
		return true // no server named: a platform-wide signal, or an empty one
	}
	if h.state == nil || h.state.Authz == nil {
		return true
	}
	userID, _ := r.Context().Value("userID").(string)
	username, _ := r.Context().Value("username").(string)
	isAdmin, _ := r.Context().Value("isAdmin").(bool)
	res, err := h.state.Authz.Resolve(authz.Identity{UserID: userID, Username: username, IsAdmin: isAdmin}, serverID)
	if err != nil {
		return true
	}
	// overview.read is "may this account see that this server exists at all",
	// which is exactly what a bare id discloses. Every invite carries it.
	return res.HasCap("overview.read")
}

// eventServerID pulls the serverId out of an event payload, or 0 when it names
// none. JSON numbers decode as float64; anything else is treated as absent
// rather than guessed at.
func eventServerID(payload string) int {
	var ev struct {
		Payload struct {
			ServerID *float64 `json:"serverId"`
		} `json:"payload"`
	}
	if json.Unmarshal([]byte(payload), &ev) != nil || ev.Payload.ServerID == nil {
		return 0
	}
	return int(*ev.Payload.ServerID)
}

func NewSystemEventsHandler(state *AppState) *SystemEventsHandler {
	return &SystemEventsHandler{state: state}
}

// StreamEvents GET /api/system/events
//
// SSE stream subscribed to Redis Pub/Sub channel `dylaris:system:events`.
// Sends a `hello` event immediately so the client can flip into "connected"
// state without waiting for the first real event. Keepalive comment frames
// every 15s keep the connection alive across proxies that idle-close at 60s.
func (h *SystemEventsHandler) StreamEvents(w http.ResponseWriter, r *http.Request) {
	if h.state.Redis == nil {
		sendJSONError(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		sendJSONError(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	fmt.Fprintf(w, "data: {\"type\":\"hello\"}\n\n")
	flusher.Flush()

	pubsub := h.state.Redis.Subscribe(r.Context(), services.SystemEventsChannel)
	defer pubsub.Close()

	ch := pubsub.Channel()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if !h.mayReceive(r, msg.Payload) {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", msg.Payload)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
