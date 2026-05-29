package store

import (
	"database/sql"
	"dylaris-core/models"
	"errors"
)

// Phase 14 — modpack authoring + Modrinth PAT storage queries.

var (
	errModpackNotFound        = errors.New("modpack not found")
	errModpackVersionNotFound = errors.New("modpack version not found")
	errModpackModNotFound     = errors.New("modpack mod not found")
)

// --- Modpacks ---

const modpackCols = `id, owner_id, name, slug, summary, mc_version, loader,
		modrinth_project_id, modrinth_visibility, created_at, updated_at`

func scanModpack(row interface{ Scan(...interface{}) error }) (*models.Modpack, error) {
	var m models.Modpack
	if err := row.Scan(&m.ID, &m.OwnerID, &m.Name, &m.Slug, &m.Summary,
		&m.McVersion, &m.Loader, &m.ModrinthProjectID, &m.ModrinthVisibility,
		&m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *PostgresStore) CreateModpack(m *models.Modpack) (int, error) {
	var id int
	err := s.db.QueryRow(`INSERT INTO modpacks
		(owner_id, name, slug, summary, mc_version, loader, modrinth_visibility)
		VALUES ($1,$2,$3,$4,$5,$6,COALESCE(NULLIF($7,''),'unlisted'))
		RETURNING id`,
		m.OwnerID, m.Name, m.Slug, m.Summary, m.McVersion, m.Loader, m.ModrinthVisibility,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) UpdateModpack(m *models.Modpack) error {
	res, err := s.db.Exec(`UPDATE modpacks SET
		name=$2, summary=$3, mc_version=$4, loader=$5,
		modrinth_project_id=$6, modrinth_visibility=$7, updated_at=NOW()
		WHERE id=$1`,
		m.ID, m.Name, m.Summary, m.McVersion, m.Loader,
		m.ModrinthProjectID, m.ModrinthVisibility)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errModpackNotFound
	}
	return nil
}

func (s *PostgresStore) DeleteModpack(id, ownerID int) error {
	res, err := s.db.Exec(`DELETE FROM modpacks WHERE id=$1 AND owner_id=$2`, id, ownerID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errModpackNotFound
	}
	return nil
}

func (s *PostgresStore) GetModpack(id int) (*models.Modpack, error) {
	row := s.db.QueryRow(`SELECT `+modpackCols+` FROM modpacks WHERE id=$1`, id)
	m, err := scanModpack(row)
	if err == sql.ErrNoRows {
		return nil, errModpackNotFound
	}
	return m, err
}

func (s *PostgresStore) ListModpacksByOwner(ownerID int) ([]models.Modpack, error) {
	rows, err := s.db.Query(`SELECT `+modpackCols+`
		FROM modpacks WHERE owner_id=$1 ORDER BY updated_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Modpack{}
	for rows.Next() {
		m, err := scanModpack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// --- Modpack Versions ---

const modpackVersionCols = `id, modpack_id, version_string, channel, changelog,
		mrpack_storage_path, file_size, modrinth_version_id, created_at, published_at`

func scanModpackVersion(row interface{ Scan(...interface{}) error }) (*models.ModpackVersion, error) {
	var v models.ModpackVersion
	var publishedAt sql.NullTime
	if err := row.Scan(&v.ID, &v.ModpackID, &v.VersionString, &v.Channel,
		&v.Changelog, &v.MrpackStoragePath, &v.FileSize, &v.ModrinthVersionID,
		&v.CreatedAt, &publishedAt); err != nil {
		return nil, err
	}
	if publishedAt.Valid {
		t := publishedAt.Time
		v.PublishedAt = &t
	}
	return &v, nil
}

func (s *PostgresStore) CreateModpackVersion(v *models.ModpackVersion) (int, error) {
	var publishedAt interface{}
	if v.PublishedAt != nil {
		publishedAt = *v.PublishedAt
	}
	channel := v.Channel
	if channel == "" {
		channel = models.ModpackChannelDraft
	}
	var id int
	err := s.db.QueryRow(`INSERT INTO modpack_versions
		(modpack_id, version_string, channel, changelog, mrpack_storage_path,
		 file_size, modrinth_version_id, published_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id`,
		v.ModpackID, v.VersionString, channel, v.Changelog,
		v.MrpackStoragePath, v.FileSize, v.ModrinthVersionID, publishedAt,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) UpdateModpackVersion(v *models.ModpackVersion) error {
	var publishedAt interface{}
	if v.PublishedAt != nil {
		publishedAt = *v.PublishedAt
	}
	res, err := s.db.Exec(`UPDATE modpack_versions SET
		version_string=$2, channel=$3, changelog=$4, mrpack_storage_path=$5,
		file_size=$6, modrinth_version_id=$7, published_at=$8
		WHERE id=$1`,
		v.ID, v.VersionString, v.Channel, v.Changelog, v.MrpackStoragePath,
		v.FileSize, v.ModrinthVersionID, publishedAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errModpackVersionNotFound
	}
	return nil
}

func (s *PostgresStore) DeleteModpackVersion(id, modpackID int) error {
	res, err := s.db.Exec(`DELETE FROM modpack_versions WHERE id=$1 AND modpack_id=$2`, id, modpackID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errModpackVersionNotFound
	}
	return nil
}

func (s *PostgresStore) GetModpackVersion(id int) (*models.ModpackVersion, error) {
	row := s.db.QueryRow(`SELECT `+modpackVersionCols+` FROM modpack_versions WHERE id=$1`, id)
	v, err := scanModpackVersion(row)
	if err == sql.ErrNoRows {
		return nil, errModpackVersionNotFound
	}
	return v, err
}

func (s *PostgresStore) ListModpackVersions(modpackID int) ([]models.ModpackVersion, error) {
	rows, err := s.db.Query(`SELECT `+modpackVersionCols+`
		FROM modpack_versions WHERE modpack_id=$1 ORDER BY created_at DESC`, modpackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ModpackVersion{}
	for rows.Next() {
		v, err := scanModpackVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// --- Modpack Mods ---

const modpackModCols = `id, modpack_version_id, modrinth_project_id,
		modrinth_project_slug, modrinth_version_id, title, file_name,
		download_url, sha512, side, required`

func scanModpackMod(row interface{ Scan(...interface{}) error }) (*models.ModpackMod, error) {
	var m models.ModpackMod
	if err := row.Scan(&m.ID, &m.ModpackVersionID, &m.ModrinthProjectID,
		&m.ModrinthProjectSlug, &m.ModrinthVersionID, &m.Title, &m.FileName,
		&m.DownloadURL, &m.SHA512, &m.Side, &m.Required); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *PostgresStore) UpsertModpackMod(m *models.ModpackMod) (int, error) {
	side := m.Side
	if side == "" {
		side = "both"
	}
	var id int
	err := s.db.QueryRow(`INSERT INTO modpack_mods
		(modpack_version_id, modrinth_project_id, modrinth_project_slug,
		 modrinth_version_id, title, file_name, download_url, sha512,
		 side, required)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (modpack_version_id, modrinth_project_id) DO UPDATE SET
			modrinth_project_slug = EXCLUDED.modrinth_project_slug,
			modrinth_version_id   = EXCLUDED.modrinth_version_id,
			title                 = EXCLUDED.title,
			file_name             = EXCLUDED.file_name,
			download_url          = EXCLUDED.download_url,
			sha512                = EXCLUDED.sha512,
			side                  = EXCLUDED.side,
			required              = EXCLUDED.required
		RETURNING id`,
		m.ModpackVersionID, m.ModrinthProjectID, m.ModrinthProjectSlug,
		m.ModrinthVersionID, m.Title, m.FileName, m.DownloadURL, m.SHA512,
		side, m.Required,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) ListModpackMods(versionID int) ([]models.ModpackMod, error) {
	rows, err := s.db.Query(`SELECT `+modpackModCols+`
		FROM modpack_mods WHERE modpack_version_id=$1 ORDER BY title ASC`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ModpackMod{}
	for rows.Next() {
		m, err := scanModpackMod(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (s *PostgresStore) DeleteModpackMod(id, versionID int) error {
	res, err := s.db.Exec(`DELETE FROM modpack_mods WHERE id=$1 AND modpack_version_id=$2`, id, versionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errModpackModNotFound
	}
	return nil
}

// --- Modrinth PATs ---

func (s *PostgresStore) SetModrinthPAT(userID int, ciphertext, modrinthUsername string) error {
	_, err := s.db.Exec(`INSERT INTO modrinth_pats
		(user_id, ciphertext, modrinth_username, last_validated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			ciphertext        = EXCLUDED.ciphertext,
			modrinth_username = EXCLUDED.modrinth_username,
			last_validated_at = NOW(),
			updated_at        = NOW()`,
		userID, ciphertext, modrinthUsername)
	return err
}

func (s *PostgresStore) GetModrinthPAT(userID int) (*models.ModrinthPAT, error) {
	var p models.ModrinthPAT
	var lastValidated sql.NullTime
	err := s.db.QueryRow(`SELECT user_id, ciphertext, modrinth_username,
		last_validated_at, created_at, updated_at
		FROM modrinth_pats WHERE user_id=$1`, userID).Scan(
		&p.UserID, &p.Ciphertext, &p.ModrinthUsername,
		&lastValidated, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastValidated.Valid {
		t := lastValidated.Time
		p.LastValidatedAt = &t
	}
	return &p, nil
}

func (s *PostgresStore) ClearModrinthPAT(userID int) error {
	_, err := s.db.Exec(`DELETE FROM modrinth_pats WHERE user_id=$1`, userID)
	return err
}
