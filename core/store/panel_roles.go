package store

import (
	"database/sql"
	"encoding/json"
)

// Write-side panel-role CRUD + per-user assignment (phase 2). The read-side
// (GetPanelRole) lives in authz_grants.go. Capability validation against the
// catalog is the handler's job: the store must not import authz (authz already
// imports store), so these methods persist whatever caps the handler passes.
// JSONB payloads use the codebase's marshal-then-string($N::jsonb) convention.

// CreatePanelRole inserts a non-system panel role and returns its id. A nil
// capabilities slice is stored as an empty JSON array. createdBy nil -> NULL.
// A duplicate name surfaces as the underlying unique-violation error.
func (s *PostgresStore) CreatePanelRole(name string, capabilities []string, createdBy *string) (int, error) {
	if capabilities == nil {
		capabilities = []string{}
	}
	capsJSON, err := json.Marshal(capabilities)
	if err != nil {
		return 0, err
	}
	var createdByArg interface{}
	if createdBy != nil {
		createdByArg = *createdBy
	}
	var id int
	err = s.db.QueryRow(
		`INSERT INTO panel_roles (name, capabilities, is_system, created_by) VALUES ($1, $2::jsonb, FALSE, $3) RETURNING id`,
		name, string(capsJSON), createdByArg).Scan(&id)
	return id, err
}

// ListPanelRoles returns every panel role, system roles first then by name.
func (s *PostgresStore) ListPanelRoles() ([]PanelRole, error) {
	rows, err := s.db.Query(
		`SELECT id, name, COALESCE(capabilities, '[]'::jsonb), is_system, created_by, created_at FROM panel_roles ORDER BY is_system DESC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PanelRole
	for rows.Next() {
		var pr PanelRole
		var capsJSON []byte
		var createdBy sql.NullString
		if err := rows.Scan(&pr.ID, &pr.Name, &capsJSON, &pr.IsSystem, &createdBy, &pr.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(capsJSON, &pr.Capabilities)
		if createdBy.Valid {
			pr.CreatedBy = &createdBy.String
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// UpdatePanelRole updates a role's name + capabilities. The handler enforces
// the is_system immutability rule (403) before calling this. Returns
// sql.ErrNoRows when no row matched (e.g. deleted concurrently).
func (s *PostgresStore) UpdatePanelRole(id int, name string, capabilities []string) error {
	if capabilities == nil {
		capabilities = []string{}
	}
	capsJSON, err := json.Marshal(capabilities)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(
		`UPDATE panel_roles SET name = $2, capabilities = $3::jsonb WHERE id = $1`,
		id, name, string(capsJSON))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeletePanelRole removes a role by id. Returns sql.ErrNoRows when absent. The
// existing users.panel_role_id FK (ON DELETE SET NULL) unassigns any holders.
func (s *PostgresStore) DeletePanelRole(id int) error {
	res, err := s.db.Exec(`DELETE FROM panel_roles WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetUserPanelRole assigns (or clears, when roleID is nil) a user's level-1
// panel role. Returns sql.ErrNoRows when the user id does not exist.
func (s *PostgresStore) SetUserPanelRole(userID string, roleID *int) error {
	var roleArg interface{}
	if roleID != nil {
		roleArg = *roleID
	}
	res, err := s.db.Exec(`UPDATE users SET panel_role_id = $1 WHERE id = $2`, roleArg, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetUserPanelCapOverrides writes the user's per-user panel cap grant/deny set.
// Returns sql.ErrNoRows when the user id does not exist.
func (s *PostgresStore) SetUserPanelCapOverrides(userID string, ov CapOverrides) error {
	ovJSON, err := json.Marshal(ov)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE users SET panel_cap_overrides = $1::jsonb WHERE id = $2`, string(ovJSON), userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
