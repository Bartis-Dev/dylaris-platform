package handlers

import (
	nodegrpc "dylaris-core/grpc"
	"dylaris-core/services"
	"dylaris-core/store"

	"github.com/redis/go-redis/v9"
)

// AppState holds the global connections for all handlers
type AppState struct {
	Store        store.Store
	Redis        *redis.Client
	Queue        *services.QueueService
	GRPCRegistry *nodegrpc.Registry
	GatewayRedis *redis.Client
	Gateway      services.GatewayProvider
}
