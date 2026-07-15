package handlers

import (
	"time"

	"dylaris-core/authz"
	nodegrpc "dylaris-core/grpc"
	"dylaris-core/services"
	"dylaris-core/services/redisacl"
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

	// Authz is the capability resolver + RequireCap middleware factory (the
	// unified permission-system chokepoint). Constructed at boot; NOT yet wired
	// into the ~400 routes (phase 2). Only GET /api/authz/catalog exists today.
	Authz *authz.Resolver

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

	// Billing drives the BYON non-payment lifecycle (past_due grace -> suspended
	// -> retention cleanup). Handlers call EnterPastDue/Reactivate/Suspend; the
	// leader-gated worker progresses expired grace windows. Nil-safe in callers.
	Billing *services.BillingLifecycleService

	// DBType is the normalized database backend ("timescaledb" or "postgres"),
	// set from config at boot. The status page uses it to distinguish an
	// intended plain-postgres deployment from a misconfigured timescale one.
	DBType string

	// StoreEnabled mirrors config.StoreEnabled: true only when BOTH StoreURL
	// and StoreSharedKey are set. Gates every store-coupled surface — the
	// connect-store button, store-linking endpoints, and the demo account/
	// servers feature — so a self-hosted open-core build with no store ENV
	// exposes none of it.
	StoreEnabled bool
	// StoreURL is the dylaris.com storefront base URL (e.g. https://dylaris.com).
	// Used to build the connect-store redirect and to read link status.
	StoreURL string
	// StoreSharedKey is the service-to-service trust shared with dylaris.com. It
	// authenticates store->core calls (link/verify, verify-user) and core->store
	// calls (link-status). NOT a user proof.
	StoreSharedKey string

	// GRPCTLSFingerprint is the cluster-wide Core control-channel cert fingerprint
	// (public pinning material). Surfaced to BYON operators so a secret-free node
	// can pin it via GRPC_TLS_FINGERPRINT. Empty string if derivation failed.
	GRPCTLSFingerprint string
	// GRPCTLSEnabled mirrors config.GRPCTLSEnabled: whether the node<->Core gRPC
	// control channel actually enforces TLS + fingerprint pinning. Gates whether
	// GRPCTLSFingerprint is worth handing to an operator (see node_enroll.go).
	GRPCTLSEnabled bool

	// ACLProvisioner provisions the route-only links' scoped Redis ACL users on
	// Core's own Redis client (link-boot + revoke + billing suspend/reactivate).
	ACLProvisioner *redisacl.Provisioner
	// ClusterSecret is the deployment-wide secret used to derive per-link tunnel
	// tokens and Redis passwords. Never sent to a tenant.
	ClusterSecret string

	// AdminSecret mirrors config.AdminSecret: the RAM-only break-glass secret
	// that gates /setup admin creation. Empty = feature disabled. Never
	// persisted, never logged.
	AdminSecret string

	// SuspendGrace mirrors config.SuspendGrace: how long after a tenant is marked
	// "suspended" the LinkBoot gate keeps letting their route-only links boot. The
	// gate uses it via linkHardSuspended; MUST match the reconciler + enforcement
	// cutoff so a link is refused exactly when its ACL is actually gone.
	SuspendGrace time.Duration

	// TabProxyIsolationActive mirrors config.TabProxyIsolationActive (spec B5):
	// true iff TAB_PROXY_ORIGIN is set and host-matches the panel. The
	// standalone share data plane (Public) and its mint (MintPublicProxyAuth)
	// refuse unless this is true, because a same-origin public share is the C1
	// cross-tenant token-theft vector. InDashboard is NOT gated by it.
	TabProxyIsolationActive bool
}

// AdminSecretConfigured reports whether the break-glass ADMIN_SECRET is set.
func (s *AppState) AdminSecretConfigured() bool { return s.AdminSecret != "" }
