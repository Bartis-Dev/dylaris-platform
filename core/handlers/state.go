package handlers

import (
	nodegrpc "dylaris-core/grpc"
	"dylaris-core/services"
	"dylaris-core/store"

	"github.com/redis/go-redis/v9"
)

// AppState holds the global connections for all handlers
type AppState struct {
	Store            store.Store
	Redis            *redis.Client
	Queue            *services.QueueService
	GRPCRegistry     *nodegrpc.Registry
	Gateway          services.GatewayProvider
	RoutingMigration *services.RoutingMigrationService
	// FrontendURL is the panel base URL — used by mailers to build absolute
	// links into the UI (verify-email, password-reset, ticket replies, etc).
	FrontendURL string

	// ExternalTicketDBURL (Phase 5) carries the operator-configured
	// connection string for the external ticket DB. Empty when not
	// configured; the migration handler reads this to drive the UI.
	ExternalTicketDBURL string
}
