package store

import "dylaris-core/models"

// server_modpack_contents queries. The per-(server, sub-server) snapshot of a
// modpack's Modrinth-identified members, backing the advisory Content-tab
// cross-check. Written wholesale at install/reinstall; read by the panel.

// ReplaceServerModpackContents clears the (server_id, sub_server_name) snapshot
// and re-inserts rows, atomically, so a reinstall refreshes cleanly. Passing an
// empty slice clears the snapshot (e.g. a reinstall to a non-modpack installer).
// ON CONFLICT DO UPDATE keeps the last write if a pack lists a project twice.
func (s *PostgresStore) ReplaceServerModpackContents(serverID int, subServer string, rows []models.ServerModpackContent) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM server_modpack_contents
		WHERE server_id=$1 AND sub_server_name=$2`, serverID, subServer); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := tx.Exec(`INSERT INTO server_modpack_contents
			(server_id, sub_server_name, modrinth_project_id, modrinth_version_id,
			 modrinth_version_number, file_name, side)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (server_id, sub_server_name, modrinth_project_id)
			DO UPDATE SET
				modrinth_version_id     = EXCLUDED.modrinth_version_id,
				modrinth_version_number = EXCLUDED.modrinth_version_number,
				file_name               = EXCLUDED.file_name,
				side                    = EXCLUDED.side`,
			serverID, subServer, r.ModrinthProjectID, r.ModrinthVersionID,
			r.ModrinthVersionNumber, r.FileName, r.Side); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) ListServerModpackContents(serverID int, subServer string) ([]models.ServerModpackContent, error) {
	rows, err := s.db.Query(`SELECT id, server_id, sub_server_name, modrinth_project_id,
		modrinth_version_id, modrinth_version_number, file_name, side
		FROM server_modpack_contents
		WHERE server_id=$1 AND sub_server_name=$2
		ORDER BY file_name ASC`, serverID, subServer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ServerModpackContent{}
	for rows.Next() {
		var m models.ServerModpackContent
		if err := rows.Scan(&m.ID, &m.ServerID, &m.SubServerName, &m.ModrinthProjectID,
			&m.ModrinthVersionID, &m.ModrinthVersionNumber, &m.FileName, &m.Side); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
