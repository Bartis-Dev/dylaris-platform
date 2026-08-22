package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dylaris-core/models"
	"dylaris-pkg/validate"

	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"
)

type ConsoleHandler struct {
	state *AppState
}

// consoleStreamKey resolves which log stream a console read should follow.
//
// The log shipper writes dylaris:server:<uuid>:logs:<sub> whenever the server
// has a sub-server, and every real server has one. An explicit ?sub_server=
// therefore wins, but an ABSENT one used to fall through to the un-suffixed
// key, which for such a server does not exist - so the request answered 200
// with an empty list while the server was running and its stream held
// hundreds of lines.
//
// The write side never had that problem: SendCommand pushes to
// dylaris:server:<uuid>:input, which is not per-sub-server, because only one
// sub-server runs at a time. So a caller could send commands to a server and
// hear nothing back, with neither call reporting an error. The read side now
// resolves the same "whichever one is running" the write side already assumes.
//
// The panel is unaffected: ConsoleView already passes the active sub-server
// on every request. This is for anyone who does not - the /api/external
// console route above all, where the caller is an integrator reading API.md
// and not a page that happens to hold the server object.
func consoleStreamKey(srv *models.Server, subServer string) string {
	if subServer == "" {
		subServer = srv.ActiveSubServer
	}
	if subServer == "" {
		return fmt.Sprintf("dylaris:server:%s:logs", srv.UUID)
	}
	return fmt.Sprintf("dylaris:server:%s:logs:%s", srv.UUID, subServer)
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

	subServer := r.URL.Query().Get("sub_server")
	if subServer != "" && !validate.IsSubServerName(subServer) {
		sendJSONError(w, "Invalid sub_server", http.StatusBadRequest)
		return
	}
	streamKey := consoleStreamKey(srv, subServer)
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
	if subServer != "" && !validate.IsSubServerName(subServer) {
		sendJSONError(w, "Invalid sub_server", http.StatusBadRequest)
		return
	}
	streamKey := consoleStreamKey(srv, subServer)
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

// SendCommand POST /api/servers/{id}/console/command - pushes one line onto
// the server's Redis input queue. Success means the node has been handed the
// command, not that the server has run it.
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
