package handlers

import (
	"fmt"
	"net/http"
	"time"

	"dylaris-core/services"
)

// SystemEventsHandler exposes the SSE side of the system events channel.
// One handler per Core process — the panel opens one stream per session and
// receives broadcasts about regions/modules/features/maintenance/server-list
// changes so it can refresh cached state without polling.
type SystemEventsHandler struct {
	state *AppState
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
			fmt.Fprintf(w, "data: %s\n\n", msg.Payload)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
