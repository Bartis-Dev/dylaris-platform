package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	APIPort     string
	FrontendURL string
	JWTSecret   string

	// Cluster
	ClusterSecret string
	CoreID        string
	GRPCPort      int
	// GRPCTLSEnabled turns on server-authenticated TLS + fingerprint pinning on
	// the node<->core NodeService channel. Off (default) = plaintext, exactly as
	// before. Must be flipped together with every node's GRPC_TLS_ENABLED.
	GRPCTLSEnabled bool
	// Region — which logical region this Core lives in. Stamped
	// into heartbeat + the system info endpoint so the panel can show a
	// "Connected to <region> Core" chip and downstream consumers can attribute
	// telemetry to a region. 'default' for single-region setups.
	Region string

	// Database
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string
	// DBType selects the storage backend for time-series data (server_stats):
	// "timescaledb" promotes it to a hypertable with native retention (best for
	// larger fleets); "postgres" keeps it a plain table with retention enforced
	// by the hourly sweep (fine for small/medium setups, no extension required).
	// Normalized to exactly "timescaledb" or "postgres".
	DBType string

	// Core Redis
	RedisAddr string
	RedisUser string
	RedisPass string
	RedisDB   int

	// Optional external ticket DB. When set, the migration UI surfaces this
	// as the target. Read by the migration/backup/restore handler — live
	// runtime queries always target the main DB.
	ExternalTicketDBURL string

	// DNS updater — leader-gated reconciler that points each region's edge
	// wildcard A record (e.g. *.eu.dylaris.com) at the live edge IPs in that
	// region via the DNS provider. Off unless DNS_UPDATER_ENABLED=true AND the
	// provider credentials are present. Credentials live ONLY in Core, never on
	// the edges.
	DNSUpdaterEnabled bool
	DNSProvider       string // "cloudflare"
	CFAPIToken        string
	CFZoneID          string

	// Store integration — the hosted dylaris.com storefront. When BOTH
	// STORE_URL and STORE_SHARED_KEY are set, StoreEnabled flips on and the
	// store-linking + demo-showcase surfaces appear (connect-store button,
	// demo account/servers). Self-hosters without these ENV vars get a clean
	// open-core build with no store or demo surface at all. STORE_SHARED_KEY is
	// the service-to-service trust between Core and dylaris.com (NOT a user
	// proof); it must match the same key configured on dylaris.com.
	StoreURL       string
	StoreSharedKey string
	StoreEnabled   bool

	// SuspendGrace defers the hard cutoff (stop servers + drop route-only link
	// ACLs) for this long after a tenant is marked "suspended", so a transient
	// billing/DB fault cannot instantly kick a paying customer. Env
	// BILLING_SUSPEND_GRACE (Go duration), default 48h; 0 = enforce on the next
	// hourly lifecycle tick (no grace).
	SuspendGrace time.Duration
}

func LoadConfig() (Config, error) {
	if _, err := os.Stat(".env"); os.IsNotExist(err) {
		log.Println("No .env file found. Using system environment variables.")
	} else {
		godotenv.Load()
	}

	redisDB, _ := strconv.Atoi(getEnv("REDIS_DB", "0"))
	grpcPort, _ := strconv.Atoi(getEnv("DYLARIS_GRPC_PORT", "25501"))

	coreID := getEnv("DYLARIS_CORE_ID", "")
	if coreID == "" {
		coreID, _ = os.Hostname()
	}

	dnsUpdaterEnabled, _ := strconv.ParseBool(getEnv("DNS_UPDATER_ENABLED", "false"))
	grpcTLSEnabled, _ := strconv.ParseBool(getEnv("GRPC_TLS_ENABLED", "false"))

	storeURL := strings.TrimSpace(getEnv("STORE_URL", ""))
	storeSharedKey := getSecret("STORE_SHARED_KEY", "")

	// BILLING_SUSPEND_GRACE is the only time.Duration env. config.go otherwise has
	// no duration env, so this follows the surrounding int-parse style (parse, keep
	// the default on empty) plus getSecret's log-on-fallback: a bad value keeps the
	// 48h default instead of silently yielding 0, which would disable the grace.
	suspendGrace := 48 * time.Hour
	if v := strings.TrimSpace(getEnv("BILLING_SUSPEND_GRACE", "")); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			suspendGrace = d
		} else {
			log.Printf("config: invalid BILLING_SUSPEND_GRACE %q: %v; using default %s", v, err, suspendGrace)
		}
	}

	cfg := Config{
		APIPort:       getEnv("API_PORT", "25500"),
		FrontendURL:   getEnv("FRONTEND_URL", "http://localhost:25510"),
		JWTSecret:     getSecret("JWT_SECRET", "change-this-secret"),
		ClusterSecret: getSecret("CLUSTER_SECRET", "dylaris-cluster-secret"),
		CoreID:         coreID,
		GRPCPort:       grpcPort,
		GRPCTLSEnabled: grpcTLSEnabled,
		Region:         getEnv("DYLARIS_REGION", "default"),

		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getSecret("DB_PASSWORD", ""),
		DBName:     getEnv("DB_NAME", "dylaris"),
		// Defaults to disable to preserve existing internal-Docker setups; set
		// DB_SSLMODE=require (or verify-full) when Postgres is remote.
		DBSSLMode: getEnv("DB_SSLMODE", "disable"),
		// Defaults to timescaledb (the bundled image). Set DB_TYPE=postgres to
		// run on plain PostgreSQL with no TimescaleDB extension.
		DBType: NormalizeDBType(getEnv("DB_TYPE", "timescaledb")),

		RedisAddr: getEnv("REDIS_ADDR", "localhost:6379"),
		RedisUser: getEnv("REDIS_USER", ""),
		RedisPass: getSecret("REDIS_PASSWORD", ""),
		RedisDB:   redisDB,

		ExternalTicketDBURL: getEnv("EXTERNAL_TICKET_DB_URL", ""),

		DNSUpdaterEnabled: dnsUpdaterEnabled,
		DNSProvider:       getEnv("DNS_PROVIDER", "cloudflare"),
		CFAPIToken:        getSecret("CF_API_TOKEN", ""),
		CFZoneID:          getEnv("CF_ZONE_ID", ""),

		StoreURL:       storeURL,
		StoreSharedKey: storeSharedKey,
		StoreEnabled:   storeURL != "" && storeSharedKey != "",

		SuspendGrace: suspendGrace,
	}

	// Refuse to boot with a predictable signing key. A default/empty JWT_SECRET
	// makes every session token forgeable; a default CLUSTER_SECRET also exposes
	// the derived Warp leader key and inter-service auth.
	if cfg.JWTSecret == "" || cfg.JWTSecret == "change-this-secret" {
		return cfg, fmt.Errorf("JWT_SECRET is unset or still the default placeholder — set a strong random value")
	}
	if cfg.ClusterSecret == "" || cfg.ClusterSecret == "dylaris-cluster-secret" {
		return cfg, fmt.Errorf("CLUSTER_SECRET is unset or still the default placeholder — set a strong random value")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// NormalizeDBType maps the various spellings operators might use to the two
// canonical values: "timescaledb" or "postgres". Anything timescale-ish (incl.
// the empty string falling through the default) resolves to "timescaledb"; any
// plain-postgres spelling resolves to "postgres". Unknown values default to
// "postgres" (the safer, extension-free backend).
func NormalizeDBType(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "timescaledb", "timescale", "ts":
		return "timescaledb"
	case "postgres", "postgresql", "pg", "plain":
		return "postgres"
	default:
		return "postgres"
	}
}

// UsesTimescale reports whether the given (already-normalized or raw) DB type
// should use TimescaleDB hypertables + native retention.
func UsesTimescale(dbType string) bool {
	return NormalizeDBType(dbType) == "timescaledb"
}

// getSecret resolves a secret with Docker/Portainer secrets support. Precedence:
//  1. contents of the file named by "<key>_FILE" (trimmed) - the docker-secret /
//     *_FILE convention, so the value never has to live in plain env;
//  2. the plain "<key>" env value;
//  3. the fallback.
// An unreadable or empty *_FILE logs and falls through to the env/fallback so a
// misconfigured secret path doesn't silently boot with a blank credential.
func getSecret(key, fallback string) string {
	if path, ok := os.LookupEnv(key + "_FILE"); ok && path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if v := strings.TrimSpace(string(data)); v != "" {
				return v
			}
			log.Printf("config: %s_FILE (%s) is empty; falling back to %s", key, path, key)
		} else {
			log.Printf("config: failed to read %s_FILE (%s): %v; falling back to %s", key, path, err, key)
		}
	}
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
