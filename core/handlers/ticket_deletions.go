package handlers

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"dylaris-core/models"
	"dylaris-core/storage"

	"github.com/gorilla/mux"
)

// TicketDeletionsHandler owns the admin-only DELETE /tickets/{id} +
// the audit log GET /admin/tickets/deletion-log. Gated by both the
// platform-wide tickets feature toggle (mounted in main.go) and the
// per-feature tickets.deletion_enabled setting (checked inline).
type TicketDeletionsHandler struct {
	state    *AppState
	provider storage.StorageProvider
}

func NewTicketDeletionsHandler(state *AppState) *TicketDeletionsHandler {
	return &TicketDeletionsHandler{
		state:    state,
		provider: buildAttachmentProvider(),
	}
}

// DeleteTicket DELETE /api/tickets/{id} — admin only, gated by
// tickets.deletion_enabled. Builds the audit snapshot BEFORE issuing the
// cascade so the audit row survives the source rows.
func (h *TicketDeletionsHandler) DeleteTicket(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	settings := LoadTicketSettings(h.state)
	if !settings.DeletionEnabled {
		// 403 not 503 — the feature exists, the admin has paused it for now.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "deletion_disabled",
			"message": "Ticket deletion is disabled. Enable it under Settings → Ticket Settings.",
		})
		return
	}
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil || id <= 0 {
		sendJSONError(w, "Invalid ticket id", http.StatusBadRequest)
		return
	}

	// Snapshot ticket subject + owner BEFORE delete.
	t, err := h.state.Store.GetTicket(id)
	if err == sql.ErrNoRows || t == nil {
		sendJSONError(w, "Ticket not found", http.StatusNotFound)
		return
	}
	if err != nil {
		sendJSONError(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Owner snapshot — fall back to "[deleted]" if the user row is already gone.
	ownerUsername := "[deleted]"
	if t.Username != "" {
		ownerUsername = t.Username
	}
	if owner, err := h.state.Store.GetUserByID(t.UserID); err == nil && owner != nil && owner.Username != "" {
		ownerUsername = owner.Username
	}

	// Category snapshot.
	var categoryName *string
	if cat, err := h.state.Store.GetTicketCategory(t.CategoryID); err == nil && cat != nil && cat.Name != "" {
		n := cat.Name
		categoryName = &n
	} else if t.CategoryName != "" {
		n := t.CategoryName
		categoryName = &n
	}

	// Actor snapshot.
	actorID, _ := r.Context().Value("userID").(string)
	actorName := actorID
	if actor, err := h.state.Store.GetUserByID(actorID); err == nil && actor != nil && actor.Username != "" {
		actorName = actor.Username
	}

	// Attachment storage keys — captured BEFORE delete so we can clean up
	// the file blobs after the rows are gone.
	storageKeys, _ := h.state.Store.ListAttachmentStorageKeysByTicket(id)

	// Build audit row.
	ownerID := t.UserID
	ip := clientIP(r)
	ua := r.UserAgent()
	rec := &models.TicketDeletion{
		TicketID:      id,
		TicketSubject: t.Title,
		OwnerUserID:   &ownerID,
		OwnerUsername: ownerUsername,
		CategoryName:  categoryName,
		DeletedBy:     actorID,
		DeletedByName: actorName,
		IPAddress:     &ip,
		UserAgent:     &ua,
	}

	// Stamp the audit row first so a half-deleted ticket still has a record.
	// The audit row is the contract; the cascade is the side-effect.
	if err := h.state.Store.InsertTicketDeletion(rec); err != nil {
		sendJSONError(w, "Failed to audit deletion: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.state.Store.DeleteTicket(id); err != nil {
		// Audit row is already in; surface the error but don't try to roll
		// back the audit — a stamped delete that didn't complete is itself
		// useful information.
		sendJSONError(w, "Failed to delete ticket: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Best-effort blob cleanup. Failures here are logged but don't bubble —
	// the ticket is gone from the DB; orphaned blobs are a cosmetic issue.
	for _, k := range storageKeys {
		if err := h.provider.DeletePath(k); err != nil {
			log.Printf("ticket-deletion: failed to remove attachment blob %s: %v", k, err)
		}
	}

	h.state.Events.Publish(r.Context(), "tickets.deleted", map[string]interface{}{
		"ticketId":  id,
		"deletedBy": actorID,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"ticketId": id,
	})
}

// ListDeletions GET /api/admin/tickets/deletion-log — paginated audit log.
func (h *TicketDeletionsHandler) ListDeletions(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	deletedBy := q.Get("deletedBy")

	rows, total, err := h.state.Store.ListTicketDeletions(limit, offset, deletedBy)
	if err != nil {
		sendJSONError(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []models.TicketDeletion{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"deletions": rows,
		"total":     total,
		"limit":     limit,
		"offset":    offset,
	})
}
