package metrics

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Store is where flushed buckets go. Implemented by the SQL store; a test
// supplies its own.
type Store interface {
	// Upsert merges rows into the destination. It MUST be a merge, not an
	// insert: a bucket is written more than once as it fills, and the second
	// write has to combine with the first rather than duplicate or replace it.
	Upsert(ctx context.Context, rows []Row) error
}

// Recorder accumulates observations in memory and flushes them periodically.
//
// **A metrics store that is down must never block anything.** These are the
// least important bytes in the system and they behave that way: a failed flush
// DROPS the batch. It is not queued and it is not retried into a backlog,
// because the alternative is unbounded memory in the one component that must
// stay up. What is lost is the samples in that batch; the bucket itself is not
// lost, because the store merges on conflict and the next flush of the same
// bucket lands correctly.
type Recorder struct {
	store Store
	res   time.Duration
	// maxPending bounds memory when the store has been unreachable for a long
	// time. Past it, NEW series are dropped while existing ones keep
	// accumulating - losing a whole series is more honest than losing a random
	// slice of every series.
	maxPending int

	mu      sync.Mutex
	pending map[pendingKey]Bucket

	dropped  atomic.Uint64 // observations refused by the cap
	failed   atomic.Uint64 // rows discarded by a failed flush
	written  atomic.Uint64
	lastErr  atomic.Pointer[string]
	loggedAt time.Time
}

type pendingKey struct {
	Key
	at int64 // bucket start, unix seconds
}

const defaultMaxPending = 200000

// NewRecorder returns a recorder writing buckets of size res.
func NewRecorder(s Store, res time.Duration) *Recorder {
	if res <= 0 {
		res = time.Hour
	}
	return &Recorder{
		store:      s,
		res:        res,
		maxPending: defaultMaxPending,
		pending:    make(map[pendingKey]Bucket),
	}
}

// Resolution is the bucket size this recorder writes.
func (r *Recorder) Resolution() time.Duration { return r.res }

// Observe folds one value into the bucket t belongs to.
//
// Cheap and lock-only: no I/O, so a caller on a hot path is never waiting on a
// database. An empty metric name is ignored rather than stored under "", which
// would silently collect every miswired producer into one series.
func (r *Recorder) Observe(k Key, v float64, t time.Time) {
	if r == nil || k.Metric == "" {
		return
	}
	pk := pendingKey{Key: k, at: BucketStart(t, r.res).Unix()}

	r.mu.Lock()
	defer r.mu.Unlock()
	b, exists := r.pending[pk]
	if !exists && len(r.pending) >= r.maxPending {
		r.dropped.Add(1)
		return
	}
	b.Add(v)
	r.pending[pk] = b
}

// ObserveNow is Observe at the current time.
func (r *Recorder) ObserveNow(k Key, v float64) { r.Observe(k, v, time.Now()) }

// Flush writes everything accumulated so far.
//
// The pending map is taken and cleared BEFORE the write, so a slow store never
// blocks Observe and a concurrent flush cannot write the same rows twice. On
// failure the batch is gone - see the note on Recorder.
func (r *Recorder) Flush(ctx context.Context) error {
	r.mu.Lock()
	if len(r.pending) == 0 {
		r.mu.Unlock()
		return nil
	}
	batch := r.pending
	r.pending = make(map[pendingKey]Bucket, len(batch))
	r.mu.Unlock()

	rows := make([]Row, 0, len(batch))
	for pk, b := range batch {
		rows = append(rows, Row{Time: time.Unix(pk.at, 0).UTC(), Key: pk.Key, Bucket: b})
	}

	if err := r.store.Upsert(ctx, rows); err != nil {
		r.failed.Add(uint64(len(rows)))
		msg := err.Error()
		r.lastErr.Store(&msg)
		// Once a minute at most: a store that is down produces one of these per
		// flush, and a log nobody can read past is its own outage.
		if time.Since(r.loggedAt) > time.Minute {
			r.loggedAt = time.Now()
			log.Printf("metrics: dropped %d rows, store write failed: %v", len(rows), err)
		}
		return err
	}
	r.written.Add(uint64(len(rows)))
	return nil
}

// Run flushes every interval until ctx ends, then flushes once more.
//
// The final flush is why a redeploy does not lose the minutes since the last
// one. It uses a fresh short-lived context because ctx is already cancelled by
// the time it runs.
func (r *Recorder) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			done, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = r.Flush(done)
			cancel()
			return
		case <-t.C:
			_ = r.Flush(ctx)
		}
	}
}

// Stats reports what the recorder has done, for the health screen.
type Stats struct {
	Pending        int
	Written        uint64
	DroppedOverCap uint64
	FailedRows     uint64
	LastError      string
}

func (r *Recorder) Stats() Stats {
	r.mu.Lock()
	n := len(r.pending)
	r.mu.Unlock()
	s := Stats{
		Pending:        n,
		Written:        r.written.Load(),
		DroppedOverCap: r.dropped.Load(),
		FailedRows:     r.failed.Load(),
	}
	if p := r.lastErr.Load(); p != nil {
		s.LastError = *p
	}
	return s
}
