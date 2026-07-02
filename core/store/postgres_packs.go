package store

import (
	"database/sql"
	"errors"

	"dylaris-core/models"
)

var errPackNotFound = errors.New("pack not found")

const packCols = `id, owner_id, internal_name, internal_slug, summary,
	solder_display_name, solder_slug, hidden, private, recommended_build, latest_build,
	icon_url, logo_url, background_url, icon_md5, logo_md5, background_md5,
	modrinth_project_id, modrinth_project_name, modrinth_visibility, created_at, updated_at`

func scanPack(row interface{ Scan(...interface{}) error }) (*models.Pack, error) {
	var p models.Pack
	if err := row.Scan(&p.ID, &p.OwnerID, &p.InternalName, &p.InternalSlug, &p.Summary,
		&p.SolderDisplayName, &p.SolderSlug, &p.Hidden, &p.Private, &p.RecommendedBuild, &p.LatestBuild,
		&p.IconURL, &p.LogoURL, &p.BackgroundURL, &p.IconMD5, &p.LogoMD5, &p.BackgroundMD5,
		&p.ModrinthProjectID, &p.ModrinthProjectName, &p.ModrinthVisibility, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *PostgresStore) CreatePack(p *models.Pack) (int, error) {
	var id int
	err := s.db.QueryRow(`INSERT INTO packs
		(owner_id, internal_name, internal_slug, summary, solder_display_name, solder_slug,
		 hidden, private, modrinth_visibility)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		p.OwnerID, p.InternalName, p.InternalSlug, p.Summary, p.SolderDisplayName, p.SolderSlug,
		p.Hidden, p.Private, p.ModrinthVisibility,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) UpdatePack(p *models.Pack) error {
	// internal_slug is intentionally immutable after creation: it is the stable
	// UNIQUE (owner_id, internal_slug) handle. Rename the launcher-facing
	// identity via solder_slug, never the internal slug.
	_, err := s.db.Exec(`UPDATE packs SET
		internal_name=$1, summary=$2, solder_display_name=$3, solder_slug=$4,
		hidden=$5, private=$6, recommended_build=$7, latest_build=$8,
		icon_url=$9, logo_url=$10, background_url=$11, icon_md5=$12, logo_md5=$13, background_md5=$14,
		modrinth_project_id=$15, modrinth_project_name=$16, modrinth_visibility=$17, updated_at=NOW()
		WHERE id=$18`,
		p.InternalName, p.Summary, p.SolderDisplayName, p.SolderSlug,
		p.Hidden, p.Private, p.RecommendedBuild, p.LatestBuild,
		p.IconURL, p.LogoURL, p.BackgroundURL, p.IconMD5, p.LogoMD5, p.BackgroundMD5,
		p.ModrinthProjectID, p.ModrinthProjectName, p.ModrinthVisibility, p.ID)
	return err
}

func (s *PostgresStore) DeletePack(id int, ownerID string) error {
	_, err := s.db.Exec(`DELETE FROM packs WHERE id=$1 AND owner_id=$2`, id, ownerID)
	return err
}

func (s *PostgresStore) GetPack(id int) (*models.Pack, error) {
	row := s.db.QueryRow(`SELECT `+packCols+` FROM packs WHERE id=$1`, id)
	p, err := scanPack(row)
	if err == sql.ErrNoRows {
		return nil, errPackNotFound
	}
	return p, err
}

func (s *PostgresStore) ListPacksByOwner(ownerID string) ([]models.Pack, error) {
	rows, err := s.db.Query(`SELECT `+packCols+` FROM packs WHERE owner_id=$1 ORDER BY updated_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Pack{}
	for rows.Next() {
		p, err := scanPack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

const buildCols = `id, pack_id, version_string, minecraft, loader, loader_version,
	min_java, min_memory, changelog, channel, frozen, solder_published, solder_private,
	modrinth_published, modrinth_version_id, mrpack_storage_key, mrpack_sha256, created_at, published_at`

func scanBuild(row interface{ Scan(...interface{}) error }) (*models.PackBuild, error) {
	var b models.PackBuild
	var publishedAt sql.NullTime
	if err := row.Scan(&b.ID, &b.PackID, &b.VersionString, &b.Minecraft, &b.Loader, &b.LoaderVersion,
		&b.MinJava, &b.MinMemory, &b.Changelog, &b.Channel, &b.Frozen, &b.SolderPublished, &b.SolderPrivate,
		&b.ModrinthPublished, &b.ModrinthVersionID, &b.MrpackStorageKey, &b.MrpackSHA256, &b.CreatedAt, &publishedAt); err != nil {
		return nil, err
	}
	if publishedAt.Valid {
		t := publishedAt.Time
		b.PublishedAt = &t
	}
	return &b, nil
}

func (s *PostgresStore) CreatePackBuild(b *models.PackBuild) (int, error) {
	ch := b.Channel
	if ch == "" {
		ch = models.ChannelDraft
	}
	var id int
	err := s.db.QueryRow(`INSERT INTO pack_builds
		(pack_id, version_string, minecraft, loader, loader_version, min_java, min_memory, changelog, channel)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		b.PackID, b.VersionString, b.Minecraft, b.Loader, b.LoaderVersion, b.MinJava, b.MinMemory, b.Changelog, ch,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) UpdatePackBuild(b *models.PackBuild) error {
	var publishedAt interface{}
	if b.PublishedAt != nil {
		publishedAt = *b.PublishedAt
	}
	_, err := s.db.Exec(`UPDATE pack_builds SET
		version_string=$1, minecraft=$2, loader=$3, loader_version=$4, min_java=$5, min_memory=$6,
		changelog=$7, channel=$8, frozen=$9, solder_published=$10, solder_private=$11,
		modrinth_published=$12, modrinth_version_id=$13, mrpack_storage_key=$14, mrpack_sha256=$15, published_at=$16
		WHERE id=$17`,
		b.VersionString, b.Minecraft, b.Loader, b.LoaderVersion, b.MinJava, b.MinMemory,
		b.Changelog, b.Channel, b.Frozen, b.SolderPublished, b.SolderPrivate,
		b.ModrinthPublished, b.ModrinthVersionID, b.MrpackStorageKey, b.MrpackSHA256, publishedAt, b.ID)
	return err
}

func (s *PostgresStore) DeletePackBuild(id, packID int) error {
	_, err := s.db.Exec(`DELETE FROM pack_builds WHERE id=$1 AND pack_id=$2`, id, packID)
	return err
}

func (s *PostgresStore) GetPackBuild(id int) (*models.PackBuild, error) {
	row := s.db.QueryRow(`SELECT `+buildCols+` FROM pack_builds WHERE id=$1`, id)
	b, err := scanBuild(row)
	if err == sql.ErrNoRows {
		return nil, errPackNotFound
	}
	return b, err
}

func (s *PostgresStore) ListPackBuilds(packID int) ([]models.PackBuild, error) {
	rows, err := s.db.Query(`SELECT `+buildCols+` FROM pack_builds WHERE pack_id=$1 ORDER BY created_at DESC`, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.PackBuild{}
	for rows.Next() {
		b, err := scanBuild(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// GetPackBySolderSlug resolves a pack by its public Solder slug (index-supported
// by packs_solder_slug_uniq). Returns (nil, nil) when no pack has that slug.
func (s *PostgresStore) GetPackBySolderSlug(slug string) (*models.Pack, error) {
	row := s.db.QueryRow(`SELECT `+packCols+` FROM packs WHERE solder_slug = $1`, slug)
	p, err := scanPack(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// GetPackBuildByVersion resolves one build inside a pack by its version string
// (unique per pack via pack_builds_pack_version_uniq). (nil, nil) when absent.
func (s *PostgresStore) GetPackBuildByVersion(packID int, versionString string) (*models.PackBuild, error) {
	row := s.db.QueryRow(`SELECT `+buildCols+` FROM pack_builds
		WHERE pack_id = $1 AND version_string = $2`, packID, versionString)
	b, err := scanBuild(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return b, err
}

// ListSolderPublishedBuilds returns the pack's Solder-published builds, newest first.
func (s *PostgresStore) ListSolderPublishedBuilds(packID int) ([]models.PackBuild, error) {
	rows, err := s.db.Query(`SELECT `+buildCols+` FROM pack_builds
		WHERE pack_id = $1 AND solder_published = true ORDER BY created_at DESC`, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.PackBuild
	for rows.Next() {
		b, err := scanBuild(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// ListPublicSolderPacks returns every pack that has a Solder slug and is neither
// private nor hidden, alphabetically by internal name (the public Solder listing).
func (s *PostgresStore) ListPublicSolderPacks() ([]models.Pack, error) {
	rows, err := s.db.Query(`SELECT `+packCols+` FROM packs
		WHERE solder_slug <> '' AND private = false AND hidden = false
		ORDER BY internal_name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Pack
	for rows.Next() {
		p, err := scanPack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}
