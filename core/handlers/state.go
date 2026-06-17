package handlers

import (
	nodegrpc "dylaris-core/grpc"
	"dylaris-core/services"
	"dylaris-core/store"

	"github.com/redis/go-redis/v9"
)

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

	// ExternalTicketDBURL carries the operator-configured connection
	// string for the external ticket DB. Empty when not configured;
	// the migration handler reads this to drive the UI.
	ExternalTicketDBURL string

	// Events publishes platform-wide config-change events into Redis
	// Pub/Sub so connected panels refresh without polling. Nil-safe:
	// publishers handle a nil pointer / nil Redis gracefully.
	Events *services.SystemEventsPublisher

	// FeatureFlags is the cached platform feature toggle reader.
	// Read by the modpack route gates; flipped via /admin/settings/modpacks.
	FeatureFlags *services.FeatureFlags

	// Migration is the leader-driven node-to-node migration orchestrator.
	// The manual-move endpoint only ENQUEUES onto it; the elected Core executes.
	Migration *services.MigrationOrchestrator

	// DBMigration drives the in-panel cross-database migration (copy the whole
	// DB onto a new target under maintenance mode). Shared job state lives in
	// Redis so every admin sees the same live status.
	DBMigration *services.DBMigrationService

	// CPUPinning reads node CPU topology (published to Redis by each node) and
	// computes auto cpusets for per-server CPU pinning.
	CPUPinning *services.CPUPinningService

	// DBType is the normalized database backend ("timescaledb" or "postgres"),
	// set from config at boot. The status page uses it to distinguish an
	// intended plain-postgres deployment from a misconfigured timescale one.
	DBType string
}
