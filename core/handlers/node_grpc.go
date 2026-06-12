package handlers

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	// Node connections are server-to-server and send no Origin header. Allow
	// empty/same-origin and reject foreign Origins. Auth here is token-based
	// (not cookie-based), so this is defense-in-depth rather than the primary
	// guard.
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		return origin == "" || origin == "http://"+r.Host || origin == "https://"+r.Host
	},
}

type NodeGRPCHandler struct {
	state *AppState
}

func NewNodeGRPCHandler(state *AppState) *NodeGRPCHandler {
	return &NodeGRPCHandler{state: state}
}

// NodeConnectHandler (Legacy / Status Check)
// Since the Node now uses Redis, this endpoint is optional.
func (h *NodeGRPCHandler) NodeConnectHandler(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.Header.Get("X-Dylaris-Token")
	}

	if token == "" {
		http.Error(w, "Token required", 401)
		return
	}

	// Use Store for token check
	node, err := h.state.Store.GetNodeByToken(token)
	if err != nil {
		// Never log the token value itself — it is a credential.
		log.Printf("Node connect rejected: unknown token")
		http.Error(w, "Invalid Token", 401)
		return
	}

	// Update Status (via Store)
	h.state.Store.SetNodeStatus(node.ID, "online")
	log.Printf("Node Ping (Legacy): %s", node.Name)

	// Keep connection open for basic compatibility if needed
	conn, err := upgrader.Upgrade(w, r, nil)
	if err == nil {
		defer conn.Close()
		defer h.state.Store.SetNodeStatus(node.ID, "offline")
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}
}
