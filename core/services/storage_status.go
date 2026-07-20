package services

import (
	"context"
	"sync"
	"time"

	"dylaris-core/storage"
)

// This file forwards the two storage backends' connection-state transitions
// onto the existing system-events channel, and answers the current state for
// the read endpoint.
//
// What it reports and what it does not. It observes transitions the storage
// layer already decided: the host-path watchdog flipping between "no evidence
// of a problem" and "not answering", and the S3 wrapper entering or leaving its
// pause-and-retry state. It changes neither. A subscriber that misses an event
// is not told again: the SSE transport has no replay, and the coalescing below
// can skip an intermediate value. GET /api/storage/connection exists because of
// exactly that.

// StorageConnectionChangedEvent is the event type for a backend's connection
// state change. Event types are bare literals at their call sites everywhere
// else in this package; this one is a const because the forwarder and its test
// both need the identical string.
const StorageConnectionChangedEvent = "storage.connection.changed"

// Backend labels and the per-backend state vocabulary.
//
// The path and s3 vocabularies deliberately do not share a word for "not ok".
// The gate FAILS requests immediately, so "unavailable" is what a user sees;
// the S3 wrapper makes them WAIT, so "reconnecting" is what a user sees.
// Collapsing both into one label would describe two different experiences with
// one word.
const (
	storageBackendPath = "path"
	storageBackendS3   = "s3"

	storageStateOK           = "ok"
	storageStateUnavailable  = "unavailable"
	storageStateReconnecting = "reconnecting"
)

// storagePublishTimeout bounds ONE publish. Publish is a synchronous Redis
// round-trip, and a Redis that has stopped answering is exactly the situation
// in which storage is also unhappy, so an unbounded one would park the
// forwarder goroutine for the life of the process.
const storagePublishTimeout = 5 * time.Second

// StorageConnectionState is one backend's coarse state as it goes on the wire.
// Since is nil (JSON null) whenever State is ok, and an RFC3339 UTC timestamp
// otherwise.
type StorageConnectionState struct {
	State string  `json:"state"`
	Since *string `json:"since"`
}

// StorageConnectionSnapshot is the read endpoint's body. Both keys are always
// present so a caller never has to distinguish "ok" from "not reported".
type StorageConnectionSnapshot struct {
	Path StorageConnectionState `json:"path"`
	S3   StorageConnectionState `json:"s3"`
}

// StorageStatus forwards backend connection transitions onto the system-events
// channel and answers the current state.
//
// Every method is safe on a nil receiver and on nil backend handles, matching
// the discipline in system_events.go and in the storage package: an AppState
// built without these (only ever a test) shares one code path with the wired-up
// one instead of needing a branch at every call site.
type StorageStatus struct {
	events *SystemEventsPublisher
	gate   *storage.Gate
	s3     *storage.S3Resilience

	// now is injected so a test can pin the path backend's since without
	// sleeping. The gate's hook carries no timestamp of its own.
	now func() time.Time

	// mu guards pending and pathSince.
	mu sync.Mutex
	// pending is the not-yet-published state per backend. Writing a backend's
	// entry overwrites the previous one, which is the coalescing: a storm of
	// transitions costs one map write each and produces one event per backend
	// when the forwarder next drains.
	//
	// The COST, stated plainly: an intermediate state can be skipped, so a
	// brief ok -> unavailable -> ok blip may never reach the panel at all. That
	// is the right trade for a state signal, where the last value is the true
	// one, but it means this is not a transition log and must not be read as one.
	pending map[string]StorageConnectionState
	// pathSince is the only backend state mirrored here, because the gate has
	// no timestamp to read back. Everything else Snapshot needs is read live
	// from the backends; a mirror of the booleans could drift from the thing it
	// mirrors and then this endpoint would lie.
	pathSince time.Time

	// wake has capacity 1 and carries no data. It is the signal that pending
	// has something in it. Capacity 1 plus a non-blocking send is what keeps a
	// hook from ever blocking and keeps a transition storm from queueing work
	// proportional to its length.
	//
	// The state rides in pending rather than in this channel because the two
	// backends must not evict each other: a single-slot channel of events would
	// drop an s3 transition because a path transition happened to land second.
	wake chan struct{}
}

func NewStorageStatus(events *SystemEventsPublisher, gate *storage.Gate, s3 *storage.S3Resilience) *StorageStatus {
	return &StorageStatus{
		events:  events,
		gate:    gate,
		s3:      s3,
		now:     time.Now,
		pending: make(map[string]StorageConnectionState),
		wake:    make(chan struct{}, 1),
	}
}

// Start runs the forwarder until ctx is done. It spawns its own goroutine and
// returns immediately, which is how every other service in this package is
// started from main.go.
func (s *StorageStatus) Start(ctx context.Context) {
	if s == nil {
		return
	}
	go s.run(ctx)
}

// Attach installs both backends' transition hooks. Call it after the publisher
// exists, or the forwarder captures a nil one.
func (s *StorageStatus) Attach() {
	if s == nil {
		return
	}
	s.gate.SetOnChange(s.onGateChange)
	s.s3.SetOnChange(s.onS3Change)
}

// onGateChange normalises the gate's polarity (it reports HEALTHY) into the
// path vocabulary.
//
// cause is deliberately dropped and must stay dropped. This payload reaches
// every authenticated user: /api/system/events sits behind AuthMiddleware, not
// an admin gate, and the stream does no per-event filtering, so whatever goes
// in here is readable by any account on the platform. The cause carries the
// host path and the errno that produced it. It stays in the logs and in the
// admin-gated health endpoint, which is where an operator already looks.
func (s *StorageStatus) onGateChange(healthy bool, cause error) {
	if s == nil {
		return
	}
	state := StorageConnectionState{State: storageStateOK}
	var since time.Time
	if !healthy {
		since = s.now()
		state = StorageConnectionState{State: storageStateUnavailable, Since: storageSinceString(since)}
	}

	s.mu.Lock()
	s.pathSince = since
	s.pending[storageBackendPath] = state
	s.mu.Unlock()
	s.signal()
}

// onS3Change normalises the S3 wrapper's polarity (it reports RECONNECTING,
// the opposite of the gate's) into the s3 vocabulary.
//
// lastErr is dropped for the same reason cause is; see onGateChange. It carries
// the endpoint host and the bucket.
func (s *StorageStatus) onS3Change(reconnecting bool, since time.Time, lastErr error) {
	if s == nil {
		return
	}
	state := StorageConnectionState{State: storageStateOK}
	if reconnecting {
		state = StorageConnectionState{State: storageStateReconnecting, Since: storageSinceString(since)}
	}

	s.mu.Lock()
	s.pending[storageBackendS3] = state
	s.mu.Unlock()
	s.signal()
}

// signal wakes the forwarder without ever blocking the caller. The hooks run on
// the watchdog's probe goroutine and on request goroutines, and neither may be
// made to wait on Redis.
func (s *StorageStatus) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
		// The slot is already full, so the forwarder has not drained yet and
		// will see this transition when it does.
	}
}

func (s *StorageStatus) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
			s.flush(ctx)
		}
	}
}

// flush publishes one event per backend that changed, never a combined
// document: the two backends fail independently and a subscriber acts on one
// of them at a time.
func (s *StorageStatus) flush(ctx context.Context) {
	s.mu.Lock()
	batch := s.pending
	s.pending = make(map[string]StorageConnectionState)
	s.mu.Unlock()

	// Ranged over a fixed order rather than over the map, so the event order is
	// not Go's randomised map iteration.
	for _, backend := range []string{storageBackendPath, storageBackendS3} {
		state, ok := batch[backend]
		if !ok {
			continue
		}
		s.publish(ctx, backend, state)
	}
}

func (s *StorageStatus) publish(ctx context.Context, backend string, state StorageConnectionState) {
	var since interface{}
	if state.Since != nil {
		since = *state.Since
	}
	pctx, cancel := context.WithTimeout(ctx, storagePublishTimeout)
	defer cancel()
	s.events.Publish(pctx, StorageConnectionChangedEvent, map[string]interface{}{
		"backend": backend,
		"state":   state.State,
		"since":   since,
	})
}

// Snapshot reads both backends LIVE. It holds no copy of their booleans on
// purpose: a copy updated only by the hooks would be wrong for any state this
// process did not observe the transition into.
//
// An ok here means the backend has given no evidence of a problem, NOT that it
// has been verified reachable. Neither backend proves reachability: the gate's
// verdict can be up to one probe interval stale, and nothing probes S3 at all.
func (s *StorageStatus) Snapshot() StorageConnectionSnapshot {
	snap := StorageConnectionSnapshot{
		Path: StorageConnectionState{State: storageStateOK},
		S3:   StorageConnectionState{State: storageStateOK},
	}
	if s == nil {
		return snap
	}

	if healthy, _ := s.gate.Healthy(); !healthy {
		s.mu.Lock()
		since := s.pathSince
		s.mu.Unlock()
		// since is the zero time when this process never saw the transition,
		// e.g. the hooks were never attached. Reporting a null there is honest;
		// inventing "now" would date the outage to whenever someone asked.
		snap.Path = StorageConnectionState{State: storageStateUnavailable, Since: storageSinceString(since)}
	}

	if reconnecting, since, _ := s.s3.State(); reconnecting {
		snap.S3 = StorageConnectionState{State: storageStateReconnecting, Since: storageSinceString(since)}
	}
	return snap
}

// storageSinceString renders a timestamp the way both the event payload and the
// endpoint body carry it, and nil for the zero time.
func storageSinceString(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
