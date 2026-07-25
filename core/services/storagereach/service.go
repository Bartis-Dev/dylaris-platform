package storagereach

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// EventStorageReachChanged is the SSE event the panel refreshes on. Event
// types in this codebase are inline string literals at the publish site; this
// one is a constant because two packages emit it (the service and the config
// round handler) and they must agree.
const EventStorageReachChanged = "storagereach.changed"

const (
	// selfCheckEvery is the owner-chosen periodic interval.
	selfCheckEvery = 120 * time.Second
	// participantPollEvery is how often a Core looks for an open round. A
	// config round has a 15s cap, so polling at the self-check interval would
	// miss every round; this is the loop's real tick.
	participantPollEvery = time.Second
	// defaultBeaconMaxAge is 2.5 self-check intervals: long enough that one
	// missed refresh does not evict a healthy Core, short enough that a dead
	// one stops vouching for the share within five minutes.
	defaultBeaconMaxAge = 300 * time.Second
	// notSharedGraceRounds is how many CONSECUTIVE not-shared verdicts gate
	// the routes. See shouldGate for why only this status gets grace.
	notSharedGraceRounds = 2
)

// ServiceDeps are injected so this package stays a leaf: it never imports
// handlers (which owns config resolution) or services (which owns
// OnlineCoreIDs), and every dependency is trivially fakeable in tests.
type ServiceDeps struct {
	Redis  *redis.Client
	CoreID string
	// NewProvider builds a provider for a config.
	NewProvider ProviderFactory
	// ConfigFor returns the PERSISTED core storage config. The bool is false
	// when storage is not configured at all.
	ConfigFor func() (Config, bool)
	// OnlineCores returns the heartbeating Core ids.
	OnlineCores func(context.Context) ([]string, error)
	// Publish, when set, emits an SSE event. Optional.
	Publish func(eventType string, payload map[string]interface{})
}

// Service owns this Core's boot check, its periodic self-check, and its
// participation in other Cores' config rounds.
type Service struct {
	deps   ServiceDeps
	status *LocalStatus
	coord  *Coordinator

	// beaconMaxAge is a field rather than a constant so tests can age beacons
	// out without waiting five minutes.
	beaconMaxAge time.Duration
	// notSharedStreak counts consecutive not-shared verdicts. Only ever
	// touched from SelfCheck, which the single service loop serialises.
	notSharedStreak int

	mu         sync.Mutex
	lastStatus Status

	startOnce sync.Once
	doneCh    chan struct{}
}

func NewService(deps ServiceDeps) *Service {
	return &Service{
		deps:         deps,
		status:       NewLocalStatus(),
		coord:        NewCoordinator(deps.Redis, deps.CoreID, deps.NewProvider),
		beaconMaxAge: defaultBeaconMaxAge,
		doneCh:       make(chan struct{}),
	}
}

// Status is what the route gate reads on every storage request.
func (s *Service) Status() *LocalStatus { return s.status }

// Coordinator runs config-time rounds.
func (s *Service) Coordinator() *Coordinator { return s.coord }

// Done closes when the service loop has exited.
func (s *Service) Done() <-chan struct{} { return s.doneCh }

// Start runs the boot self-check, then loops. Calling it twice is a no-op so a
// second call cannot leave an unstoppable goroutine behind.
func (s *Service) Start(ctx context.Context) {
	s.startOnce.Do(func() {
		go s.run(ctx)
	})
}

func (s *Service) run(ctx context.Context) {
	defer close(s.doneCh)

	// The boot check happens before the first tick: a Core that cannot see the
	// shared storage must be gated the moment it joins the load balancer, not
	// two minutes later.
	s.SelfCheck(ctx)

	ticker := time.NewTicker(participantPollEvery)
	defer ticker.Stop()
	nextSelfCheck := time.Now().Add(selfCheckEvery)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// Take part in any open config round. This is the fast path: a round
		// lives for 15s, so it cannot wait for the 120s self-check.
		if roundID, err := PendingRoundID(ctx, s.deps.Redis); err == nil && roundID != "" {
			if err := RunParticipant(ctx, s.deps.Redis, s.deps.CoreID, roundID, s.deps.NewProvider); err != nil {
				log.Printf("storagereach: participating in round %s: %v", roundID, err)
			}
		}

		if time.Now().After(nextSelfCheck) {
			s.SelfCheck(ctx)
			nextSelfCheck = time.Now().Add(selfCheckEvery)
		}
	}
}

// SelfCheck proves THIS Core's access to the persisted shared storage and
// applies the result: it updates the local gate, records or clears the Redis
// fault, and publishes on a change.
//
// It refreshes the fleet beacon rather than running a round, because a
// self-check has no coordinator: each Core ticks on its own schedule, so only
// a stable per-Core path lets them see each other at all. And it reads the
// PERSISTED config, unlike a config round, which tests a candidate that has
// deliberately not been saved yet.
func (s *Service) SelfCheck(ctx context.Context) CoreResult {
	me := s.deps.CoreID

	cfg, configured := s.deps.ConfigFor()
	if !configured {
		// Not a fault: RequireCoreStorageConfigured already refuses the write
		// routes, and a red fleet banner on every fresh install would train
		// operators to ignore it.
		return s.apply(ctx, CoreResult{CoreID: me, Status: StatusOK})
	}

	participants, err := s.deps.OnlineCores(ctx)
	if err != nil {
		// Redis being unreachable is separately visible on the health page.
		// Closing every storage route because peers could not be COUNTED
		// would turn a Redis blip into a storage outage.
		log.Printf("storagereach: could not list online Cores, skipping this self-check: %v", err)
		return CoreResult{CoreID: me, Status: StatusOK}
	}
	if !contains(participants, me) {
		// This Core's own heartbeat has not landed yet. Checking without
		// itself in the set would prove nothing about itself.
		participants = append(participants, me)
	}

	fingerprint := Fingerprint(cfg)
	prov, provErr := s.deps.NewProvider(cfg)
	if provErr != nil {
		return s.apply(ctx, CoreResult{CoreID: me, Status: StatusUnreachable, Detail: provErr.Error()})
	}

	rep := RefreshBeacon(ctx, prov, BeaconOptions{
		CoreID: me, Fingerprint: fingerprint,
		Participants: participants, MaxAge: s.beaconMaxAge,
	})

	// Only THIS Core's verdict is meaningful here - the report is its own.
	// The fleet view is assembled from every Core's own fault record, not
	// from one Core's opinion of its peers.
	agg := Aggregate(participants, map[string]Report{me: rep}, fingerprint, true)
	for _, r := range agg.Results {
		if r.CoreID == me {
			return s.apply(ctx, r)
		}
	}
	return CoreResult{CoreID: me, Status: StatusOK}
}

// shouldGate decides whether a verdict closes this Core's storage routes.
//
// not-shared is the one ambiguous status: on a single pass, "I cannot see
// core-b's beacon" is indistinguishable from "core-b booted ten seconds ago
// and has not written one yet". Gating on that would make every scale-up
// briefly close its peers' storage routes. Every other failing status is a
// directly observed local fault and gates at once.
//
// The fault is RECORDED on the first pass either way, so the panel shows it
// immediately even while the routes stay open.
func (s *Service) shouldGate(status Status) bool {
	if status == StatusOK {
		s.notSharedStreak = 0
		return false
	}
	if status != StatusNotShared {
		s.notSharedStreak = 0
		return true
	}
	s.notSharedStreak++
	return s.notSharedStreak >= notSharedGraceRounds
}

// apply commits a verdict: gate or ungate locally, record or clear the fault,
// and publish only when the status actually changed.
func (s *Service) apply(ctx context.Context, res CoreResult) CoreResult {
	// The fault below is recorded on the FIRST failing pass regardless, so the
	// panel shows it immediately. Only the local route gate waits out the
	// grace window, and only for not-shared (see shouldGate).
	if s.shouldGate(res.Status) {
		s.status.Set(res.Status, res.Detail)
	} else {
		s.status.Set(StatusOK, "")
	}

	if res.Status == StatusOK {
		if err := ClearFault(ctx, s.deps.Redis, res.CoreID); err != nil {
			log.Printf("storagereach: %v", err)
		}
	} else {
		hostname, _ := os.Hostname()
		now := time.Now().Unix()
		if err := RecordFault(ctx, s.deps.Redis, Fault{
			CoreID:       res.CoreID,
			Hostname:     hostname,
			Status:       res.Status,
			Detail:       res.Detail,
			MissingPeers: res.MissingPeers,
			DeniedPeers:  res.DeniedPeers,
			Since:        now,
			At:           now,
		}); err != nil {
			log.Printf("storagereach: %v", err)
		}
		log.Printf("storagereach: %s cannot use the shared storage: %s %s", res.CoreID, res.Status, res.Detail)
	}

	s.mu.Lock()
	changed := s.lastStatus != res.Status
	s.lastStatus = res.Status
	s.mu.Unlock()

	// Only on a change: publishing every 120s tick would make a healthy fleet
	// chatter at the panel forever.
	if changed && s.deps.Publish != nil {
		s.deps.Publish(EventStorageReachChanged, map[string]interface{}{
			"coreId": res.CoreID,
			"status": string(res.Status),
		})
	}
	return res
}
