package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
)

// userID pulls the authenticated caller id (set by AuthMiddleware).
func solderCaller(r *http.Request) string {
	uid, _ := r.Context().Value("userID").(string)
	return uid
}

// --- Solder clients (per-owner) ---

// ListClients GET /api/solder/clients - the Technic launcher clients the
// caller has registered.
func (h *SolderHandler) ListClients(w http.ResponseWriter, r *http.Request) {
	clients, err := h.state.Store.ListSolderClientsByOwner(solderCaller(r))
	if err != nil {
		sendJSONError(w, "Failed to list clients", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(clients)
}

// CreateClient POST /api/solder/clients - registers a Technic launcher client
// under the caller.
func (h *SolderHandler) CreateClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	c, err := h.state.Store.CreateSolderClient(req.Name, solderCaller(r))
	if err != nil {
		log.Printf("solder CreateClient: %v", err)
		sendJSONError(w, "Failed to create client", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "client": c})
}

// DeleteClient DELETE /api/solder/clients/{id} - removes one of the caller's
// clients; the delete is owner-scoped.
func (h *SolderHandler) DeleteClient(w http.ResponseWriter, r *http.Request) {
	id := atoiVar(r, "id")
	if err := h.state.Store.DeleteSolderClient(id, solderCaller(r)); err != nil {
		sendJSONError(w, "Failed to delete client", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// --- Pack-client whitelist (per-pack, both pack and client must belong to caller) ---

// ownsPackAndClient loads the pack and client scoped to the caller; writes a 404
// and returns false if either is missing/foreign.
func (h *SolderHandler) ownsPackAndClient(w http.ResponseWriter, r *http.Request, packID, clientID int) bool {
	uid := solderCaller(r)
	pack, err := h.state.Store.GetPack(packID)
	if err != nil || pack == nil || pack.OwnerID != uid {
		sendJSONError(w, "Pack not found", http.StatusNotFound)
		return false
	}
	client, err := h.state.Store.GetSolderClient(clientID, uid)
	if err != nil || client == nil {
		sendJSONError(w, "Client not found", http.StatusNotFound)
		return false
	}
	return true
}

// ListPackClientsHandler GET /api/packs/{id}/clients - the Technic clients
// whitelisted for one pack. A pack the caller does not own is 404.
func (h *SolderHandler) ListPackClientsHandler(w http.ResponseWriter, r *http.Request) {
	packID := atoiVar(r, "id")
	uid := solderCaller(r)
	pack, err := h.state.Store.GetPack(packID)
	if err != nil || pack == nil || pack.OwnerID != uid {
		sendJSONError(w, "Pack not found", http.StatusNotFound)
		return
	}
	clients, err := h.state.Store.ListPackClients(packID)
	if err != nil {
		sendJSONError(w, "Failed to list whitelist", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(clients)
}

// AddPackClient POST /api/packs/{id}/clients/{clientId} - whitelists one
// client for a private pack. Both the pack and the client must belong to the
// caller.
func (h *SolderHandler) AddPackClient(w http.ResponseWriter, r *http.Request) {
	packID, clientID := atoiVar(r, "id"), atoiVar(r, "clientId")
	if !h.ownsPackAndClient(w, r, packID, clientID) {
		return
	}
	if err := h.state.Store.AddPackClient(packID, clientID); err != nil {
		sendJSONError(w, "Failed to add to whitelist", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// RemovePackClient DELETE /api/packs/{id}/clients/{clientId} - drops a client
// from a pack's whitelist.
func (h *SolderHandler) RemovePackClient(w http.ResponseWriter, r *http.Request) {
	packID, clientID := atoiVar(r, "id"), atoiVar(r, "clientId")
	if !h.ownsPackAndClient(w, r, packID, clientID) {
		return
	}
	if err := h.state.Store.RemovePackClient(packID, clientID); err != nil {
		sendJSONError(w, "Failed to remove from whitelist", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// --- Solder keys (per-owner; plaintext shown once) ---

// ListKeys GET /api/solder/keys - the caller's Solder API keys, hashes only.
func (h *SolderHandler) ListKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.state.Store.ListSolderKeysByOwner(solderCaller(r))
	if err != nil {
		sendJSONError(w, "Failed to list keys", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(keys)
}

// CreateKey POST /api/solder/keys - mints a 64-character Solder API key. Only
// its hash is stored, so the plaintext is shown once and cannot be recovered.
func (h *SolderHandler) CreateKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		sendJSONError(w, "Failed to generate key", http.StatusInternalServerError)
		return
	}
	plaintext := hex.EncodeToString(buf) // 64 chars, shown once
	k, err := h.state.Store.CreateSolderKey(req.Name, solderCaller(r), solderKeyHash(plaintext))
	if err != nil {
		log.Printf("solder CreateKey: %v", err)
		sendJSONError(w, "Failed to create key", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "plaintext": plaintext, "key": k})
}

// DeleteKey DELETE /api/solder/keys/{id} - revokes one of the caller's Solder
// keys.
func (h *SolderHandler) DeleteKey(w http.ResponseWriter, r *http.Request) {
	id := atoiVar(r, "id")
	if err := h.state.Store.DeleteSolderKey(id, solderCaller(r)); err != nil {
		sendJSONError(w, "Failed to delete key", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
