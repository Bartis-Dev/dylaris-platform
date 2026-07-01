package store

import (
	"database/sql"
	"strings"

	"dylaris-core/models"
)

// prefixCols turns "a, b, c" into "alias.a, alias.b, alias.c" so a joined
// SELECT scans in the exact order of the shared column constant.
func prefixCols(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

func (s *PostgresStore) UpsertMod(m *models.Mod) (int, error) {
	ct := m.ContentType
	if ct == "" {
		ct = models.ContentTypeMod
	}
	var id int
	err := s.db.QueryRow(`INSERT INTO mods (owner_id, slug, pretty_name, author, description, link, content_type)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (owner_id, slug) DO UPDATE SET
			pretty_name=EXCLUDED.pretty_name, author=EXCLUDED.author,
			description=EXCLUDED.description, link=EXCLUDED.link, content_type=EXCLUDED.content_type
		RETURNING id`,
		m.OwnerID, m.Slug, m.PrettyName, m.Author, m.Description, m.Link, ct,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) GetModBySlug(ownerID, slug string) (*models.Mod, error) {
	var m models.Mod
	err := s.db.QueryRow(`SELECT id, owner_id, slug, pretty_name, author, description, link, content_type
		FROM mods WHERE owner_id=$1 AND slug=$2`, ownerID, slug).
		Scan(&m.ID, &m.OwnerID, &m.Slug, &m.PrettyName, &m.Author, &m.Description, &m.Link, &m.ContentType)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &m, err
}

const mvCols = `id, mod_id, version, filesize, storage_key, md5, sha1, sha512, url_override,
	source, target_path, modrinth_project_id, modrinth_version_id, modrinth_version_number,
	modrinth_game_versions, modrinth_latest_version_id, modrinth_last_checked, created_at, updated_at`

func scanModversion(row interface{ Scan(...interface{}) error }) (*models.Modversion, error) {
	var v models.Modversion
	if err := row.Scan(&v.ID, &v.ModID, &v.Version, &v.Filesize, &v.StorageKey, &v.MD5, &v.SHA1, &v.SHA512, &v.URLOverride,
		&v.Source, &v.TargetPath, &v.ModrinthProjectID, &v.ModrinthVersionID, &v.ModrinthVersionNumber,
		&v.ModrinthGameVersions, &v.ModrinthLatestVersionID, &v.ModrinthLastChecked, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return nil, err
	}
	return &v, nil
}

func (s *PostgresStore) CreateModversion(mv *models.Modversion) (int, error) {
	src := mv.Source
	if src == "" {
		src = models.SourceUpload
	}
	var id int
	err := s.db.QueryRow(`INSERT INTO modversions
		(mod_id, version, filesize, storage_key, md5, sha1, sha512, url_override, source, target_path,
		 modrinth_project_id, modrinth_version_id, modrinth_version_number, modrinth_game_versions)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`,
		mv.ModID, mv.Version, mv.Filesize, mv.StorageKey, mv.MD5, mv.SHA1, mv.SHA512, mv.URLOverride, src, mv.TargetPath,
		mv.ModrinthProjectID, mv.ModrinthVersionID, mv.ModrinthVersionNumber, mv.ModrinthGameVersions,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) UpdateModversion(mv *models.Modversion) error {
	_, err := s.db.Exec(`UPDATE modversions SET
		version=$1, filesize=$2, storage_key=$3, md5=$4, sha1=$5, sha512=$6, url_override=$7,
		source=$8, target_path=$9, modrinth_project_id=$10, modrinth_version_id=$11, modrinth_version_number=$12,
		modrinth_game_versions=$13, modrinth_latest_version_id=$14, modrinth_last_checked=$15, updated_at=NOW()
		WHERE id=$16`,
		mv.Version, mv.Filesize, mv.StorageKey, mv.MD5, mv.SHA1, mv.SHA512, mv.URLOverride,
		mv.Source, mv.TargetPath, mv.ModrinthProjectID, mv.ModrinthVersionID, mv.ModrinthVersionNumber,
		mv.ModrinthGameVersions, mv.ModrinthLatestVersionID, mv.ModrinthLastChecked, mv.ID)
	return err
}

func (s *PostgresStore) GetModversion(id int) (*models.Modversion, error) {
	row := s.db.QueryRow(`SELECT `+mvCols+` FROM modversions WHERE id=$1`, id)
	v, err := scanModversion(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return v, err
}

// FindModversionBySHA1 finds an existing artifact by the owner's catalog hash,
// used to auto-link/dedupe an uploaded jar. Joins through mods for owner scope.
func (s *PostgresStore) FindModversionBySHA1(ownerID, sha1 string) (*models.Modversion, error) {
	if sha1 == "" {
		return nil, nil
	}
	row := s.db.QueryRow(`SELECT `+prefixCols("mv", mvCols)+`
		FROM modversions mv JOIN mods m ON m.id = mv.mod_id
		WHERE m.owner_id=$1 AND mv.sha1=$2 LIMIT 1`, ownerID, sha1)
	v, err := scanModversion(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return v, err
}

func (s *PostgresStore) AttachModversionToBuild(buildID, modversionID int, side string) (int, error) {
	if side == "" {
		side = models.SideBoth
	}
	var id int
	err := s.db.QueryRow(`INSERT INTO build_modversions (build_id, modversion_id, side)
		VALUES ($1,$2,$3)
		ON CONFLICT (build_id, modversion_id) DO UPDATE SET side=EXCLUDED.side
		RETURNING id`, buildID, modversionID, side).Scan(&id)
	return id, err
}

func (s *PostgresStore) DetachFromBuild(buildID, modversionID int) error {
	_, err := s.db.Exec(`DELETE FROM build_modversions WHERE build_id=$1 AND modversion_id=$2`, buildID, modversionID)
	return err
}

func (s *PostgresStore) ListBuildContent(buildID int) ([]models.BuildContentEntry, error) {
	rows, err := s.db.Query(`SELECT `+prefixCols("mv", mvCols)+`,
		bmv.side, m.slug, m.pretty_name, m.content_type
		FROM build_modversions bmv
		JOIN modversions mv ON mv.id = bmv.modversion_id
		JOIN mods m ON m.id = mv.mod_id
		WHERE bmv.build_id=$1
		ORDER BY m.slug ASC`, buildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.BuildContentEntry{}
	for rows.Next() {
		var e models.BuildContentEntry
		v := &e.Modversion
		if err := rows.Scan(&v.ID, &v.ModID, &v.Version, &v.Filesize, &v.StorageKey, &v.MD5, &v.SHA1, &v.SHA512, &v.URLOverride,
			&v.Source, &v.TargetPath, &v.ModrinthProjectID, &v.ModrinthVersionID, &v.ModrinthVersionNumber,
			&v.ModrinthGameVersions, &v.ModrinthLatestVersionID, &v.ModrinthLastChecked, &v.CreatedAt, &v.UpdatedAt,
			&e.Side, &e.ModSlug, &e.PrettyName, &e.ContentType); err != nil {
			return nil, err
		}
		e.Linked = v.ModrinthProjectID != ""
		out = append(out, e)
	}
	return out, rows.Err()
}
