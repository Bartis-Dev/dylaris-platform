package store

import (
	"database/sql"
	"encoding/json"
)

// Write-side upsert/delete for the reworked server_invites grant (phase 3).
// server_id NULL is an ACCOUNT-WIDE grant (all the owner's servers + owner
// tools); a non-null server_id is a per-server grant. The two shapes need two
// different ON CONFLICT targets: Postgres treats NULL server_id as distinct, so
// the account-wide path relies on the F6 partial unique index
// (owner_user_id, user_id) WHERE server_id IS NULL (created in phase 3 schema).
// invited_by is the legacy NOT NULL column; it is set to owner_user_id here
// (the acting principal is recorded in the handler audit, not this column).

// UpsertServerGrant inserts or updates a friend's grant. Capability validation
// + delegation-cap enforcement happen in the handler; this method persists
// whatever the handler resolved.
func (s *PostgresStore) UpsertServerGrant(serverID *int, userID, ownerUserID string, serverRoleID *int, overrides CapOverrides, inherit bool) error {
	ovJSON, err := json.Marshal(overrides)
	if err != nil {
		return err
	}
	var roleArg interface{}
	if serverRoleID != nil {
		roleArg = *serverRoleID
	}
	if serverID == nil {
		_, err = s.db.Exec(
			`INSERT INTO server_invites (server_id, user_id, owner_user_id, server_role_id, cap_overrides, inherit, invited_by) VALUES (NULL, $1, $2, $3, $4::jsonb, $5, $2) ON CONFLICT (owner_user_id, user_id) WHERE server_id IS NULL DO UPDATE SET server_role_id = EXCLUDED.server_role_id, cap_overrides = EXCLUDED.cap_overrides, inherit = EXCLUDED.inherit`,
			userID, ownerUserID, roleArg, string(ovJSON), inherit)
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO server_invites (server_id, user_id, owner_user_id, server_role_id, cap_overrides, inherit, invited_by) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $3) ON CONFLICT (server_id, user_id) DO UPDATE SET owner_user_id = EXCLUDED.owner_user_id, server_role_id = EXCLUDED.server_role_id, cap_overrides = EXCLUDED.cap_overrides, inherit = EXCLUDED.inherit`,
		*serverID, userID, ownerUserID, roleArg, string(ovJSON), inherit)
	return err
}

// DeleteServerGrant removes a friend's account-wide (serverID nil) or per-server
// grant. Returns sql.ErrNoRows when nothing matched. ownerUserID scopes the
// account-wide delete to the correct realm; the per-server delete is already
// unique on (server_id, user_id).
func (s *PostgresStore) DeleteServerGrant(serverID *int, ownerUserID, userID string) error {
	var res sql.Result
	var err error
	if serverID == nil {
		res, err = s.db.Exec(
			`DELETE FROM server_invites WHERE server_id IS NULL AND owner_user_id = $1 AND user_id = $2`,
			ownerUserID, userID)
	} else {
		res, err = s.db.Exec(
			`DELETE FROM server_invites WHERE server_id = $1 AND user_id = $2`,
			*serverID, userID)
	}
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
