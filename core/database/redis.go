package database

import (
	"context"
	"dylaris-core/config"
	"log"

	"github.com/redis/go-redis/v9"
)

func InitRedis(cfg config.Config) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Username: cfg.RedisUser,
		Password: cfg.RedisPass,
		DB:       cfg.RedisDB,
	})

	// Ping Test
	if err := client.Ping(context.Background()).Err(); err != nil {
		log.Printf("Failed to connect to Redis at %s: %v", cfg.RedisAddr, err)
		return nil, err
	}

	log.Printf("Successfully connected to Redis at %s (DB: %d)", cfg.RedisAddr, cfg.RedisDB)
	return client, nil
}

// InitGatewayRedis creates a Redis client for the Gateway subsystem.
// It may point to a different address or DB index than the core Redis.
func InitGatewayRedis(cfg config.Config) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.GatewayRedisAddr,
		Username: cfg.GatewayRedisUser,
		Password: cfg.GatewayRedisPass,
		DB:       cfg.GatewayRedisDB,
	})

	if err := client.Ping(context.Background()).Err(); err != nil {
		log.Printf("Failed to connect to Gateway Redis at %s: %v", cfg.GatewayRedisAddr, err)
		return nil, err
	}

	log.Printf("Gateway Redis connected at %s (DB: %d)", cfg.GatewayRedisAddr, cfg.GatewayRedisDB)
	return client, nil
}
