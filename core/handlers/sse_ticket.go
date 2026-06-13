package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

// sseTicketTTL is the lifetime of an SSE auth ticket. AuthMiddleware also
// uses this value when refreshing the ticket on each accepted SSE request so
// long-lived streams (and native EventSource reconnects) keep the same ticket
// valid as a sliding window.
const sseTicketTTL = 5 * time.Minute

// MintSSETicket issues a short-lived, single-purpose ticket that an
// EventSource can carry in the URL (?ticket=) instead of the long-lived
// session JWT. EventSource cannot set an Authorization header, so the only
// way to authenticate an SSE stream is via the URL — and putting the session
// JWT there leaks it into access logs / Referer. The ticket is random,
// expires in 5 minutes, and only maps back to a username inside Redis.
//
// Registered under AuthMiddleware, so the caller authenticates with the
// normal Bearer header to obtain the ticket.
func (h *AuthHandler) MintSSETicket(w http.ResponseWriter, r *http.Request) {
	if h.state.Redis == nil {
		sendJSONError(w, "Ticket store unavailable", http.StatusServiceUnavailable)
		return
	}

	username, _ := r.Context().Value("username").(string)
	if username == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		sendJSONError(w, "Failed to generate ticket", http.StatusInternalServerError)
		return
	}
	ticket := hex.EncodeToString(buf)

	if err := h.state.Redis.Set(r.Context(), "sse:ticket:"+ticket, username, sseTicketTTL).Err(); err != nil {
		sendJSONError(w, "Failed to store ticket", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ticket": ticket})
}
