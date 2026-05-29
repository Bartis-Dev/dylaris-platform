package config

import (
	"log"
	"os"
	"strconv"

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
	// Region (Phase 6) — which logical region this Core lives in. Stamped
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

	// Core Redis
	RedisAddr string
	RedisUser string
	RedisPass string
	RedisDB   int

	// Optional external ticket DB (Phase 5). When set, the migration UI
	// surfaces this as the target. Today queries still route to the main
	// DB — a future polish phase wires the live read/write switch once
	// the cross-DB-user-JOIN story is settled.
	ExternalTicketDBURL string
}

func LoadConfig() (Config, error) {
	if _, err := os.Stat(".env"); os.IsNotExist(err) {
		log.Println("No .env file found. Using system environment variables.")
	} else {
		godotenv.Load()
	}

	redisDB, _ := strconv.Atoi(getEnv("REDIS_DB", "0"))
	grpcPort, _ := strconv.Atoi(getEnv("DYLARIS_GRPC_PORT", "25520"))

	coreID := getEnv("DYLARIS_CORE_ID", "")
	if coreID == "" {
		coreID, _ = os.Hostname()
	}

	cfg := Config{
		APIPort:       getEnv("API_PORT", "25500"),
		FrontendURL:   getEnv("FRONTEND_URL", "http://localhost:25510"),
		JWTSecret:     getEnv("JWT_SECRET", "change-this-secret"),
		ClusterSecret: getEnv("CLUSTER_SECRET", "dylaris-cluster-secret"),
		CoreID:        coreID,
		GRPCPort:      grpcPort,
		Region:        getEnv("DYLARIS_REGION", "default"),

		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getEnv("DB_PASSWORD", ""),
		DBName:     getEnv("DB_NAME", "dylaris"),

		RedisAddr: getEnv("REDIS_ADDR", "localhost:6379"),
		RedisUser: getEnv("REDIS_USER", ""),
		RedisPass: getEnv("REDIS_PASSWORD", ""),
		RedisDB:   redisDB,

		ExternalTicketDBURL: getEnv("EXTERNAL_TICKET_DB_URL", ""),
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
