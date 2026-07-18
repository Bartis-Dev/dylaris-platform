package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"dylaris-core/models"
	"dylaris-core/pkg/leader"
)

// telemetryStore is the slice of the Store interface this service needs.
// Defined locally to keep the service narrowly testable and to dodge the
// full store.Store dependency surface — handlers and other services that
// own different DB shapes can mock this with three methods.
type telemetryStore interface {
	GetSetting(key string) (string, error)
	GetAllActiveServers() ([]models.Server, error)
	SumLatestPlayerCounts() (int, error)
}

// TelemetryHeartbeat posts anonymous platform stats to dylaris.dev every 10
// minutes for the public live-counter on the website. Sends:
//   - instanceId: SHA256(salt + CLUSTER_SECRET)[:16]. Stable per DEPLOYMENT,
//     not per Core: every Core of one cluster shares CLUSTER_SECRET, so a
//     load-balanced multi-Core setup counts as ONE self-hoster, and the id
//     survives restarts, redeploys and leader changes. No hostname or user
//     data ever leaves the box.
//   - type: platform | gateway (from routing_mode setting)
//   - servers / online / players counts (players unused by the counter today)
//   - version string for build correlation
//
// Default ENABLED. Opt out via Settings -> Features (telemetry_enabled = false)
// or the DYLARIS_TELEMETRY=false ENV (hard kill, ignores DB setting).
// Leader-gated so multi-Core deployments only post once; if leadership briefly
// overlaps during a lease handover the shared instanceId just dedupes.
type TelemetryHeartbeat struct {
	store       telemetryStore
	instanceID  string
	region      string
	coreVersion string
	leader      leader.Election
	httpClient  *http.Client
}

// TelemetryCoreVersion is the platform version string included in every
// heartbeat. Hardcoded for now — bump alongside platform releases.
const TelemetryCoreVersion = "0.17.0"

// telemetryInstanceSalt namespaces the instanceId hash so the value shared with
// the website can never be confused with any other CLUSTER_SECRET-derived hash,
// and gives us a version handle if the id scheme ever changes.
const telemetryInstanceSalt = "dylaris-telemetry-instance-v1"

// deriveInstanceID turns the deployment-wide CLUSTER_SECRET into a stable,
// opaque 16-hex-char id. Deterministic and identical across every Core that
// shares the secret, so one deployment maps to exactly one id. Truncated to
// 64 bits: collision-safe for a counter, and not reversible to the secret.
func deriveInstanceID(clusterSecret string) string {
	sum := sha256.Sum256([]byte(telemetryInstanceSalt + ":" + clusterSecret))
	return hex.EncodeToString(sum[:])[:16]
}

func NewTelemetryHeartbeat(store telemetryStore, clusterSecret, region string) *TelemetryHeartbeat {
	return &TelemetryHeartbeat{
		store:       store,
		instanceID:  deriveInstanceID(clusterSecret),
		region:      region,
		coreVersion: TelemetryCoreVersion,
		httpClient:  &http.Client{Timeout: 15 * time.Second},
	}
}

// SetLeader wires the leader-election gate. Without it the heartbeat fires
// from every Core — fine for single-instance dev, double-counts in HA.
func (t *TelemetryHeartbeat) SetLeader(l leader.Election) {
	t.leader = l
}

// Start kicks off the background ticker. Returns immediately. Stops on ctx
// cancellation (typically SIGTERM via the root context).
func (t *TelemetryHeartbeat) Start(ctx context.Context) {
	if strings.EqualFold(os.Getenv("DYLARIS_TELEMETRY"), "false") {
		log.Printf("[TELEMETRY] disabled via DYLARIS_TELEMETRY=false ENV")
		return
	}
	log.Printf("[TELEMETRY] sending anonymous usage stats every 10min to dylaris.dev (disable: Settings → Features or DYLARIS_TELEMETRY=false)")
	go func() {
		// Initial delay so we don't pile heartbeats on cold-start. 30s lets
		// the rest of the stack settle (Postgres connections, leader election).
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		t.post(ctx)
		tick := time.NewTicker(10 * time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				t.post(ctx)
			}
		}
	}()
}

func (t *TelemetryHeartbeat) post(ctx context.Context) {
	// Leader-only — multi-Core deployments share the same DB so each Core
	// would otherwise send identical payloads under different instanceIds,
	// inflating the platform counter.
	if t.leader != nil && !t.leader.IsLeader() {
		return
	}
	enabled, _ := t.store.GetSetting("telemetry_enabled")
	if strings.ToLower(enabled) == "false" {
		return
	}
	endpoint, _ := t.store.GetSetting("telemetry_endpoint")
	if endpoint == "" {
		endpoint = "https://dylaris.dev/api/heartbeat"
	}

	routing, _ := t.store.GetSetting("routing_mode")
	instType := "platform"
	if routing == "gateway" || routing == "both" {
		instType = "gateway"
	}

	servers, _ := t.store.GetAllActiveServers()
	online := 0
	for _, s := range servers {
		if s.Status == "online" {
			online++
		}
	}
	players, _ := t.store.SumLatestPlayerCounts()

	payload := map[string]interface{}{
		"instanceId": t.instanceID,
		"type":       instType,
		"servers":    len(servers),
		"online":     online,
		"players":    players,
		"version":    t.coreVersion,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("Dylaris-Telemetry/%s", t.coreVersion))
	// Optional shared secret. When the receiving endpoint enforces auth
	// (HEARTBEAT_SECRET on the site), this Core must send the matching key or its
	// heartbeats are rejected (401). Empty = no header, so an unsecured endpoint
	// keeps working unchanged.
	if key := strings.TrimSpace(os.Getenv("DYLARIS_TELEMETRY_KEY")); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		// Network blips are silent — operator doesn't need to see a stack
		// trace every 10min when their box is offline.
		return
	}
	_ = resp.Body.Close()
}
