package storagereach

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"dylaris-core/storage"

	"github.com/redis/go-redis/v9"
)

// RoundsChannel carries a round id to every Core the moment a round opens.
// It is the LATENCY path only: currentRoundKey is the source of truth, because
// a fire-and-forget publish is exactly what a Core in a GC pause misses.
const RoundsChannel = "dylaris:storagereach:rounds"

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
// tests can hand every Core a LocalProvider on one root.
type ProviderFactory func(cfg Config) (storage.StorageProvider, error)

// Coordinator runs a config-time round: stage, publish, probe locally, collect,
// aggregate, clean up.
type Coordinator struct {
	rdb         *redis.Client
	coreID      string
	newProvider ProviderFactory
}

func NewCoordinator(rdb *redis.Client, coreID string, newProvider ProviderFactory) *Coordinator {
	return &Coordinator{rdb: rdb, coreID: coreID, newProvider: newProvider}
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
	// OnProgress, when set, is called with each new partial result so a caller
	// can stream an X/N counter. Never called concurrently.
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
	// config key first and the current-round key last: if it fails partway
	// through (e.g. Redis drops the connection between those two SETs), the
	// config key can already be sitting in Redis when we return the error
	// below, and it still needs to come out rather than wait out its TTL.
	defer func() {
		if err := c.rdb.Del(context.WithoutCancel(ctx), roundConfigKey(roundID), currentRoundKey).Err(); err != nil {
			log.Printf("storagereach: could not clear round %s staging keys: %v", roundID, err)
		}
	}()
	if err := publishRound(ctx, c.rdb, meta, cfg); err != nil {
		return RoundResult{}, err
	}

	// The coordinator is a participant like any other: it must prove its own
	// access, not assume it.
	prov, provErr := c.newProvider(cfg)
	if provErr == nil {
		rep := Probe(ctx, prov, ProbeOptions{
			CoreID: c.coreID, RoundID: roundID, Fingerprint: fingerprint,
			Participants: participants, Deadline: opts.Deadline, RetryEvery: opts.PollEvery,
		})
		storeReport(ctx, c.rdb, roundID, rep)
		defer func() {
			if err := CleanupRound(context.WithoutCancel(ctx), prov, roundID); err != nil {
				// Orphans age out and are ignored by later rounds, which key
				// off the round id in every beacon.
				log.Printf("storagereach: probe cleanup for round %s: %v", roundID, err)
			}
		}()
	} else {
		storeReport(ctx, c.rdb, roundID, Report{
			CoreID: c.coreID, Fingerprint: fingerprint, WriteErr: provErr.Error(), At: time.Now().Unix(),
		})
	}

	var last RoundResult
	for {
		reports := collectReports(ctx, c.rdb, roundID, participants)
		expired := !time.Now().Before(deadline)
		complete := len(reports) == len(participants)

		last = Aggregate(participants, reports, fingerprint, complete || expired)
		last.RoundID = roundID
		if opts.OnProgress != nil {
			opts.OnProgress(last)
		}
		if complete || expired {
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

// publishRound writes the round metadata and the staged config, then signals.
// Order matters: a Core woken by the publish must find both keys already
// there, or it reports no-response for a round that was perfectly fine.
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
	// Best-effort: currentRoundKey above already guarantees discovery.
	if err := rdb.Publish(ctx, RoundsChannel, meta.RoundID).Err(); err != nil {
		log.Printf("storagereach: round %s publish signal failed, peers will find it by poll: %v", meta.RoundID, err)
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
