package store

import (
	"database/sql"
	"dylaris-core/models"
	"errors"
)

// Phase 10 — server_mods queries (Modrinth-sourced installs).

var errServerModNotFound = errors.New("server mod not found")

const serverModCols = `id, server_id, sub_server_name, modrinth_project_id,
		modrinth_project_slug, modrinth_version_id, title, file_name, sha512,
		installed_at, installed_by`

func scanServerMod(row interface {
	Scan(dest ...interface{}) error
}) (*models.ServerMod, error) {
	var m models.ServerMod
	var installedBy sql.NullInt64
	if err := row.Scan(&m.ID, &m.ServerID, &m.SubServerName, &m.ModrinthProjectID,
		&m.ModrinthProjectSlug, &m.ModrinthVersionID, &m.Title, &m.FileName, &m.SHA512,
		&m.InstalledAt, &installedBy); err != nil {
		return nil, err
	}
	if installedBy.Valid {
		v := int(installedBy.Int64)
		m.InstalledBy = &v
	}
	return &m, nil
}

func (s *PostgresStore) UpsertServerMod(m *models.ServerMod) (int, error) {
	var installedBy interface{}
	if m.InstalledBy != nil {
		installedBy = *m.InstalledBy
	}
	var id int
	err := s.db.QueryRow(`INSERT INTO server_mods
		(server_id, sub_server_name, modrinth_project_id, modrinth_project_slug,
		 modrinth_version_id, title, file_name, sha512, installed_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (server_id, sub_server_name, modrinth_project_id)
		DO UPDATE SET
			modrinth_project_slug = EXCLUDED.modrinth_project_slug,
			modrinth_version_id   = EXCLUDED.modrinth_version_id,
			title                 = EXCLUDED.title,
			file_name             = EXCLUDED.file_name,
			sha512                = EXCLUDED.sha512,
			installed_at          = NOW(),
			installed_by          = EXCLUDED.installed_by
		RETURNING id`,
		m.ServerID, m.SubServerName, m.ModrinthProjectID, m.ModrinthProjectSlug,
		m.ModrinthVersionID, m.Title, m.FileName, m.SHA512, installedBy,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) ListServerMods(serverID int, subServerName string) ([]models.ServerMod, error) {
	rows, err := s.db.Query(`SELECT `+serverModCols+`
		FROM server_mods WHERE server_id=$1 AND sub_server_name=$2
		ORDER BY title ASC, installed_at DESC`, serverID, subServerName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ServerMod{}
	for rows.Next() {
		m, err := scanServerMod(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (s *PostgresStore) DeleteServerMod(id, serverID int) error {
	res, err := s.db.Exec(`DELETE FROM server_mods WHERE id=$1 AND server_id=$2`, id, serverID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errServerModNotFound
	}
	return nil
}
