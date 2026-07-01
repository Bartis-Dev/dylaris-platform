package store

import (
	"database/sql"

	"dylaris-core/models"
)

const loaderCols = `id, minecraft, loader, loader_version, client_storage_key, md5,
	filesize, build_status, build_error, built_at, created_at, updated_at`

// GetLoader returns the cached loader for the triple, or (nil, nil) if none.
func (s *PostgresStore) GetLoader(minecraft, loader, loaderVersion string) (*models.Loader, error) {
	row := s.db.QueryRow(`SELECT `+loaderCols+` FROM loaders
		WHERE minecraft=$1 AND loader=$2 AND loader_version=$3`, minecraft, loader, loaderVersion)
	l, err := scanLoader(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return l, err
}

// UpsertLoader inserts or updates the loader row for its unique triple.
func (s *PostgresStore) UpsertLoader(l *models.Loader) (int, error) {
	var id int
	err := s.db.QueryRow(`
		INSERT INTO loaders (minecraft, loader, loader_version, client_storage_key, md5,
			filesize, build_status, build_error, built_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW())
		ON CONFLICT (minecraft, loader, loader_version) DO UPDATE SET
			client_storage_key=EXCLUDED.client_storage_key, md5=EXCLUDED.md5,
			filesize=EXCLUDED.filesize, build_status=EXCLUDED.build_status,
			build_error=EXCLUDED.build_error, built_at=EXCLUDED.built_at, updated_at=NOW()
		RETURNING id`,
		l.Minecraft, l.Loader, l.LoaderVersion, l.ClientStorageKey, l.MD5,
		l.Filesize, l.BuildStatus, l.BuildError, l.BuiltAt).Scan(&id)
	return id, err
}

// UpdateLoaderStatus flips just the status + error (used to mark 'pending'/'failed'
// without touching a previously-built artifact's key/md5).
func (s *PostgresStore) UpdateLoaderStatus(minecraft, loader, loaderVersion, status, buildError string) error {
	_, err := s.db.Exec(`UPDATE loaders SET build_status=$4, build_error=$5, updated_at=NOW()
		WHERE minecraft=$1 AND loader=$2 AND loader_version=$3`,
		minecraft, loader, loaderVersion, status, buildError)
	return err
}

func scanLoader(row interface{ Scan(...interface{}) error }) (*models.Loader, error) {
	var l models.Loader
	var builtAt sql.NullTime
	if err := row.Scan(&l.ID, &l.Minecraft, &l.Loader, &l.LoaderVersion, &l.ClientStorageKey,
		&l.MD5, &l.Filesize, &l.BuildStatus, &l.BuildError, &builtAt, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return nil, err
	}
	if builtAt.Valid {
		t := builtAt.Time
		l.BuiltAt = &t
	}
	return &l, nil
}
