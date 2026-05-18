package backup

import (
	"context"
	"fmt"

	"dylaris-core/models"
)

// Open returns a Storage implementation matching the given backup_storages
// row. The factory keeps the rest of the codebase decoupled from concrete
// providers.
func Open(ctx context.Context, bs *models.BackupStorage) (Storage, error) {
	if bs == nil {
		return nil, fmt.Errorf("nil backup storage")
	}
	switch bs.Provider {
	case "local":
		return NewLocal(bs.Config)
	case "s3":
		return NewS3(ctx, bs.Config)
	default:
		return nil, fmt.Errorf("unknown backup provider: %s", bs.Provider)
	}
}
