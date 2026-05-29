package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

// CoreHeartbeat is written to Redis so Nodes can discover this Core instance.
// Region (Phase 6) is the operator-configured DYLARIS_REGION env. Nodes can
// use it for region-affinity decisions later; the panel uses it for the
// "Connected to <region> Core" chip.
type CoreHeartbeat struct {
	ID       string  `json:"id"`
	Region   string  `json:"region"`
	GRPCAddr string  `json:"grpc_addr"` // host:port for gRPC connections
	IPs      CoreIPs `json:"ips"`
}

type CoreIPs struct {
	Public  string   `json:"public"`
	Private []string `json:"private"`
}

type CoreHeartbeatService struct {
	redis    *redis.Client
	coreID   string
	region   string
	grpcPort int
}

func NewCoreHeartbeatService(r *redis.Client, coreID, region string, grpcPort int) *CoreHeartbeatService {
	if region == "" {
		region = "default"
	}
	return &CoreHeartbeatService{
		redis:    r,
		coreID:   coreID,
		region:   region,
		grpcPort: grpcPort,
	}
}

// Start begins writing heartbeats every 10 seconds.
func (s *CoreHeartbeatService) Start() {
	log.Printf("Core Heartbeat started (id=%s, grpc_port=%d)", s.coreID, s.grpcPort)

	s.writeHeartbeat()

	ticker := time.NewTicker(10 * time.Second)
	go func() {
		for range ticker.C {
			s.writeHeartbeat()
		}
	}()
}

func (s *CoreHeartbeatService) writeHeartbeat() {
	ctx := context.Background()

	privateIPs := getPrivateIPs()
	publicIP := getOutboundIP()

	// Build gRPC address: prefer first private IP, fallback to public
	host := publicIP
	if len(privateIPs) > 0 {
		host = privateIPs[0]
	}
	grpcAddr := fmt.Sprintf("%s:%d", host, s.grpcPort)

	hb := CoreHeartbeat{
		ID:       s.coreID,
		Region:   s.region,
		GRPCAddr: grpcAddr,
		IPs: CoreIPs{
			Public:  publicIP,
			Private: privateIPs,
		},
	}

	data, err := json.Marshal(hb)
	if err != nil {
		log.Printf("Core Heartbeat marshal error: %v", err)
		return
	}

	key := "dylaris:core:" + s.coreID
	if err := s.redis.Set(ctx, key, string(data), 30*time.Second).Err(); err != nil {
		log.Printf("Core Heartbeat Redis error: %v", err)
	}
}

// getPrivateIPs returns all private/internal IP addresses.
func getPrivateIPs() []string {
	var ips []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}
		if ip[0] == 10 || (ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31) || (ip[0] == 192 && ip[1] == 168) {
			ips = append(ips, ip.String())
		}
	}
	return ips
}

// getOutboundIP returns the preferred outbound IP (same approach as Node).
func getOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		name, _ := os.Hostname()
		return name
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
