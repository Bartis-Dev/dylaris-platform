package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"dylaris-core/services"
)

// ChangelogHandler serves the in-panel changelog drawer and tracks the
// per-user last-seen cursor used for the unread badge.
type ChangelogHandler struct {
	state *AppState
}

func NewChangelogHandler(state *AppState) *ChangelogHandler {
	return &ChangelogHandler{state: state}
}

// changelogResponse is the GET /api/changelog payload.
type changelogResponse struct {
	Success     bool                        `json:"success"`
	Released    []services.ChangelogEntry   `json:"released"`
	ComingSoon  []services.ChangelogEntry   `json:"comingSoon"`
	UnreadCount int                         `json:"unreadCount"`
	LastSeen    *string                     `json:"lastSeen"`
}

// Get GET /api/changelog
// Returns released + coming-soon entries (audience-filtered for non-admins),
// the unread count, and the user's last-seen cursor. Always success-shaped.
func (h *ChangelogHandler) Get(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		sendJSONError(w, "Unauthenticated", http.StatusUnauthorized)
		return
	}
	isAdmin := IsAdmin(r)

	released := h.state.Changelog.Released(isAdmin)
	comingSoon := h.state.Changelog.ComingSoon(isAdmin)

	// last-seen cursor; missing column / fresh user → nil → everything unread.
	lastSeen, _ := h.state.Store.GetLastSeenChangelog(userID)
	var lastSeenStr *string
	cursor := time.Time{}
	if lastSeen != nil {
		s := lastSeen.Format("2006-01-02")
		lastSeenStr = &s
		cursor = *lastSeen
	}

	unread := h.state.Changelog.CountUnread(cursor, isAdmin)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(changelogResponse{
		Success:     true,
		Released:    released,
		ComingSoon:  comingSoon,
		UnreadCount: unread,
		LastSeen:    lastSeenStr,
	})
}

// markSeenRequest is the POST /api/changelog/mark-seen body.
type markSeenRequest struct {
	LatestDate string `json:"latestDate"`
}

// MarkSeen POST /api/changelog/mark-seen
// Body: { latestDate: "2026-06-09" }. The server clamps upward so reopening
// an older entry doesn't drop the cursor — only forward progress sticks.
func (h *ChangelogHandler) MarkSeen(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		sendJSONError(w, "Unauthenticated", http.StatusUnauthorized)
		return
	}

	var req markSeenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.LatestDate == "" {
		sendJSONError(w, "latestDate is required", http.StatusBadRequest)
		return
	}
	newDate, err := time.Parse("2006-01-02", req.LatestDate)
	if err != nil {
		sendJSONError(w, "latestDate must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	// Clamp upward: never let a re-mark of an older entry move the cursor back.
	current, _ := h.state.Store.GetLastSeenChangelog(userID)
	final := newDate
	if current != nil && current.After(newDate) {
		final = *current
	}

	if err := h.state.Store.SetLastSeenChangelog(userID, final); err != nil {
		sendJSONError(w, "Failed to save cursor", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"lastSeen": final.Format("2006-01-02"),
	})
}
