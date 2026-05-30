package store

import (
	"database/sql"
	"dylaris-core/models"
	"strconv"
)

// RenameUser is the canonical username-change path. Single transaction:
// insert history row, update users row. Atomicity matters — a partial run
// would let a duplicate username sneak in between insert and update.
func (s *PostgresStore) RenameUser(userID, newUsername, changedBy string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldUsername string
	if err := tx.QueryRow(`SELECT username FROM users WHERE id=$1`, userID).Scan(&oldUsername); err != nil {
		return err
	}
	if oldUsername == newUsername {
		return tx.Commit() // no-op
	}
	var changedByArg interface{}
	if changedBy != "" {
		changedByArg = changedBy
	}
	if _, err := tx.Exec(`INSERT INTO user_username_history
		(user_id, old_username, new_username, changed_by)
		VALUES ($1, $2, $3, $4)`,
		userID, oldUsername, newUsername, changedByArg); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE users SET username=$2, last_username_change=NOW() WHERE id=$1`,
		userID, newUsername); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) ListUsernameHistory(userID string) ([]models.UsernameHistory, error) {
	rows, err := s.db.Query(`SELECT id, user_id, old_username, new_username,
		changed_at, changed_by
		FROM user_username_history WHERE user_id=$1 ORDER BY changed_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.UsernameHistory{}
	for rows.Next() {
		var h models.UsernameHistory
		var changedBy sql.NullString
		if err := rows.Scan(&h.ID, &h.UserID, &h.OldUsername, &h.NewUsername, &h.ChangedAt, &changedBy); err != nil {
			return nil, err
		}
		if changedBy.Valid {
			cb := changedBy.String
			h.ChangedBy = &cb
			h.ByAdmin = cb != h.UserID
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetUserAccountPolicy() (bool, int, error) {
	var allow, cooldown string
	_ = s.db.QueryRow(`SELECT value FROM settings WHERE key='users.allow_name_change'`).Scan(&allow)
	_ = s.db.QueryRow(`SELECT value FROM settings WHERE key='users.name_change_cooldown_days'`).Scan(&cooldown)
	allowB := allow != "false"
	cd, _ := strconv.Atoi(cooldown)
	if cd <= 0 {
		cd = 30
	}
	return allowB, cd, nil
}

func (s *PostgresStore) SetUserAccountPolicy(allowChange bool, cooldownDays int) error {
	if cooldownDays < 0 {
		cooldownDays = 0
	}
	allow := "true"
	if !allowChange {
		allow = "false"
	}
	for _, kv := range []struct{ k, v string }{
		{"users.allow_name_change", allow},
		{"users.name_change_cooldown_days", strconv.Itoa(cooldownDays)},
	} {
		if _, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, kv.k, kv.v); err != nil {
			return err
		}
	}
	return nil
}
