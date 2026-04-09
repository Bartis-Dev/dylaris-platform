package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
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
	portMode   string // "sequential" (default) or "random"

	mu        sync.Mutex
	usedPorts map[int]string // port → serverUUID (in-RAM index for fast allocation)
}

func NewPortManager(rdb *redis.Client, nodeID string, rangeStart, rangeEnd int, portMode string) *PortManager {
	if portMode == "" {
		portMode = "sequential"
	}
	pm := &PortManager{
		rdb:        rdb,
		nodeID:     nodeID,
		rangeStart: rangeStart,
		rangeEnd:   rangeEnd,
		portMode:   portMode,
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

	// Build candidate list based on mode
	rangeSize := pm.rangeEnd - pm.rangeStart + 1
	candidates := make([]int, rangeSize)
	for i := range candidates {
		candidates[i] = pm.rangeStart + i
	}
	if pm.portMode == "random" {
		rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	}

	for _, port := range candidates {
		if _, used := pm.usedPorts[port]; !used {
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

// SetPort forces a specific port for a server, releasing any previous allocation.
// Returns an error if the port is already used by another server.
func (pm *PortManager) SetPort(serverUUID string, port int) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Release existing allocation for this server if different
	for p, uuid := range pm.usedPorts {
		if uuid == serverUUID && p != port {
			key := fmt.Sprintf("dylaris:node:%s:port:%s", pm.nodeID, serverUUID)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			pm.rdb.Del(ctx, key)
			cancel()
			delete(pm.usedPorts, p)
			break
		}
	}

	// Check if new port is taken by another server
	if existingUUID, ok := pm.usedPorts[port]; ok && existingUUID != serverUUID {
		return fmt.Errorf("port %d is already allocated to server %s", port, existingUUID)
	}

	key := fmt.Sprintf("dylaris:node:%s:port:%s", pm.nodeID, serverUUID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := pm.rdb.Set(ctx, key, strconv.Itoa(port), 0).Err(); err != nil {
		return fmt.Errorf("failed to persist port: %w", err)
	}
	pm.usedPorts[port] = serverUUID
	log.Printf("PortManager: set port %d for server %s", port, serverUUID)
	return nil
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
