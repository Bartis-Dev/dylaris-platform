package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// PortManager handles host-port allocation for servers when GATEWAY_ENABLED=false.
// Ports are persisted in Redis under dylaris:node:{nodeID}:port:{serverUUID} so
// allocations survive node restarts and are visible to Core.
type PortManager struct {
	rdb        *redis.Client
	nodeID     string
	rangeStart int
	rangeEnd   int

	mu        sync.Mutex
	usedPorts map[int]string // port → serverUUID (in-RAM index for fast allocation)
}

func NewPortManager(rdb *redis.Client, nodeID string, rangeStart, rangeEnd int) *PortManager {
	pm := &PortManager{
		rdb:        rdb,
		nodeID:     nodeID,
		rangeStart: rangeStart,
		rangeEnd:   rangeEnd,
		usedPorts:  make(map[int]string),
	}
	pm.loadFromRedis()
	return pm
}

// loadFromRedis reads existing port assignments from Redis into the in-RAM index.
func (pm *PortManager) loadFromRedis() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var cursor uint64
	pattern := fmt.Sprintf("dylaris:node:%s:port:*", pm.nodeID)
	for {
		keys, next, err := pm.rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			log.Printf("PortManager: Redis scan error: %v", err)
			break
		}
		for _, key := range keys {
			portStr, err := pm.rdb.Get(ctx, key).Result()
			if err != nil {
				continue
			}
			port, err := strconv.Atoi(portStr)
			if err != nil {
				continue
			}
			// Extract serverUUID from key suffix
			suffix := key[len(fmt.Sprintf("dylaris:node:%s:port:", pm.nodeID)):]
			pm.usedPorts[port] = suffix
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	log.Printf("PortManager: loaded %d existing port allocations (range %d-%d)", len(pm.usedPorts), pm.rangeStart, pm.rangeEnd)
}

// AllocatePort finds the next free port in the configured range and reserves it.
// Returns the port number or an error if the range is exhausted.
func (pm *PortManager) AllocatePort(serverUUID string) (int, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Check if already allocated for this server
	for port, uuid := range pm.usedPorts {
		if uuid == serverUUID {
			return port, nil
		}
	}

	// Find next free port
	for port := pm.rangeStart; port <= pm.rangeEnd; port++ {
		if _, used := pm.usedPorts[port]; !used {
			// Persist to Redis
			key := fmt.Sprintf("dylaris:node:%s:port:%s", pm.nodeID, serverUUID)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := pm.rdb.Set(ctx, key, strconv.Itoa(port), 0).Err(); err != nil {
				return 0, fmt.Errorf("failed to persist port allocation: %w", err)
			}
			pm.usedPorts[port] = serverUUID
			log.Printf("PortManager: allocated port %d for server %s", port, serverUUID)
			return port, nil
		}
	}

	return 0, fmt.Errorf("port range %d-%d exhausted", pm.rangeStart, pm.rangeEnd)
}

// ReleasePort frees the port assigned to a server.
func (pm *PortManager) ReleasePort(serverUUID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for port, uuid := range pm.usedPorts {
		if uuid == serverUUID {
			key := fmt.Sprintf("dylaris:node:%s:port:%s", pm.nodeID, serverUUID)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			pm.rdb.Del(ctx, key)
			delete(pm.usedPorts, port)
			log.Printf("PortManager: released port %d (server %s)", port, serverUUID)
			return
		}
	}
}

// GetPort returns the port assigned to a server, or 0 if none.
func (pm *PortManager) GetPort(serverUUID string) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	for port, uuid := range pm.usedPorts {
		if uuid == serverUUID {
			return port
		}
	}
	return 0
}
