package store

import (
	"database/sql"

	"dylaris-core/models"
)

// sub_server_installs queries. One row per (server, sub-server) recording how it
// was installed, so the panel can put the same choices back on screen.

const subServerInstallCols = `sub_server_name, installer_type, mc_version, build_version, loader,
	modrinth_project_id, modrinth_version_id, modrinth_project_slug, pack_id, pack_build_id, installed_at`

// UpsertSubServerInstall records the install, replacing whatever was there.
//
// Wholesale rather than field-by-field, and that is the point: a reinstall from
// a modpack to plain Paper has to CLEAR the modpack reference, not leave it
// behind for the panel to prefill a pack the directory no longer contains.
// Passing a zero-valued install for those fields is how that clearing happens.
func (s *PostgresStore) UpsertSubServerInstall(in models.SubServerInstall) error {
	_, err := s.db.Exec(`
		INSERT INTO sub_server_installs
			(server_id, sub_server_name, installer_type, mc_version, build_version, loader,
			 modrinth_project_id, modrinth_version_id, modrinth_project_slug,
			 pack_id, pack_build_id, installed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW())
		ON CONFLICT (server_id, sub_server_name) DO UPDATE SET
			installer_type=EXCLUDED.installer_type,
			mc_version=EXCLUDED.mc_version,
			build_version=EXCLUDED.build_version,
			loader=EXCLUDED.loader,
			modrinth_project_id=EXCLUDED.modrinth_project_id,
			modrinth_version_id=EXCLUDED.modrinth_version_id,
			modrinth_project_slug=EXCLUDED.modrinth_project_slug,
			pack_id=EXCLUDED.pack_id,
			pack_build_id=EXCLUDED.pack_build_id,
			installed_at=NOW()`,
		in.ServerID, in.SubServerName, in.InstallerType, in.McVersion, in.BuildVersion, in.Loader,
		in.ModrinthProjectID, in.ModrinthVersionID, in.ModrinthProjectSlug,
		in.PackID, in.PackBuildID)
	return err
}

// GetSubServerInstall returns the recorded install, or nil when there is none.
//
// Nil is a real and common answer: every sub-server installed before this table
// existed has no row, and the panel must fall back to the servers row rather
// than show an empty form. "We never wrote this down" is not "it was installed
// with nothing".
func (s *PostgresStore) GetSubServerInstall(serverID int, subServer string) (*models.SubServerInstall, error) {
	in := models.SubServerInstall{ServerID: serverID}
	err := s.db.QueryRow(`SELECT `+subServerInstallCols+`
		FROM sub_server_installs WHERE server_id=$1 AND sub_server_name=$2`,
		serverID, subServer).Scan(
		&in.SubServerName, &in.InstallerType, &in.McVersion, &in.BuildVersion, &in.Loader,
		&in.ModrinthProjectID, &in.ModrinthVersionID, &in.ModrinthProjectSlug,
		&in.PackID, &in.PackBuildID, &in.InstalledAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &in, nil
}

// ListSubServerInstalls returns every recorded install for a server.
func (s *PostgresStore) ListSubServerInstalls(serverID int) ([]models.SubServerInstall, error) {
	rows, err := s.db.Query(`SELECT `+subServerInstallCols+`
		FROM sub_server_installs WHERE server_id=$1 ORDER BY sub_server_name`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.SubServerInstall{}
	for rows.Next() {
		in := models.SubServerInstall{ServerID: serverID}
		if err := rows.Scan(
			&in.SubServerName, &in.InstallerType, &in.McVersion, &in.BuildVersion, &in.Loader,
			&in.ModrinthProjectID, &in.ModrinthVersionID, &in.ModrinthProjectSlug,
			&in.PackID, &in.PackBuildID, &in.InstalledAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// DeleteSubServerInstall drops the record when a sub-server is deleted. The row
// would otherwise outlive the directory and prefill a form for something that is
// no longer on disk.
func (s *PostgresStore) DeleteSubServerInstall(serverID int, subServer string) error {
	_, err := s.db.Exec(`DELETE FROM sub_server_installs WHERE server_id=$1 AND sub_server_name=$2`,
		serverID, subServer)
	return err
}
