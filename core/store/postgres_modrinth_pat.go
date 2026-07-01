package store

import (
	"database/sql"
	"dylaris-core/models"
)

// Modrinth PAT storage. One row per user; ciphertext is hex(aes-gcm(plaintext)).
// Preserved from the retired Phase 14 modpack builder because Modrinth
// publishing (a later phase of the unified pack model) reuses these methods.

func (s *PostgresStore) SetModrinthPAT(userID string, ciphertext, modrinthUsername string) error {
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

func (s *PostgresStore) GetModrinthPAT(userID string) (*models.ModrinthPAT, error) {
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

func (s *PostgresStore) ClearModrinthPAT(userID string) error {
	_, err := s.db.Exec(`DELETE FROM modrinth_pats WHERE user_id=$1`, userID)
	return err
}
