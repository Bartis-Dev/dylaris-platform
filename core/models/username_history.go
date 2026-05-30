package models

import "time"

// UsernameHistory mirrors a row of user_username_history. ChangedBy is
// nullable — when the user renamed themselves it equals UserID; when an
// admin force-renamed it's the admin's UUID; when the actor was later
// deleted it's NULL.
type UsernameHistory struct {
	ID          int       `json:"id"`
	UserID      string    `json:"userId"`
	OldUsername string    `json:"oldUsername"`
	NewUsername string    `json:"newUsername"`
	ChangedAt   time.Time `json:"changedAt"`
	ChangedBy   *string   `json:"changedBy,omitempty"`
	ByAdmin     bool      `json:"byAdmin"` // computed: changedBy != nil && changedBy != userID
}
