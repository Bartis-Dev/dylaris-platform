package store

// Per-server RCON console-noise toggle. Same shape and same reason as the edge
// MOTD next door: a lazy accessor plus one bulk read, so the column stays out of
// every server-list scan and the periodic Redis republish costs one query rather
// than one per server.

// ServerRconLogFilter is one server's toggle, keyed by UUID because that is what
// the log-shipper's Redis key is namespaced on.
type ServerRconLogFilter struct {
	UUID string
	On   bool
}

func (s *PostgresStore) GetServerRconLogFilter(serverID int) (bool, error) {
	var on bool
	err := s.db.QueryRow(`SELECT rcon_log_filter FROM servers WHERE id=$1`, serverID).Scan(&on)
	return on, err
}

func (s *PostgresStore) SetServerRconLogFilter(serverID int, on bool) error {
	_, err := s.db.Exec(`UPDATE servers SET rcon_log_filter=$2 WHERE id=$1`, serverID, on)
	return err
}

// ListServerRconLogFilter returns every server's toggle for the periodic Redis
// republish (one query, not one per server).
func (s *PostgresStore) ListServerRconLogFilter() ([]ServerRconLogFilter, error) {
	rows, err := s.db.Query(`SELECT uuid, rcon_log_filter FROM servers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ServerRconLogFilter
	for rows.Next() {
		var f ServerRconLogFilter
		if err := rows.Scan(&f.UUID, &f.On); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
