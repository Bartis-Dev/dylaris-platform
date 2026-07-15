package store

import (
	"database/sql"
	"encoding/json"
)

// Write-side owner-scoped server-role CRUD (phase 3). The read-side
// (GetServerRole) lives in authz_grants.go. Capability validation against the
// catalog is the handler's job: the store must not import authz (authz already
// imports store), so these methods persist whatever caps the handler passes.
// Every write is scoped by owner_user_id so a user can only touch their own
// realm's roles. JSONB uses the codebase's marshal-then-string($N::jsonb) form.

// CreateServerRole inserts an owner-scoped server role and returns its id. A nil
// capabilities slice is stored as an empty JSON array. A duplicate
// (owner_user_id, name) surfaces as the underlying unique-violation error.
func (s *PostgresStore) CreateServerRole(ownerUserID, name string, capabilities []string) (int, error) {
	if capabilities == nil {
		capabilities = []string{}
	}
	capsJSON, err := json.Marshal(capabilities)
	if err != nil {
		return 0, err
	}
	var id int
	err = s.db.QueryRow(
		`INSERT INTO server_roles (owner_user_id, name, capabilities) VALUES ($1, $2, $3::jsonb) RETURNING id`,
		ownerUserID, name, string(capsJSON)).Scan(&id)
	return id, err
}

// ListServerRolesByOwner returns every server role owned by ownerUserID, name-ordered.
func (s *PostgresStore) ListServerRolesByOwner(ownerUserID string) ([]ServerRole, error) {
	rows, err := s.db.Query(
		`SELECT id, owner_user_id, name, COALESCE(capabilities, '[]'::jsonb), created_at FROM server_roles WHERE owner_user_id = $1 ORDER BY name ASC`,
		ownerUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ServerRole
	for rows.Next() {
		var sr ServerRole
		var capsJSON []byte
		if err := rows.Scan(&sr.ID, &sr.OwnerUserID, &sr.Name, &capsJSON, &sr.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(capsJSON, &sr.Capabilities)
		out = append(out, sr)
	}
	return out, rows.Err()
}

// UpdateServerRole updates a role's name + capabilities, scoped by owner so a
// user cannot edit another owner's role even by guessing an id. The handler
// validates the caps first. Returns sql.ErrNoRows when no owned row matched.
func (s *PostgresStore) UpdateServerRole(id int, ownerUserID, name string, capabilities []string) error {
	if capabilities == nil {
		capabilities = []string{}
	}
	capsJSON, err := json.Marshal(capabilities)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(
		`UPDATE server_roles SET name = $3, capabilities = $4::jsonb WHERE id = $1 AND owner_user_id = $2`,
		id, ownerUserID, name, string(capsJSON))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteServerRole removes an owner's role by id. Returns sql.ErrNoRows when
// absent (or owned by someone else). The existing server_invites.server_role_id
// FK (ON DELETE SET NULL) unassigns any grants that referenced it.
func (s *PostgresStore) DeleteServerRole(id int, ownerUserID string) error {
	res, err := s.db.Exec(`DELETE FROM server_roles WHERE id = $1 AND owner_user_id = $2`, id, ownerUserID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
