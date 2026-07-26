package storagereach

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

// RunParticipant runs this Core's side of a round: read the metadata and the
// staged candidate config, build a provider for it, probe, and write the
// report back.
//
// The candidate config comes from Redis rather than this Core's own settings
// on purpose. A config round tests a backend that has NOT been persisted yet
// (fail-closed: the save happens only after the round passes), so a
// participant probing its own stored config would prove the wrong thing.
//
// A round that has already expired is not an error: the coordinator gave up on
// it, and reporting late would only write a key nobody reads.
func RunParticipant(ctx context.Context, rdb *redis.Client, coreID, roundID string, newProvider ProviderFactory) error {
	metaJSON, err := rdb.Get(ctx, roundMetaKey(roundID)).Result()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		return err
	}
	var meta roundMeta
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		return err
	}
	if meta.Coordinator == coreID {
		// The coordinator probes inline in RunRound; probing again here would
		// write a second beacon and double the work for no new evidence.
		return nil
	}
	if !contains(meta.Participants, coreID) {
		// Not in this round's participant set - it was opened before this
		// Core's heartbeat landed. Its own boot self-check covers it.
		return nil
	}

	cfgJSON, err := rdb.Get(ctx, roundConfigKey(roundID)).Result()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg Config
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		return err
	}

	budget := time.Until(time.UnixMilli(meta.DeadlineUnixMilli))
	if budget <= 0 {
		return nil
	}
	// Retry at the coordinator's own cadence: a participant that re-listed
	// slower than the round's chosen pace could overshoot a short deadline by
	// a whole extra cycle, which is exactly the kind of multi-second test
	// stall that a mismatched hardcoded interval would otherwise cause.
	retryEvery := time.Duration(meta.PollEveryMillis) * time.Millisecond
	if retryEvery <= 0 {
		retryEvery = time.Second
	}

	prov, provErr := newProvider(cfg)
	if provErr != nil {
		// Reporting the failure is the point: a Core whose credentials do not
		// work must say "unreachable, because X", not stay silent and be read
		// as no-response, which sends the operator looking at the network.
		storeReport(ctx, rdb, roundID, Report{
			CoreID: coreID, Fingerprint: meta.Fingerprint,
			WriteErr: provErr.Error(), At: time.Now().Unix(),
			SeenPeers: []string{}, MismatchedPeers: []string{},
			CrossWroteTo: []string{}, CrossWriteDenied: []string{},
		})
		return nil
	}

	// Bound the probe's individual backend CALLS to the round window, not just
	// the gaps between them: Probe checks its deadline only between calls, so
	// without this nothing bounds a single one and a call that never returns on
	// its own runs long past the round it belongs to. The S3 SDK genuinely
	// aborts on a context (every S3Provider method threads the caller's ctx into
	// aws-sdk-go-v2); a host-path syscall does not, and the service loop's own
	// round watchdog (service.go) is what covers that case.
	probeCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	rep := Probe(probeCtx, prov, ProbeOptions{
		CoreID: coreID, RoundID: roundID, Fingerprint: meta.Fingerprint,
		Participants: meta.Participants, Deadline: budget, RetryEvery: retryEvery,
	})
	// ctx, not probeCtx: the report write must not be cancelled by the probe's
	// own budget, or a participant that ran the window out would have nothing to
	// show for it and read as no-response.
	storeReport(ctx, rdb, roundID, rep)
	return nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
