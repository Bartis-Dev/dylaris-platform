package metrics

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

type fakeStore struct {
	mu   sync.Mutex
	rows []Row
	err  error
	// block, if set, holds Upsert until it is closed - used to prove Observe
	// does not wait on the store.
	block chan struct{}
	calls int
}

func (f *fakeStore) Upsert(_ context.Context, rows []Row) error {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, rows...)
	return nil
}

func (f *fakeStore) snapshot() []Row {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Row, len(f.rows))
	copy(out, f.rows)
	return out
}

func find(t *testing.T, rows []Row, metric string) Row {
	t.Helper()
	for _, r := range rows {
		if r.Key.Metric == metric {
			return r
		}
	}
	t.Fatalf("no row for metric %q in %d rows", metric, len(rows))
	return Row{}
}

func TestBucketAddAndMerge(t *testing.T) {
	tests := []struct {
		name          string
		values        []float64
		min, max, sum float64
		count         int64
		avg           float64
	}{
		{"empty", nil, 0, 0, 0, 0, 0},
		{"one", []float64{5}, 5, 5, 5, 1, 5},
		{"ascending", []float64{1, 2, 3}, 1, 3, 6, 3, 2},
		{"descending", []float64{3, 2, 1}, 1, 3, 6, 3, 2},
		// A measured zero is not "nothing measured". Without the Count == 0
		// branch in Add, the first observation would leave Min at the zero
		// value of the struct and a series that never goes below 5 would
		// report a minimum of 0 forever.
		{"all zero", []float64{0, 0}, 0, 0, 0, 2, 0},
		{"negative", []float64{-2, 4}, -2, 4, 2, 2, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var b Bucket
			for _, v := range tc.values {
				b.Add(v)
			}
			if b.Min != tc.min || b.Max != tc.max || b.Sum != tc.sum || b.Count != tc.count {
				t.Fatalf("got min=%v max=%v sum=%v count=%d, want %v/%v/%v/%d",
					b.Min, b.Max, b.Sum, b.Count, tc.min, tc.max, tc.sum, tc.count)
			}
			if math.Abs(b.Avg()-tc.avg) > 1e-9 {
				t.Fatalf("avg = %v, want %v", b.Avg(), tc.avg)
			}
		})
	}
}

func TestBucketMergeIsTheSameArithmeticAsAdding(t *testing.T) {
	// The whole design rests on this: a bucket written in two halves must equal
	// the same bucket written in one go, or splitting a flush changes the
	// recorded history.
	var whole Bucket
	for _, v := range []float64{4, 9, 1, 7} {
		whole.Add(v)
	}
	var a, b Bucket
	a.Add(4)
	a.Add(9)
	b.Add(1)
	b.Add(7)
	got := a.Merge(b)
	if got != whole {
		t.Fatalf("merged %+v != whole %+v", got, whole)
	}
	// And merging is commutative, because flush order is not guaranteed.
	if b.Merge(a) != whole {
		t.Fatalf("merge is not commutative")
	}
}

func TestMergeWithAnEmptyBucketKeepsTheOther(t *testing.T) {
	var full Bucket
	full.Add(3)
	if got := full.Merge(Bucket{}); got != full {
		t.Fatalf("merging an empty bucket changed the result: %+v", got)
	}
	if got := (Bucket{}).Merge(full); got != full {
		t.Fatalf("merging into an empty bucket lost the values: %+v", got)
	}
	// Not just cosmetic: without the Count == 0 guards, min would come out 0
	// for a series whose values are all positive.
	if full.Merge(Bucket{}).Min != 3 {
		t.Fatalf("an empty bucket dragged the minimum to zero")
	}
}

func TestBucketStartIsUTCAndAligned(t *testing.T) {
	// Two Cores in different zones must produce the SAME bucket for the same
	// instant, or the UPSERT that is meant to merge them inserts a second row.
	east := time.FixedZone("UTC+5", 5*3600)
	inst := time.Date(2026, 9, 2, 14, 37, 42, 0, time.UTC)
	a := BucketStart(inst, time.Hour)
	b := BucketStart(inst.In(east), time.Hour)
	if !a.Equal(b) {
		t.Fatalf("zone changed the bucket: %v vs %v", a, b)
	}
	if a.Minute() != 0 || a.Second() != 0 {
		t.Fatalf("hour bucket not aligned: %v", a)
	}
	if m := BucketStart(inst, time.Minute); m.Second() != 0 || m.Minute() != 37 {
		t.Fatalf("minute bucket wrong: %v", m)
	}
}

func TestObserveAccumulatesPerSeriesAndBucket(t *testing.T) {
	fs := &fakeStore{}
	r := NewRecorder(fs, time.Hour)
	base := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)

	r.Observe(Key{Metric: "a"}, 1, base)
	r.Observe(Key{Metric: "a"}, 3, base.Add(30*time.Minute)) // same hour
	r.Observe(Key{Metric: "a"}, 9, base.Add(time.Hour))      // next hour
	r.Observe(Key{Metric: "b", Subject: "s1"}, 5, base)
	// Same metric, different subject: a separate series, not the same one.
	r.Observe(Key{Metric: "b", Subject: "s2"}, 100, base)

	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows := fs.snapshot()
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows (2 buckets of a, 2 subjects of b), got %d", len(rows))
	}

	var firstHourA Bucket
	var nextHourA Bucket
	subjects := map[string]float64{}
	for _, row := range rows {
		switch {
		case row.Key.Metric == "a" && row.Time.Equal(base):
			firstHourA = row.Bucket
		case row.Key.Metric == "a":
			nextHourA = row.Bucket
		case row.Key.Metric == "b":
			subjects[row.Key.Subject] = row.Bucket.Sum
		}
	}
	if firstHourA.Count != 2 || firstHourA.Min != 1 || firstHourA.Max != 3 || firstHourA.Sum != 4 {
		t.Fatalf("first hour of a: %+v", firstHourA)
	}
	if nextHourA.Count != 1 || nextHourA.Sum != 9 {
		t.Fatalf("second hour of a: %+v", nextHourA)
	}
	if subjects["s1"] != 5 || subjects["s2"] != 100 {
		t.Fatalf("subjects were merged together: %v", subjects)
	}
}

func TestRegionIsPartOfTheIdentity(t *testing.T) {
	fs := &fakeStore{}
	r := NewRecorder(fs, time.Hour)
	now := time.Now()
	r.Observe(Key{Metric: "edge.rx", Region: "eu"}, 10, now)
	r.Observe(Key{Metric: "edge.rx", Region: "sg"}, 20, now)
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(fs.snapshot()); got != 2 {
		t.Fatalf("regions collapsed into %d row(s); a per-region number that is not per-region is a wrong number", got)
	}
}

func TestFlushClearsPendingSoNothingIsWrittenTwice(t *testing.T) {
	fs := &fakeStore{}
	r := NewRecorder(fs, time.Hour)
	r.ObserveNow(Key{Metric: "a"}, 1)

	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(fs.snapshot()); got != 1 {
		t.Fatalf("the same bucket was written %d times; the store merges, so a double write doubles the count", got)
	}
	if fs.calls != 1 {
		t.Fatalf("an empty flush still called the store %d times", fs.calls)
	}
}

func TestAFailedFlushDropsTheBatchAndDoesNotRetryIt(t *testing.T) {
	// The design promise: a metrics store that is down must never block
	// anything and must never grow a backlog. What is lost is the batch, not
	// the bucket - the store merges, so the next flush of the same bucket
	// lands correctly.
	fs := &fakeStore{err: errors.New("down")}
	r := NewRecorder(fs, time.Hour)
	r.ObserveNow(Key{Metric: "a"}, 1)

	if err := r.Flush(context.Background()); err == nil {
		t.Fatal("expected the failure to be reported to the caller")
	}
	if got := r.Stats().Pending; got != 0 {
		t.Fatalf("%d rows were queued for retry; that is the backlog this must not build", got)
	}
	if got := r.Stats().FailedRows; got != 1 {
		t.Fatalf("FailedRows = %d, want 1 - a silent drop is worse than a loud one", got)
	}

	fs.mu.Lock()
	fs.err = nil
	fs.mu.Unlock()
	r.ObserveNow(Key{Metric: "a"}, 5)
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows := fs.snapshot()
	if len(rows) != 1 || rows[0].Bucket.Sum != 5 {
		t.Fatalf("recovery did not resume cleanly: %+v", rows)
	}
}

func TestObserveDoesNotWaitOnTheStore(t *testing.T) {
	// If Observe held the lock across the write, every producer on a hot path
	// would stall for as long as the database takes. Flush takes the map and
	// releases before writing, so this must return while a write is in flight.
	fs := &fakeStore{block: make(chan struct{})}
	r := NewRecorder(fs, time.Hour)
	r.ObserveNow(Key{Metric: "a"}, 1)

	done := make(chan struct{})
	go func() { _ = r.Flush(context.Background()); close(done) }()

	observed := make(chan struct{})
	go func() { r.ObserveNow(Key{Metric: "b"}, 2); close(observed) }()

	select {
	case <-observed:
	case <-time.After(2 * time.Second):
		t.Fatal("Observe blocked behind an in-flight store write")
	}
	close(fs.block)
	<-done
}

func TestTheCapDropsNewSeriesRatherThanGrowingForever(t *testing.T) {
	// A store that has been unreachable for hours must not turn into unbounded
	// memory in the one component that has to stay up.
	fs := &fakeStore{err: errors.New("down")}
	r := NewRecorder(fs, time.Hour)
	r.maxPending = 2

	r.ObserveNow(Key{Metric: "a"}, 1)
	r.ObserveNow(Key{Metric: "b"}, 1)
	r.ObserveNow(Key{Metric: "c"}, 1) // refused: new series past the cap
	r.ObserveNow(Key{Metric: "a"}, 2) // accepted: existing series keeps filling

	st := r.Stats()
	if st.Pending != 2 {
		t.Fatalf("pending = %d, want 2 - the cap did not hold", st.Pending)
	}
	if st.DroppedOverCap != 1 {
		t.Fatalf("DroppedOverCap = %d, want 1", st.DroppedOverCap)
	}
	fs.mu.Lock()
	fs.err = nil
	fs.mu.Unlock()
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The existing series kept accumulating while the cap was in force, which
	// is the point: losing one whole series beats losing a random slice of
	// every series.
	if got := find(t, fs.snapshot(), "a").Bucket; got.Count != 2 || got.Sum != 3 {
		t.Fatalf("existing series lost samples under the cap: %+v", got)
	}
}

func TestObserveIgnoresAnEmptyMetricName(t *testing.T) {
	// Otherwise every miswired producer collects into one nameless series that
	// looks like real data.
	fs := &fakeStore{}
	r := NewRecorder(fs, time.Hour)
	r.ObserveNow(Key{Subject: "s"}, 1)
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(fs.snapshot()); got != 0 {
		t.Fatalf("an unnamed metric was recorded (%d rows)", got)
	}
}

func TestRunFlushesOnShutdown(t *testing.T) {
	// A redeploy must not lose the minutes since the last tick.
	fs := &fakeStore{}
	r := NewRecorder(fs, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx, time.Hour); close(done) }()

	r.ObserveNow(Key{Metric: "a"}, 42)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	rows := fs.snapshot()
	if len(rows) != 1 || rows[0].Bucket.Sum != 42 {
		t.Fatalf("shutdown flush lost the bucket: %+v", rows)
	}
}

func TestNilRecorderIsSafe(t *testing.T) {
	// Metrics are optional. A producer must be able to hold a nil recorder and
	// call it without a guard at every call site - a guard everyone has to
	// remember is a guard somebody forgets.
	var r *Recorder
	r.ObserveNow(Key{Metric: "a"}, 1)
}
