package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

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

	// Core Redis
	RedisAddr string
	RedisUser string
	RedisPass string
	RedisDB   int

	// Optional external ticket DB. When set, the migration UI surfaces this
	// as the target. Read by the migration/backup/restore handler — live
	// runtime queries always target the main DB.
	ExternalTicketDBURL string
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

	cfg := Config{
		APIPort:       getEnv("API_PORT", "25500"),
		FrontendURL:   getEnv("FRONTEND_URL", "http://localhost:25510"),
		JWTSecret:     getSecret("JWT_SECRET", "change-this-secret"),
		ClusterSecret: getSecret("CLUSTER_SECRET", "dylaris-cluster-secret"),
		CoreID:        coreID,
		GRPCPort:      grpcPort,
		Region:        getEnv("DYLARIS_REGION", "default"),

		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getSecret("DB_PASSWORD", ""),
		DBName:     getEnv("DB_NAME", "dylaris"),
		// Defaults to disable to preserve existing internal-Docker setups; set
		// DB_SSLMODE=require (or verify-full) when Postgres is remote.
		DBSSLMode: getEnv("DB_SSLMODE", "disable"),

		RedisAddr: getEnv("REDIS_ADDR", "localhost:6379"),
		RedisUser: getEnv("REDIS_USER", ""),
		RedisPass: getSecret("REDIS_PASSWORD", ""),
		RedisDB:   redisDB,

		ExternalTicketDBURL: getEnv("EXTERNAL_TICKET_DB_URL", ""),
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
