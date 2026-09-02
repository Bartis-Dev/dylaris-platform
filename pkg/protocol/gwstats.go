package protocol

import (
	"encoding/json"
	"strings"
)

// GatewayStatsVersion is the version of the gateway telemetry wire contract.
// A consumer MUST ignore a record whose Version it does not understand.
//
// Bumping this is a HARD CUTOVER, not a courtesy: ParseGatewayStats compares
// for equality, so the moment one repo bumps, every consumer built from the
// other discards ALL of that component's records until both sides are deployed.
// That is the right behaviour for a field whose MEANING changed and the wrong
// price for a field that was merely added - JSON already ignores what it does
// not know, and an absent field decodes to the zero value.
//
// So: ADDING an omitempty field does not bump this. Renaming a field, changing
// its units, or repurposing one does, and then both repos bump together and are
// deployed together. Where a consumer must tell "an old producer said nothing"
// apart from "a new producer said zero", that is what a pointer or a presence
// key inside Counters/Gauges is for - not a bump.
const GatewayStatsVersion = 1

// MaxCustomMetrics bounds how many Counters + Gauges entries a consumer will
// accept from one record.
//
// The maps are the reason this contract never has to change again, and they are
// also the one part of it a producer could make unbounded. Every distinct name
// becomes its own stored series forever, so a producer that ever folded a
// session id or an address into a name would turn a fixed set of series into
// one per player. The cap is deliberately far above what any component
// publishes (a handful each) and far below what would hurt.
const MaxCustomMetrics = 32

// GatewayStats is the shared live-telemetry payload every gateway component
// (edge, warp-leader, beam-relay, link, splice) publishes to its stats stream
// (dylaris:{component}:{id}:stats). The core fields are common to all; the
// component-specific extras are omitempty and ignored by consumers that do not
// know them.
type GatewayStats struct {
	Version   int     `json:"v"`
	TS        int64   `json:"ts"`
	Component string  `json:"component"` // "edge" | "warp" | "beam" | "link" | "splice"
	ID        string  `json:"id"`        // edgeID / leaderID / relayID / linkID
	Host      string  `json:"host"`      // swarm node hostname (per-host aggregation)
	Region    string  `json:"region"`    // "" for region-less
	CapMbit   int     `json:"cap_mbit"`  // BANDWIDTH_MBIT (0 = unset/unlimited)
	RxBps     uint64  `json:"rx_bps"`    // live receive throughput, bits/s
	TxBps     uint64  `json:"tx_bps"`    // live transmit throughput, bits/s
	CPU       float64 `json:"cpu"`
	RAMPct    float64 `json:"ram_pct"`

	// UptimeSec is how long the publishing PROCESS has been running. It is the
	// restart signal: a value lower than the one before it means this component
	// restarted between the two records, which is what makes "how many players
	// survived an edge restart" a question the record can answer at all.
	UptimeSec int64 `json:"uptime_sec,omitempty"`

	// Counters is how many times something HAPPENED since the previous publish
	// - a DELTA, not a running total. That distinction is load-bearing: a
	// cumulative counter resets when its process dies, and a process dying is
	// precisely the event these counters exist to measure, so a total would
	// subtract exactly where the interesting number is. Deltas simply stop and
	// resume. A consumer SUMS them over a window.
	Counters map[string]int64 `json:"counters,omitempty"`

	// Gauges is what is true at the instant of the sample - live sessions,
	// configured peers. A consumer AVERAGES them over a window, and min/max
	// stay meaningful.
	Gauges map[string]float64 `json:"gauges,omitempty"`

	// Component-specific extras (ignored by the shared aggregator).
	PerPeer         []PeerBandwidth `json:"per_peer,omitempty"`          // warp
	ActiveMCStreams int64           `json:"active_mc_streams,omitempty"` // edge
	ActiveTransfers int             `json:"active_transfers,omitempty"`  // beam
}

// ValidMetricName reports whether name is safe to turn into a stored series.
//
// Lowercase letters, digits and underscore only, and short. The rule is not
// style: a name reaches a metric column and a chart legend, and the one way
// these maps could hurt is a producer folding a per-session value into a name.
// Refusing anything that does not look like a fixed identifier is what keeps
// the series count a property of the CODE rather than of the traffic.
func ValidMetricName(name string) bool {
	if name == "" || len(name) > 40 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return !strings.HasPrefix(name, "_") && !strings.HasSuffix(name, "_")
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
