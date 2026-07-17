package store

// Edge transitional-MOTD per-server config. Kept out of the main postgres.go
// server scans (same rationale as RCON in postgres_phase9.go): the two columns
// would otherwise bloat every server-list query for a setting only the gateway
// edge and the config page read.

// ServerEdgeMotd is one server's edge-MOTD config, keyed by UUID so the periodic
// Redis republish loop can push dylaris:server:<uuid>:edge_motd_* without a
// per-server round trip.
type ServerEdgeMotd struct {
	UUID string
	Mode string
	Text string
}

func (s *PostgresStore) GetServerEdgeMotd(serverID int) (string, string, error) {
	var mode, text string
	err := s.db.QueryRow(`SELECT edge_motd_mode, edge_motd_text FROM servers WHERE id=$1`, serverID).Scan(&mode, &text)
	return mode, text, err
}

func (s *PostgresStore) SetServerEdgeMotd(serverID int, mode, text string) error {
	_, err := s.db.Exec(`UPDATE servers SET edge_motd_mode=$2, edge_motd_text=$3 WHERE id=$1`, serverID, mode, text)
	return err
}

// ListServerEdgeMotd returns every server's edge-MOTD config for the periodic
// Redis republish (one query, not one per server).
func (s *PostgresStore) ListServerEdgeMotd() ([]ServerEdgeMotd, error) {
	rows, err := s.db.Query(`SELECT uuid, edge_motd_mode, edge_motd_text FROM servers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ServerEdgeMotd
	for rows.Next() {
		var m ServerEdgeMotd
		if err := rows.Scan(&m.UUID, &m.Mode, &m.Text); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
