package services

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"dylaris-core/pkg/leader"
	"dylaris-core/store"
)

// LeaderAnnouncement is what a warp leader writes into its own liveness key.
//
// CROSS-REPO WIRE CONTRACT, produced by gateway/warp/announce.go. Both repos
// compile their own copy of this shape, so a field may be ADDED but never
// renamed or repurposed. V tells an incompatible future shape apart instead of
// half-reading it.
type LeaderAnnouncement struct {
	V        int    `json:"v"`
	Region   string `json:"region"`
	Endpoint string `json:"endpoint"`
	Subnet   string `json:"subnet,omitempty"`
}

// leaderAnnouncementVersion is the only shape this reader understands.
const leaderAnnouncementVersion = 1

const (
	// warpAliveKeyPrefix and warpAliveKeySuffix bracket a leader's id. The
	// pattern deliberately requires the suffix: the same namespace holds
	// dylaris:warp:firewall:allowed_ports and dylaris:warp:region:*:subnet,
	// neither of which is a leader.
	warpAliveKeyPrefix = "dylaris:warp:"
	warpAliveKeySuffix = ":alive"

	// selfRegInterval is how often the registry is rebuilt from what the
	// leaders say. Slower than the 30s liveness TTL on purpose: this loop
	// DISCOVERS leaders and refreshes endpoints, while enroll already reads
	// liveness directly on every request. A leader that just booted joins
	// within a minute, which is faster than an operator typing a row.
	selfRegInterval = 60 * time.Second
)

// warpRegistrarStore is the narrow store surface this needs.
type warpRegistrarStore interface {
	GetWarpRegion(region string) (*store.WarpRegion, error)
	UpsertWarpRegion(region, subnet string, enabled bool) error
	ListWarpLeaders() ([]store.WarpLeader, error)
	UpsertWarpLeader(leaderID, region, endpoint string, enabled bool) error
}

// WarpSelfRegistrar keeps the warp leader registry in step with the leaders that
// are actually running.
//
// A leader used to be a row an operator typed into the panel, which is wrong in
// two ways that only show up in a real deployment. The leader runs as a GLOBAL
// Swarm service keyed on the node hostname, so every host added to the edge tier
// starts a leader with a new id that Core has never heard of - and the endpoint
// an operator types is the host's public address, which nothing but that host
// knows for certain. Both facts are already available at the leader, and the
// leader already writes to Redis every ten seconds.
//
// What it deliberately does NOT do is remove a leader whose announcement stops.
// Core drops a peer by enumerating the leader rows of its region; a row deleted
// while its WireGuard peers are configured leaves those peers on a machine
// nothing can address any more. Liveness already orders a dead leader last, and
// an operator can still delete the row explicitly. Absence is not the same as
// "please forget this existed".
type WarpSelfRegistrar struct {
	store  warpRegistrarStore
	redis  *redis.Client
	leader leader.Election
}

func NewWarpSelfRegistrar(s warpRegistrarStore, r *redis.Client) *WarpSelfRegistrar {
	return &WarpSelfRegistrar{store: s, redis: r}
}

func (s *WarpSelfRegistrar) SetLeader(l leader.Election) { s.leader = l }

func (s *WarpSelfRegistrar) Start(ctx context.Context) {
	log.Printf("Warp self-registration started (interval: %s)", selfRegInterval)
	s.RunOnce(ctx)
	ticker := time.NewTicker(selfRegInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.RunOnce(ctx)
			}
		}
	}()
}

// RunOnce reads every live announcement and applies it. Exported for tests and
// for a caller that wants the registry current right now.
func (s *WarpSelfRegistrar) RunOnce(ctx context.Context) {
	if s.leader != nil && !s.leader.IsLeader() {
		return
	}
	if s.redis == nil || s.store == nil {
		return
	}
	found, err := s.announcements(ctx)
	if err != nil {
		log.Printf("[warp] self-registration: %v", err)
		return
	}
	for id, a := range found {
		s.apply(id, a)
	}
}

// announcements collects every live leader announcement, keyed by leader id.
//
// The id comes from the KEY, not from the payload. Core addresses a leader by
// building dylaris:warp:<id>:queue, so an id taken from anywhere else could name
// a queue nothing is listening on.
func (s *WarpSelfRegistrar) announcements(ctx context.Context) (map[string]LeaderAnnouncement, error) {
	out := map[string]LeaderAnnouncement{}
	var cursor uint64
	for {
		keys, next, err := s.redis.Scan(ctx, cursor, warpAliveKeyPrefix+"*"+warpAliveKeySuffix, 100).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			id := strings.TrimSuffix(strings.TrimPrefix(key, warpAliveKeyPrefix), warpAliveKeySuffix)
			if id == "" {
				continue
			}
			val, err := s.redis.Get(ctx, key).Result()
			if err != nil {
				// Expired between SCAN and GET is the normal race: that leader
				// is stale by definition. Anything else is Redis failing, and
				// reading it as "no leaders" would be a silent no-op every
				// minute, so it stops the whole pass instead.
				if errors.Is(err, redis.Nil) {
					continue
				}
				return nil, err
			}
			// "1" is what a leader older than self-registration writes, and it
			// is also the fallback of a current one that cannot name its own
			// address. Neither is an announcement; the operator-entered row
			// stands.
			var a LeaderAnnouncement
			if err := json.Unmarshal([]byte(val), &a); err != nil {
				continue
			}
			if a.V != leaderAnnouncementVersion || a.Region == "" || a.Endpoint == "" {
				continue
			}
			out[id] = a
		}
		if next == 0 {
			return out, nil
		}
		cursor = next
	}
}

// apply reconciles one announcement into the registry.
func (s *WarpSelfRegistrar) apply(id string, a LeaderAnnouncement) {
	if !s.ensureRegion(id, a) {
		return
	}
	leaders, err := s.store.ListWarpLeaders()
	if err != nil {
		log.Printf("[warp] self-registration: list leaders: %v", err)
		return
	}
	for _, l := range leaders {
		if l.LeaderID != id {
			continue
		}
		if l.Endpoint == a.Endpoint && l.Region == a.Region {
			return // already what it says it is
		}
		// The stored Enabled flag is carried over, never re-asserted. An
		// operator who disabled a leader means it, and a heartbeat arriving
		// sixty seconds later must not undo that - it would be a switch that
		// flips itself back on while the machine it belongs to is still
		// running, which is exactly when someone reaches for it.
		if err := s.store.UpsertWarpLeader(id, a.Region, a.Endpoint, l.Enabled); err != nil {
			log.Printf("[warp] self-registration: update leader %s: %v", id, err)
			return
		}
		log.Printf("[warp] leader %s moved: %s/%s -> %s/%s", id, l.Region, l.Endpoint, a.Region, a.Endpoint)
		return
	}
	// Enabled on sight. The announcement is authenticated by possession of the
	// scoped Redis credential, and the leader derives the region's WireGuard key
	// from CLUSTER_SECRET anyway - it can already do everything a leader does.
	// A pending state would only reinstate the manual step this removes.
	if err := s.store.UpsertWarpLeader(id, a.Region, a.Endpoint, true); err != nil {
		log.Printf("[warp] self-registration: register leader %s: %v", id, err)
		return
	}
	log.Printf("[warp] leader %s registered itself in region %s at %s", id, a.Region, a.Endpoint)
}

// ensureRegion makes sure the announced region exists, and reports whether the
// leader may be registered into it.
func (s *WarpSelfRegistrar) ensureRegion(id string, a LeaderAnnouncement) bool {
	reg, err := s.store.GetWarpRegion(a.Region)
	if err == nil && reg != nil {
		// A subnet disagreement is a misconfiguration, not something to
		// reconcile. Enrolled machines hold addresses out of the stored subnet,
		// so overwriting it would strand every one of them; and the leader
		// bounds its peers by its own, so routing them through it would fail
		// anyway. Say which two values disagree and change nothing.
		if a.Subnet != "" && reg.Subnet != a.Subnet {
			log.Printf("[warp] leader %s announces subnet %s but region %s is %s; not registering it. Fix WARP_WG_SUBNET or the region.",
				id, a.Subnet, a.Region, reg.Subnet)
			return false
		}
		return true
	}
	// No region yet. The leader knows its own subnet, and requiring an operator
	// to retype it into the panel is where the two used to drift apart - the
	// leader fatals at boot on a mismatch, so the pair has to agree exactly.
	if a.Subnet == "" {
		log.Printf("[warp] leader %s announces region %s, which does not exist, and no subnet to create it with. Set WARP_WG_SUBNET or create the region.", id, a.Region)
		return false
	}
	if err := s.store.UpsertWarpRegion(a.Region, a.Subnet, true); err != nil {
		log.Printf("[warp] self-registration: create region %s: %v", a.Region, err)
		return false
	}
	log.Printf("[warp] region %s created from leader %s (subnet %s)", a.Region, id, a.Subnet)
	return true
}
