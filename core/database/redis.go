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

