package authz

import (
	"dylaris-core/models"
	"dylaris-core/store"
)

// Store is the slice of the persistence layer the resolver needs. Declaring it
// here (rather than importing the full store.Store) keeps authz decoupled and
// makes the resolver trivial to fake in tests. *store.PostgresStore satisfies
// this structurally, so NewResolver(pgStore) just works.
type Store interface {
	GetServerByID(id int) (*models.Server, error)
	GetServerByUUID(uuid string) (*models.Server, error)
	GetPanelRole(id int) (*store.PanelRole, error)
	GetServerRole(id int) (*store.ServerRole, error)
	GetUserPanelAuthz(userID string) (*int, store.CapOverrides, error)
	GetServerGrant(serverID int, userID string) (*store.ServerGrant, error)
	GetAccountGrant(ownerUserID, userID string) (*store.ServerGrant, error)
}
