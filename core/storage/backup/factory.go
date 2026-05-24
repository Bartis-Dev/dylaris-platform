package backup

import (
	"context"
	"fmt"

	nodegrpc "dylaris-core/grpc"
	"dylaris-core/models"
)

// Deps carries the runtime dependencies the `node-local` provider needs to
// talk to a Node over the existing gRPC mesh. The s3 and shared providers
// ignore it, so existing call-sites can pass a zero-value Deps when they
// don't have these handles. Pass a populated Deps any time you call into a
// node-local storage (download, delete, list) so the operation can be
// dispatched to the right Node.
type Deps struct {
	Registry  *nodegrpc.Registry  // Active gRPC connections to Nodes
	NodeStore NodeStore           // Looks up a Node's ID by server UUID
}

// NodeStore is the narrow store surface the node-local provider depends on.
// Implemented by the existing store.Store interface — kept abstract here so
// the backup package doesn't take a hard dependency on the full store API.
type NodeStore interface {
	GetServerByUUID(uuid string) (*models.Server, error)
}

// Open returns a Storage implementation matching the given backup_storages
// row. The factory keeps the rest of the codebase decoupled from concrete
// providers.
//
// `deps` is used by the `node-local` provider to reach the right Node over
// the existing gRPC mesh. Pass a zero-value Deps for s3/shared (the
// providers ignore it) when the caller has no registry handy — e.g. a
// scheduler probe that only ever touches s3.
func Open(ctx context.Context, bs *models.BackupStorage, deps Deps) (Storage, error) {
	if bs == nil {
		return nil, fmt.Errorf("nil backup storage")
	}
	switch bs.Provider {
	case "local", "shared":
		// "shared" is the new UI label for what used to be "local" — the
		// provider DB column stays "local" for back-compat; either string
		// resolves to the same implementation.
		return NewLocal(bs.Config)
	case "s3":
		return NewS3(ctx, bs.Config)
	case "node-local":
		if deps.Registry == nil || deps.NodeStore == nil {
			return nil, fmt.Errorf("node-local storage requires gRPC registry and node-store handles")
		}
		return NewNodeLocal(deps.Registry, deps.NodeStore), nil
	default:
		return nil, fmt.Errorf("unknown backup provider: %s", bs.Provider)
	}
}
