package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"dylaris-core/models"
	"dylaris-core/storage"

	"github.com/gorilla/mux"
)

// StorageConnectionsHandler serves CRUD + a test probe for the shared, named
// storage connections (currently s3). Note the plural: the singular
// StorageConnectionHandler in storage_connection.go is a different thing (the
// core-storage backend health snapshot), so the two must not be conflated.
type StorageConnectionsHandler struct {
	state *AppState
}

func NewStorageConnectionsHandler(state *AppState) *StorageConnectionsHandler {
	return &StorageConnectionsHandler{state: state}
}

// storageConnectionRequest is the write DTO. The secret arrives here (a plain
// json field) but the model's SecretAccessKey is json:"-", so the model can
// neither receive it from nor leak it to the wire; the handler bridges the two.
type storageConnectionRequest struct {
	Name            string          `json:"name"`
	Provider        string          `json:"provider"`
	Config          json.RawMessage `json:"config"`
	AccessKey       string          `json:"accessKey"`
	SecretAccessKey string          `json:"secretAccessKey"`
}

// storageConnectionConfig is the non-secret portion of a connection's config
// JSONB. The access key and the secret live in their own columns, never here.
type storageConnectionConfig struct {
	Endpoint       string `json:"endpoint"`
	Region         string `json:"region"`
	Bucket         string `json:"bucket"`
	ForcePathStyle bool   `json:"forcePathStyle"`
	Prefix         string `json:"prefix"`
}

// validStorageConnectionProvider allowlists the providers a connection may use.
// Only s3 is credentialed today; the table has room for others later.
func validStorageConnectionProvider(p string) bool {
	return p == "s3"
}

// ListConnections GET /api/storage-connections. The secret never appears in the
// response: SecretAccessKey is json:"-" and the store does not decrypt on the
// list path, so only the SecretSet flag reports that one is stored.
func (h *StorageConnectionsHandler) ListConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := h.state.Store.ListStorageConnections()
	if err != nil {
		sendJSONError(w, "Database error", 500)
		return
	}
	if conns == nil {
		conns = []models.StorageConnection{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "connections": conns})
}

// CreateConnection POST /api/storage-connections.
func (h *StorageConnectionsHandler) CreateConnection(w http.ResponseWriter, r *http.Request) {
	var req storageConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}
	if req.Name == "" || req.Provider == "" {
		sendJSONError(w, "name and provider are required", 400)
		return
	}
	if !validStorageConnectionProvider(req.Provider) {
		sendJSONError(w, "invalid provider (expected s3)", 400)
		return
	}
	conn := models.StorageConnection{
		Name:            req.Name,
		Provider:        req.Provider,
		Config:          req.Config,
		AccessKey:       req.AccessKey,
		SecretAccessKey: req.SecretAccessKey,
	}
	id, err := h.state.Store.CreateStorageConnection(&conn)
	if err != nil {
		sendJSONError(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "id": id})
}

// UpdateConnection PATCH /api/storage-connections/{id}. The secret is write-only:
// the metadata update never touches secret_enc, and the secret is rotated only
// when a non-blank value was submitted. A blank secret (the "leave blank to
// keep" case) therefore preserves the stored credential.
func (h *StorageConnectionsHandler) UpdateConnection(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		sendJSONError(w, "Invalid ID", 400)
		return
	}
	var req storageConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}
	if !validStorageConnectionProvider(req.Provider) {
		sendJSONError(w, "invalid provider (expected s3)", 400)
		return
	}
	conn := models.StorageConnection{
		ID:        id,
		Name:      req.Name,
		Provider:  req.Provider,
		Config:    req.Config,
		AccessKey: req.AccessKey,
	}
	if err := h.state.Store.UpdateStorageConnection(&conn); err != nil {
		sendJSONError(w, err.Error(), 500)
		return
	}
	if req.SecretAccessKey != "" {
		if err := h.state.Store.SetStorageConnectionSecret(id, req.SecretAccessKey); err != nil {
			sendJSONError(w, err.Error(), 500)
			return
		}
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// DeleteConnection DELETE /api/storage-connections/{id}.
func (h *StorageConnectionsHandler) DeleteConnection(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		sendJSONError(w, "Invalid ID", 400)
		return
	}
	if err := h.state.Store.DeleteStorageConnection(id); err != nil {
		sendJSONError(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// TestConnection POST /api/storage-connections/{id}/test. Loads the saved
// connection (which decrypts the secret), builds a provider and runs the
// write/read-back/delete probe. Like the core-storage and backup probes it
// returns HTTP 200 with an "ok" verdict even on a failed probe - the transport
// succeeded; the verdict is the payload.
func (h *StorageConnectionsHandler) TestConnection(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		sendJSONError(w, "Invalid ID", 400)
		return
	}
	conn, err := h.state.Store.GetStorageConnection(id)
	if err != nil {
		sendJSONError(w, "Connection not found", 404)
		return
	}
	prov, err := storageConnectionProvider(conn)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "ok": false, "message": err.Error()})
		return
	}
	ok, message := probeStorageProvider(r.Context(), prov)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "ok": ok, "message": message})
}

// storageConnectionProvider builds a storage.StorageProvider from a resolved
// connection (its secret already decrypted into SecretAccessKey). The access key
// and secret come from their own columns; the rest from the config JSONB.
func storageConnectionProvider(conn *models.StorageConnection) (storage.StorageProvider, error) {
	if conn.Provider != "s3" {
		return nil, fmt.Errorf("unsupported provider %q", conn.Provider)
	}
	var cfg storageConnectionConfig
	if len(conn.Config) > 0 {
		if err := json.Unmarshal(conn.Config, &cfg); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}
	return storage.NewProvider("s3", "", map[string]string{
		storage.OptS3Endpoint:  cfg.Endpoint,
		storage.OptS3Region:    cfg.Region,
		storage.OptS3Bucket:    cfg.Bucket,
		storage.OptS3AccessKey: conn.AccessKey,
		storage.OptS3SecretKey: conn.SecretAccessKey,
		storage.OptS3PathStyle: boolStr(cfg.ForcePathStyle),
		storage.OptS3Prefix:    cfg.Prefix,
	})
}
