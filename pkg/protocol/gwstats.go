package protocol

import "encoding/json"

// GatewayStatsVersion is the version of the gateway telemetry wire contract.
// A consumer MUST ignore a record whose Version it does not understand. Bump on
// BOTH repos (gateway/pkg/protocol + platform/pkg/protocol) when a field
// changes, never silently - same discipline as the beam header.
const GatewayStatsVersion = 1

// GatewayStats is the shared live-telemetry payload every gateway component
// (edge, warp-leader, beam-relay) publishes to its stats stream
// (dylaris:{component}:{id}:stats). The core fields are common to all three;
// component-specific extras are omitempty and ignored by the shared aggregator.
type GatewayStats struct {
	Version   int     `json:"v"`
	TS        int64   `json:"ts"`
	Component string  `json:"component"` // "edge" | "warp" | "beam"
	ID        string  `json:"id"`        // edgeID / leaderID / relayID
	Host      string  `json:"host"`      // swarm node hostname (per-host aggregation)
	Region    string  `json:"region"`    // "" for region-less
	CapMbit   int     `json:"cap_mbit"`  // BANDWIDTH_MBIT (0 = unset/unlimited)
	RxBps     uint64  `json:"rx_bps"`    // live receive throughput, bits/s
	TxBps     uint64  `json:"tx_bps"`    // live transmit throughput, bits/s
	CPU       float64 `json:"cpu"`
	RAMPct    float64 `json:"ram_pct"`

	// Component-specific extras (ignored by the shared aggregator).
	PerPeer         []PeerBandwidth `json:"per_peer,omitempty"`          // warp
	ActiveMCStreams int64           `json:"active_mc_streams,omitempty"` // edge
	ActiveTransfers int             `json:"active_transfers,omitempty"`  // beam
}

// PeerBandwidth is one warp peer's live throughput, keyed by WireGuard public
// key (mappable to a node via warp_peers). Consumed by the F3 rebalancer.
type PeerBandwidth struct {
	Pubkey string `json:"pubkey"`
	RxBps  uint64 `json:"rx_bps"`
	TxBps  uint64 `json:"tx_bps"`
}

// MarshalGatewayStats stamps the current version and marshals s.
func MarshalGatewayStats(s GatewayStats) ([]byte, error) {
	s.Version = GatewayStatsVersion
	return json.Marshal(s)
}

// ParseGatewayStats unmarshals a stats record and reports whether it is a
// version this build understands. ok=false with a nil error means "ignore this
// record" (unknown version), NOT a failure.
func ParseGatewayStats(data []byte) (GatewayStats, bool, error) {
	var s GatewayStats
	if err := json.Unmarshal(data, &s); err != nil {
		return GatewayStats{}, false, err
	}
	if s.Version != GatewayStatsVersion {
		return s, false, nil
	}
	return s, true, nil
}

// ResolveHost returns the swarm node hostname for per-host aggregation. In
// Swarm the stack sets NODE_HOSTNAME to the {{.Node.Hostname}} template so
// co-located components on one vServer report the SAME host and aggregate
// together; outside Swarm it falls back to the OS hostname.
func ResolveHost(getenv func(string) string, osHostname func() (string, error)) string {
	if h := getenv("NODE_HOSTNAME"); h != "" {
		return h
	}
	if h, err := osHostname(); err == nil {
		return h
	}
	return ""
}
