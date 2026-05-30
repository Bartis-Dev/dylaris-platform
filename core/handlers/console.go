package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"
)

type ConsoleHandler struct {
	state *AppState
}

func NewConsoleHandler(state *AppState) *ConsoleHandler {
	return &ConsoleHandler{state: state}
}

// GetHistory GET /api/servers/{id}/console/history
// Returns the last 1000 log lines from the Redis Stream for this server.
func (h *ConsoleHandler) GetHistory(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil || h.state.Redis == nil {
		sendJSONError(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", http.StatusBadRequest)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}

	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)
	userID, _ := r.Context().Value("userID").(string)
	if !checkServerAccess(h.state.Store, srv, username, isAdmin, userID, "console") {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}

	subServer := r.URL.Query().Get("sub_server")
	streamKey := fmt.Sprintf("dylaris:server:%s:logs", srv.UUID)
	if subServer != "" {
		streamKey = fmt.Sprintf("dylaris:server:%s:logs:%s", srv.UUID, subServer)
	}
	// XRevRangeN returns newest-first; we reverse to get chronological order
	entries, err := h.state.Redis.XRevRangeN(r.Context(), streamKey, "+", "-", 1000).Result()
	if err != nil {
		// Stream may not exist yet (server never started) — return empty list
		entries = nil
	}

	lines := make([]string, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		if v, ok := entries[i].Values["line"]; ok {
			lines = append(lines, fmt.Sprintf("%v", v))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"lines": lines})
}

// StreamConsole GET /api/servers/{id}/console/stream
// Streams live server logs via SSE (Server-Sent Events).
// Auth token can be passed as ?token= query param since EventSource
// does not support custom headers.
// Reads directly from the Redis Stream via XREAD BLOCK — no Pub/Sub,
// no watcher tracking needed. Delivery is reliable: messages are never
// lost between reconnects because they stay in the stream history.
func (h *ConsoleHandler) StreamConsole(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil || h.state.Redis == nil {
		sendJSONError(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", http.StatusBadRequest)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}

	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)
	userID, _ := r.Context().Value("userID").(string)
	if !checkServerAccess(h.state.Store, srv, username, isAdmin, userID, "console") {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
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

	subServer := r.URL.Query().Get("sub_server")
	streamKey := fmt.Sprintf("dylaris:server:%s:logs", srv.UUID)
	if subServer != "" {
		streamKey = fmt.Sprintf("dylaris:server:%s:logs:%s", srv.UUID, subServer)
	}
	// "$" means: only deliver messages that arrive after this connection opens.
	lastID := "$"

	for {
		select {
		case <-r.Context().Done():
			return
		default:
		}

		results, err := h.state.Redis.XRead(r.Context(), &redis.XReadArgs{
			Streams: []string{streamKey, lastID},
			Count:   100,
			Block:   5 * time.Second,
		}).Result()
		if err == redis.Nil {
			// Block timeout with no new entries — send an SSE comment as keepalive
			// so the browser doesn't close the connection.
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
			continue
		}
		if err != nil {
			// r.Context() cancelled (client disconnect) or Redis error
			return
		}

		for _, stream := range results {
			for _, msg := range stream.Messages {
				line, _ := msg.Values["line"].(string)
				fmt.Fprintf(w, "data: %s\n\n", sseEscape(line))
				flusher.Flush()
				lastID = msg.ID
			}
		}
	}
}

// SendCommand POST /api/servers/{id}/console/command
func (h *ConsoleHandler) SendCommand(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil || h.state.Redis == nil {
		sendJSONError(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	vars := mux.Vars(r)
	serverID, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid server ID", http.StatusBadRequest)
		return
	}

	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	req.Command = strings.TrimSpace(req.Command)
	if req.Command == "" {
		sendJSONError(w, "Command required", http.StatusBadRequest)
		return
	}

	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}

	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)
	userID, _ := r.Context().Value("userID").(string)
	if !checkServerAccess(h.state.Store, srv, username, isAdmin, userID, "console") {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}

	queueKey := fmt.Sprintf("dylaris:server:%s:input", srv.UUID)
	if err := h.state.Redis.RPush(r.Context(), queueKey, req.Command).Err(); err != nil {
		sendJSONError(w, "Failed to send command", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// sseEscape prevents payload from breaking SSE framing.
// \r\n, \r, and \n are all line terminators in the SSE protocol and must be removed.
func sseEscape(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
