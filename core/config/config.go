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

	// --- Gateway (Hub via Redis Queue) ---
	GatewayEnabled   bool
	GatewayRedisAddr string
	GatewayRedisUser string
	GatewayRedisPass string
	GatewayRedisDB   int
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

		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getEnv("DB_PASSWORD", ""),
		DBName:     getEnv("DB_NAME", "dylaris"),

		RedisAddr: getEnv("REDIS_ADDR", "localhost:6379"),
		RedisUser: getEnv("REDIS_USER", ""),
		RedisPass: getEnv("REDIS_PASSWORD", ""),
		RedisDB:   redisDB,

	}

	// Gateway defaults to Core Redis unless overridden
	cfg.GatewayRedisAddr = getEnv("GATEWAY_REDIS_ADDR", cfg.RedisAddr)
	cfg.GatewayRedisUser = getEnv("GATEWAY_REDIS_USER", cfg.RedisUser)
	cfg.GatewayRedisPass = getEnv("GATEWAY_REDIS_PASS", cfg.RedisPass)
	if v := getEnv("GATEWAY_REDIS_DB", ""); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.GatewayRedisDB = i
		}
	} else {
		cfg.GatewayRedisDB = cfg.RedisDB
	}

	cfg.GatewayEnabled = getEnv("GATEWAY_ENABLED", "false") == "true"

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
