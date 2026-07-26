package storagereach

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"time"

	"dylaris-core/storage"

	"github.com/redis/go-redis/v9"
)

const (
	keyPrefix       = "dylaris:storagereach:"
	currentRoundKey = keyPrefix + "current"
	// roundTTL outlives the 15s round window with enough slack that a
	// participant which started late still finds the metadata it needs.
	roundTTL = 60 * time.Second
	// configTTL is deliberately shorter than roundTTL: the staged config
	// carries the S3 secret, so it must not survive a coordinator that died
	// before its explicit delete.
	configTTL = 30 * time.Second
	// defaultProbeWatchdogGrace is how long PAST the round deadline the
	// coordinator waits for its own probe before it stops waiting and rules on
	// it.
	//
	// The margin is the whole point of the constant. Probe stops retrying at the
	// round deadline but still has to finish the backend call it is inside, and
	// on a host path that call is a syscall no context can interrupt. Ruling at
	// exactly the deadline would race a perfectly healthy probe and convict this
	// Core as unreachable when all it did was fail to see a peer - sending the
	// operator to the wrong place, which is the one thing the taxonomy exists to
	// avoid. A second is far longer than a healthy backend's final call and far
	// shorter than a wedged mount's forever, and it keeps the worst-case round
	// (15s + 1s) inside reachRoundLockTTL, which must outlive it.
	defaultProbeWatchdogGrace = time.Second
)

func roundMetaKey(id string) string   { return keyPrefix + "round:" + id + ":meta" }
func roundConfigKey(id string) string { return keyPrefix + "round:" + id + ":config" }
func roundReportKey(id, coreID string) string {
	return keyPrefix + "round:" + id + ":report:" + coreID
}

// roundMeta is what a participant needs to run its side. The config lives in
// its own key with a shorter TTL because it holds a credential.
type roundMeta struct {
	RoundID      string   `json:"roundId"`
	Fingerprint  string   `json:"fingerprint"`
	Participants []string `json:"participants"`
	Coordinator  string   `json:"coordinator"`
	// DeadlineUnixMilli is millisecond, not second, precision: a whole-second
	// Unix() timestamp truncates away up to 999ms, which is a rounding error
	// against a multi-second production round but swallows the entire budget
	// of a short test deadline, making every real participant compute a
	// negative budget and return without ever probing.
	DeadlineUnixMilli int64 `json:"deadlineUnixMilli"`
	// PollEveryMillis carries the coordinator's chosen probe retry cadence so a
	// participant retries at the same pace instead of a cadence hardcoded
	// independently of what the round was configured with (a mismatch here is
	// how a fast test deadline ends up overshot by a slow, unrelated retry
	// interval).
	PollEveryMillis int64 `json:"pollEveryMillis"`
}

// ProviderFactory builds a provider for a candidate config. It is injected so
// this package never imports handlers (which owns config resolution) and so
// tests can hand every Core a LocalProvider on one root. It must be safe for
// concurrent use: the service loop's watchdogs can call it from the check
// goroutine and the round goroutine at the same time.
type ProviderFactory func(cfg Config) (storage.StorageProvider, error)

// Coordinator runs a config-time round: stage, publish, probe locally, collect,
// aggregate, clean up.
type Coordinator struct {
	rdb         *redis.Client
	coreID      string
	newProvider ProviderFactory

	// probeGrace is a field rather than a bare const only so the suite can
	// shrink it and prove the watchdog fires without waiting out a real second,
	// the same arrangement Service and storage.S3Resilience use. Production
	// never reassigns it.
	probeGrace time.Duration
}

func NewCoordinator(rdb *redis.Client, coreID string, newProvider ProviderFactory) *Coordinator {
	return &Coordinator{
		rdb:         rdb,
		coreID:      coreID,
		newProvider: newProvider,
		probeGrace:  defaultProbeWatchdogGrace,
	}
}

// RoundOptions bounds a round and exposes its progress.
type RoundOptions struct {
	// Deadline is the hard cap. Success returns as soon as every participant
	// is confirmed; only failure waits it out.
	Deadline time.Duration
	// PollEvery is how often collected reports are re-read. It also becomes
	// the retry cadence every participant's own Probe uses while it looks for
	// peer beacons - the two are the same "how chatty is this round" knob, so
	// a short PollEvery in a test also keeps the underlying Probe loop from
	// overshooting a short Deadline by a whole extra cycle.
	PollEvery time.Duration
	// OnProgress, when set, is called with each new partial result - "new"
	// meaning the aggregate actually differs from the last call, not merely
	// that another poll tick elapsed with nothing to say. The final call (the
	// round completes or the deadline expires) always fires regardless, so a
	// caller can rely on it to learn the round settled. Never called
	// concurrently.
	OnProgress func(RoundResult)
}

// RunRound opens a round, takes part in it, and returns the aggregate.
//
// Fail-closed by construction: the result is OK only when every participant
// reported ok before the deadline. A round that times out returns a result,
// not an error - the caller needs the per-Core taxonomy to show the operator,
// and an error would throw it away.
func (c *Coordinator) RunRound(ctx context.Context, cfg Config, participants []string, opts RoundOptions) (RoundResult, error) {
	if len(participants) == 0 {
		return RoundResult{}, fmt.Errorf("storagereach: no participants")
	}
	if opts.Deadline <= 0 {
		opts.Deadline = 15 * time.Second
	}
	if opts.PollEvery <= 0 {
		opts.PollEvery = time.Second
	}

	roundID, err := newRoundID()
	if err != nil {
		return RoundResult{}, err
	}
	fingerprint := Fingerprint(cfg)
	deadline := time.Now().Add(opts.Deadline)

	meta := roundMeta{
		RoundID:           roundID,
		Fingerprint:       fingerprint,
		Participants:      participants,
		Coordinator:       c.coreID,
		DeadlineUnixMilli: deadline.UnixMilli(),
		PollEveryMillis:   opts.PollEvery.Milliseconds(),
	}
	// The staged config holds the S3 secret; removing it is not optional, so
	// this is registered BEFORE publishRound runs. publishRound writes the
	// meta key, then the config key, then the current-round key last: if it
	// fails partway through (e.g. Redis drops the connection between those
	// SETs), the config key can already be sitting in Redis when we return
	// the error below, and it still needs to come out rather than wait out
	// its TTL.
	defer func() {
		if err := c.rdb.Del(context.WithoutCancel(ctx), roundConfigKey(roundID), currentRoundKey).Err(); err != nil {
			log.Printf("storagereach: could not clear round %s staging keys: %v", roundID, err)
		}
	}()
	if err := publishRound(ctx, c.rdb, meta, cfg); err != nil {
		return RoundResult{}, err
	}

	// The coordinator is a participant like any other: it must prove its own
	// access, not assume it. The provider BUILD runs inside probeSelf's own
	// watchdogged child goroutine, sharing its budget with the probe - see
	// probeSelf's doc comment for why a build that never returns (a dead
	// mount's os.MkdirAll, gated by nothing since NewReachProvider always
	// passes a nil gate) is exactly the same hazard a wedged probe is, and
	// needs the same watchdog.
	rep, prov, completed := c.probeSelf(ctx, cfg, deadline, ProbeOptions{
		CoreID: c.coreID, RoundID: roundID, Fingerprint: fingerprint,
		Participants: participants, Deadline: opts.Deadline, RetryEvery: opts.PollEvery,
	})
	storeReport(ctx, c.rdb, roundID, rep)
	if prov != nil {
		// prov is nil when the build itself failed or never came back in
		// time; either way there is no beacon on the backend for CleanupRound
		// to remove.
		defer func() {
			cleanup := func() {
				if err := CleanupRound(context.WithoutCancel(ctx), prov, roundID); err != nil {
					// Orphans age out and are ignored by later rounds, which key
					// off the round id in every beacon.
					log.Printf("storagereach: probe cleanup for round %s: %v", roundID, err)
				}
			}
			if !completed {
				// The probe never came back, so this DeletePath goes to a
				// backend that has stopped answering and would block the
				// caller's goroutine exactly as the probe did - undoing the
				// watchdog one line after it fired. Cleanup is best-effort by
				// contract (see CleanupRound), so it is detached instead.
				go cleanup()
				return
			}
			cleanup()
		}()
	}

	var last RoundResult
	var lastReported RoundResult
	reported := false
	for {
		reports := collectReports(ctx, c.rdb, roundID, participants)
		expired := !time.Now().Before(deadline)
		complete := len(reports) == len(participants)
		final := complete || expired

		last = Aggregate(participants, reports, fingerprint, final)
		last.RoundID = roundID
		// Skip a publish that would say nothing new: the panel's fleet-health
		// card reloads on every storagereach.changed event, so an unchanged
		// result on every one of up to 15 poll ticks per round is up to 15
		// no-op status refetches per open panel session. The final call is
		// exempt - a settled round must always report even if the last
		// reported state happened to already match it.
		if opts.OnProgress != nil && (final || !reported || !reflect.DeepEqual(last, lastReported)) {
			opts.OnProgress(last)
			lastReported = last
			reported = true
		}
		if final {
			return last, nil
		}

		select {
		case <-ctx.Done():
			last.Done = true
			last.OK = false
			return last, ctx.Err()
		case <-time.After(opts.PollEvery):
		}
	}
}

// probeSelfOutcome is what the build-and-probe child goroutine below hands
// back over its one channel. prov is nil when the build failed or never
// returned in time - either way there is nothing on the backend for the
// caller to clean up.
type probeSelfOutcome struct {
	prov storage.StorageProvider
	rep  Report
}

// probeSelf builds the coordinator's OWN provider AND runs its OWN probe on a
// single watchdogged child goroutine, sharing one budget between the two, and
// returns what it observed. The bool is false when neither came back in time,
// in which case the report is the synthetic one below.
//
// Same hazard and the same watchdog shape Service.run already uses for its
// self-check and its round participation: a wedged mount blocks inside syscalls
// no context cancellation can interrupt (storage/provider.go says so on the
// interface itself). Both halves run INLINE on the admin's HTTP goroutine while
// that goroutine holds the config-round lock: the BUILD (newStorageProviderForConfig's
// os.MkdirAll, ungated because NewReachProvider always passes a nil gate for a
// candidate config - see its own doc comment) just as much as the probe that
// follows it. Either one never returning would make every later storage-config
// save on this replica answer 409 "wait for it to finish" forever, so the two
// are watchdogged together rather than only the probe on its own.
//
// The child goroutine is left to leak on a timeout because nothing in Go can
// reclaim one parked in a syscall. results has capacity 1 so an orphan can still
// complete its send and exit rather than block on a channel nobody reads. The
// child produces a probeSelfOutcome and nothing else; every piece of state
// stays on the calling goroutine.
func (c *Coordinator) probeSelf(ctx context.Context, cfg Config, deadline time.Time, opts ProbeOptions) (Report, storage.StorageProvider, bool) {
	// Bound the probe's individual backend CALLS, not just the gaps between
	// them: Probe checks its own deadline only between calls, so without this
	// nothing bounds a single one. The S3 SDK genuinely aborts on a context
	// (every S3Provider method threads the caller's ctx into aws-sdk-go-v2), so
	// for s3 this alone is a real bound; a host-path syscall ignores it, which
	// is what the watchdog below is for.
	probeCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	results := make(chan probeSelfOutcome, 1)
	go func() {
		prov, err := c.newProvider(cfg)
		if err != nil {
			// Same shape RunParticipant uses for its own build failure: no
			// SeenPeers/etc are set here, only WriteErr, which Aggregate turns
			// into unreachable.
			results <- probeSelfOutcome{rep: Report{
				CoreID: opts.CoreID, Fingerprint: opts.Fingerprint,
				WriteErr: err.Error(), At: time.Now().Unix(),
			}}
			return
		}
		results <- probeSelfOutcome{prov: prov, rep: Probe(probeCtx, prov, opts)}
	}()

	grace := c.probeGrace
	if grace <= 0 {
		grace = defaultProbeWatchdogGrace
	}
	timer := time.NewTimer(time.Until(deadline) + grace)
	defer timer.Stop()

	select {
	case out := <-results:
		return out.rep, out.prov, true
	case <-ctx.Done():
		return timedOutReport(opts, fmt.Sprintf("storage probe did not finish: %v", ctx.Err())), nil, false
	case <-timer.C:
		// The probe may have completed in the same instant the budget expired.
		// Its own honest observation always beats a synthetic verdict.
		select {
		case out := <-results:
			return out.rep, out.prov, true
		default:
		}
		return timedOutReport(opts, fmt.Sprintf("storage probe did not finish within %s of the round deadline", grace)), nil, false
	}
}

// timedOutReport is the synthetic report for a probe that never came back.
//
// Reachable stays false with the reason in WriteErr, which Aggregate turns into
// unreachable - the taxonomy's own words for "up, but cannot reach the storage".
// Storing nothing instead would leave this Core reading as no-response, which
// tells the operator to retry and read a Core's logs when what actually
// happened is that the backend stopped answering.
func timedOutReport(opts ProbeOptions, detail string) Report {
	return Report{
		CoreID:      opts.CoreID,
		Fingerprint: opts.Fingerprint,
		WriteErr:    detail,
		At:          time.Now().Unix(),
		// Non-nil for the same reason Probe does it: the panel renders these
		// lists directly, and null is not a list.
		SeenPeers:        []string{},
		MismatchedPeers:  []string{},
		CrossWroteTo:     []string{},
		CrossWriteDenied: []string{},
	}
}

// publishRound writes the round metadata and the staged config, then the
// current-round key that every Core polls for.
//
// Order matters, and the current-round key going LAST is the whole of it: a
// Core that finds it must find both other keys already there, or it reports
// no-response for a round that was perfectly fine. Discovery is polling only -
// a round's window is 15s and the service loop polls every second, so a Core
// sees an open round within one tick of it opening.
func publishRound(ctx context.Context, rdb *redis.Client, meta roundMeta, cfg Config) error {
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := rdb.Set(ctx, roundMetaKey(meta.RoundID), metaJSON, roundTTL).Err(); err != nil {
		return fmt.Errorf("storagereach: publish round meta: %w", err)
	}
	if err := rdb.Set(ctx, roundConfigKey(meta.RoundID), cfgJSON, configTTL).Err(); err != nil {
		return fmt.Errorf("storagereach: stage round config: %w", err)
	}
	if err := rdb.Set(ctx, currentRoundKey, meta.RoundID, roundTTL).Err(); err != nil {
		return fmt.Errorf("storagereach: publish current round: %w", err)
	}
	return nil
}

// PendingRoundID returns the round a Core should take part in, or "" when
// none is running.
func PendingRoundID(ctx context.Context, rdb *redis.Client) (string, error) {
	id, err := rdb.Get(ctx, currentRoundKey).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("storagereach: read current round: %w", err)
	}
	return id, nil
}

func storeReport(ctx context.Context, rdb *redis.Client, roundID string, rep Report) {
	data, err := json.Marshal(rep)
	if err != nil {
		log.Printf("storagereach: marshal report for %s: %v", rep.CoreID, err)
		return
	}
	if err := rdb.Set(ctx, roundReportKey(roundID, rep.CoreID), data, roundTTL).Err(); err != nil {
		log.Printf("storagereach: store report for %s: %v", rep.CoreID, err)
	}
}

// collectReports reads back whatever has landed so far. A missing or corrupt
// report is simply absent, which Aggregate turns into no-response - the
// fail-closed answer.
func collectReports(ctx context.Context, rdb *redis.Client, roundID string, participants []string) map[string]Report {
	out := make(map[string]Report, len(participants))
	for _, id := range participants {
		val, err := rdb.Get(ctx, roundReportKey(roundID, id)).Result()
		if err != nil {
			continue
		}
		var rep Report
		if err := json.Unmarshal([]byte(val), &rep); err != nil {
			continue
		}
		out[id] = rep
	}
	return out
}

// newRoundID returns an unguessable token. Unguessable matters: the round id
// is the token every beacon carries, so a predictable id would let a stale or
// planted beacon be accepted as proof for a later round.
func newRoundID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("storagereach: round id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
