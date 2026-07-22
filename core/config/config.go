package config

import (
	"fmt"
	"log"
	"net"
	"net/url"
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

	// AdminSecret is the optional RAM-only break-glass secret. When non-empty,
	// creating an admin via /setup requires this exact value in every mode
	// (fresh_install, lost_admin, complete). Empty disables the feature. Read
	// via ADMIN_SECRET (or ADMIN_SECRET_FILE). Never persisted, never logged.
	AdminSecret string

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

	// TabProxyPort, if set, makes Core bind a SECOND HTTP listener on this port
	// serving ONLY the WS5 tab-proxy data plane (spec B5). The browser reaches
	// it as a distinct ORIGIN (same host, different port) so a proxied
	// container's JS runs on that origin and can never read the panel token from
	// the panel origin's localStorage. Empty = the second listener is not started.
	TabProxyPort string
	// TabProxyOrigin is the browser-facing absolute base URL of that isolated
	// origin (what the panel builds proxied-iframe srcs against; behind a front
	// proxy it maps to TabProxyPort). Normalized scheme://host[:port], no
	// trailing slash; "" whenever origin-isolation is inactive.
	TabProxyOrigin string
	// TabProxyIsolationActive is true iff TAB_PROXY_ORIGIN is set AND host-matches
	// FRONTEND_URL. Gates the public-share mint/serve refusal (Core) and drives
	// the panel's absolute-vs-relative iframe src choice.
	TabProxyIsolationActive bool

	// UpdatesFeedURLPlatform / UpdatesFeedURLGateway are the PUBLIC raw URLs of
	// the append-only JSONL update feeds the admin "what's new" bell diffs against
	// the build-baked baseline. Platform defaults to its own public-repo raw feed;
	// gateway is empty until the private gateway feed is cross-pushed into the
	// public platform repo (owner infra). Both fail open when unset or unreachable
	// (the bell simply shows no updates).
	UpdatesFeedURLPlatform string
	UpdatesFeedURLGateway  string

	// TrustedProxyCIDRs are the networks a reverse proxy in front of Core may
	// occupy. It decides whether an X-Forwarded-For header is believed: the
	// client IP is taken as the first hop, walking the header right-to-left,
	// that is NOT inside one of these networks. A forged XFF entry from a real
	// client sits to the LEFT of the address the trusted proxy appended, so it
	// is never reached - which is what stops a client from spoofing the IP the
	// rate limiters and the audit log key on.
	//
	// Parsed from TRUSTED_PROXY_CIDRS. Unset or empty defaults to the private
	// ranges (the shipped reference proxy sits on the private Docker network),
	// so per-client limiting works out of the box while a public attacker's
	// forged header is ignored. The literal value "none" trusts nothing and
	// makes Core ignore XFF entirely - correct when Core is exposed directly.
	TrustedProxyCIDRs []*net.IPNet
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

	// FRONTEND_URL is read once here so both the Config field and the
	// TAB_PROXY_ORIGIN host-match validation below use the exact same value.
	frontendURL := getEnv("FRONTEND_URL", "http://localhost:25510")
	tabProxyPort := strings.TrimSpace(getEnv("TAB_PROXY_PORT", ""))
	tabProxyOrigin, tabProxyIsolationActive := resolveTabProxyOrigin(getEnv("TAB_PROXY_ORIGIN", ""), frontendURL)

	cfg := Config{
		APIPort:       getEnv("API_PORT", "25500"),
		FrontendURL:   frontendURL,
		JWTSecret:     getSecret("JWT_SECRET", "change-this-secret"),
		AdminSecret:   getSecret("ADMIN_SECRET", ""),
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

		TabProxyPort:            tabProxyPort,
		TabProxyOrigin:          tabProxyOrigin,
		TabProxyIsolationActive: tabProxyIsolationActive,

		// Platform defaults to its own public repo's raw feed (works once the repo
		// is public + the feed is populated); gateway stays empty until the feed is
		// cross-pushed there. Override both via env for a self-hosted mirror.
		UpdatesFeedURLPlatform: getEnv("UPDATES_FEED_URL_PLATFORM", "https://raw.githubusercontent.com/Bartis-Dev/dylaris-platform/main/core/updates/feed.jsonl"),
		UpdatesFeedURLGateway:  getEnv("UPDATES_FEED_URL_GATEWAY", ""),

		TrustedProxyCIDRs: ParseTrustedProxyCIDRs(getEnv("TRUSTED_PROXY_CIDRS", "")),
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

	// ADMIN_SECRET is OPTIONAL (empty = disabled), unlike JWT/Cluster. When set
	// it must be strong enough to gate admin creation; a bad value fails the boot
	// (main.go log.Fatals on any LoadConfig error).
	if err := validateAdminSecret(cfg.AdminSecret); err != nil {
		return cfg, err
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

// validateAdminSecret checks the optional break-glass ADMIN_SECRET. Empty is
// valid and disables the feature. A configured secret must be at least 16
// characters so a trivially-guessable value cannot gate admin creation. Pure so
// it can be unit-tested without booting LoadConfig (which main.go log.Fatals on).
func validateAdminSecret(s string) error {
	if s == "" {
		return nil
	}
	if len(s) < 16 {
		return fmt.Errorf("ADMIN_SECRET must be at least 16 characters when set (got %d); unset it to disable break-glass admin creation", len(s))
	}
	return nil
}

// effectivePort returns the URL's explicit port, or the scheme default (443 for
// https, 80 for http) when none is given. This normalizes so that e.g.
// "https://h" and "https://h:443" compare equal - both address the SAME origin.
func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

// resolveTabProxyOrigin decides whether origin-isolation for the WS5 custom-tab
// reverse proxy (spec B5) is active and returns the normalized browser-facing
// proxy origin the panel builds iframe srcs against.
//
// Origin-isolation is active only when TAB_PROXY_ORIGIN is set, parses as an
// absolute http(s) URL, shares the panel's (FRONTEND_URL) SCHEME and HOST, and
// resolves to a DIFFERENT effective PORT than the panel. The browser-facing
// proxy origin must differ from the panel ONLY in port: the dyl_tabproxy ticket
// cookie is host-only, so a different host would drop the cookie and break proxy
// auth; and a proxy origin equal to the panel origin is not isolation at all - a
// proxied container's JS would run on the panel origin and could read the panel
// token from localStorage, silently reopening the very vector B5 closes. Rather
// than silently break auth OR silently reopen the token-theft vector, any
// mismatch (scheme, host, or an identical effective port) logs a clear warning
// and disables isolation, falling back to today's same-origin behavior. The
// returned origin is scheme://host[:port] with no trailing slash; it is ""
// whenever isolation is inactive.
func resolveTabProxyOrigin(rawOrigin, frontendURL string) (origin string, active bool) {
	rawOrigin = strings.TrimSpace(rawOrigin)
	if rawOrigin == "" {
		return "", false
	}
	u, err := url.Parse(rawOrigin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		log.Printf("config: TAB_PROXY_ORIGIN %q is not a valid absolute http(s) origin; origin-isolation disabled (same-origin fallback)", rawOrigin)
		return "", false
	}
	fu, ferr := url.Parse(strings.TrimSpace(frontendURL))
	if ferr != nil || fu.Hostname() == "" {
		log.Printf("config: FRONTEND_URL %q is not parseable; cannot host-match TAB_PROXY_ORIGIN; origin-isolation disabled", frontendURL)
		return "", false
	}
	if !strings.EqualFold(u.Scheme, fu.Scheme) {
		log.Printf("config: TAB_PROXY_ORIGIN scheme %q != FRONTEND_URL scheme %q; a different scheme is a different origin the cookie cannot follow, so origin-isolation is disabled (same-origin fallback)", u.Scheme, fu.Scheme)
		return "", false
	}
	if !strings.EqualFold(u.Hostname(), fu.Hostname()) {
		log.Printf("config: TAB_PROXY_ORIGIN host %q != FRONTEND_URL host %q; the host-only dyl_tabproxy cookie cannot reach a different host, so origin-isolation is disabled (same-origin fallback)", u.Hostname(), fu.Hostname())
		return "", false
	}
	if effectivePort(u) == effectivePort(fu) {
		log.Printf("config: TAB_PROXY_ORIGIN %q resolves to the SAME origin as FRONTEND_URL %q (same scheme, host and effective port); origin isolation needs a DIFFERENT port or a proxied container's JS could read the panel token, so it is disabled (same-origin fallback)", rawOrigin, frontendURL)
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

// IsLocalOrigin reports whether a CORS Origin header points at the local machine
// or a private LAN address: localhost, 127.0.0.0/8, ::1, the RFC1918 ranges
// (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16) and IPv6 ULA (fc00::/7), on ANY
// port. Such origins are always allowed so a self-hoster reaches the panel over
// localhost or a LAN IP without configuring FRONTEND_URL. It never matches a
// public hostname - those must be named explicitly in FRONTEND_URL - so an empty
// configuration never implicitly trusts a public (vendor) origin. An unparseable
// or opaque origin (e.g. "null") returns false.
func IsLocalOrigin(origin string) bool {
	u, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}
