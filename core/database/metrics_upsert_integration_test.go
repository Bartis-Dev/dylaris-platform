package database

import (
	"context"
	"dylaris-core/metrics"
	"fmt"
	"testing"
	"time"
)

// The whole long-term record rests on ON CONFLICT merging rather than
// inserting, and that is a claim about Postgres, not about our code - no fake
// can prove it. Two things in particular only fail against a real database:
//
//   - `region` and `subject` are NOT NULL with an empty default. In Postgres a
//     NULL never equals another NULL, so over nullable columns the unique index
//     would not dedupe and every flush of a region-less series would INSERT a
//     new row. The merge would silently stop merging and a year of history would
//     be a pile of fragments.
//   - LEAST/GREATEST/+ have to reproduce Bucket.Merge exactly, or a bucket
//     written in two flushes differs from the same bucket written in one.
func TestIntegrationMetricUpsertMergesInsteadOfDuplicating(t *testing.T) {
	db, _ := integrationDB(t)
	ctx := context.Background()

	if err := metrics.EnsureSchema(ctx, db, false); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	// A metric name unique to this run, so a repeated local run against the same
	// database does not read another run's rows.
	metric := fmt.Sprintf("test.merge.%d", time.Now().UnixNano())
	t.Cleanup(func() { db.Exec(`DELETE FROM metric_samples WHERE metric = $1`, metric) })

	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	st := metrics.NewSQLStore(db)

	// Region deliberately empty: this is the case a nullable column would break.
	key := metrics.Key{Metric: metric, Subject: "s1"}

	first := metrics.Bucket{Min: 4, Max: 9, Sum: 13, Count: 2}
	second := metrics.Bucket{Min: 1, Max: 7, Sum: 8, Count: 2}

	if err := st.Upsert(ctx, []metrics.Row{{Time: at, Key: key, Bucket: first}}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := st.Upsert(ctx, []metrics.Row{{Time: at, Key: key, Bucket: second}}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM metric_samples WHERE metric = $1`, metric).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("two flushes of one bucket produced %d rows; the merge is not merging", rows)
	}

	var min, max, sum float64
	var count int64
	if err := db.QueryRow(
		`SELECT min_v, max_v, sum_v, count_v FROM metric_samples WHERE metric = $1`, metric,
	).Scan(&min, &max, &sum, &count); err != nil {
		t.Fatal(err)
	}

	// Exactly what Bucket.Merge produces in memory. The two paths agreeing is
	// the property; a difference would mean history changes depending on where
	// a flush boundary happened to fall.
	want := first.Merge(second)
	if min != want.Min || max != want.Max || sum != want.Sum || count != want.Count {
		t.Fatalf("stored min=%v max=%v sum=%v count=%d, in-memory merge says %+v", min, max, sum, count, want)
	}
}

func TestIntegrationMetricSeriesAreKeptApart(t *testing.T) {
	db, _ := integrationDB(t)
	ctx := context.Background()
	if err := metrics.EnsureSchema(ctx, db, false); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	metric := fmt.Sprintf("test.split.%d", time.Now().UnixNano())
	t.Cleanup(func() { db.Exec(`DELETE FROM metric_samples WHERE metric = $1`, metric) })

	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	st := metrics.NewSQLStore(db)
	b := metrics.Bucket{Min: 1, Max: 1, Sum: 1, Count: 1}

	// Four rows that differ only in one identity field each, plus one repeat of
	// the first. Anything left out of the conflict target would collapse two of
	// these into one and quietly halve a number.
	rows := []metrics.Row{
		{Time: at, Key: metrics.Key{Metric: metric, Subject: "a", Region: "eu"}, Bucket: b},
		{Time: at, Key: metrics.Key{Metric: metric, Subject: "b", Region: "eu"}, Bucket: b},
		{Time: at, Key: metrics.Key{Metric: metric, Subject: "a", Region: "sg"}, Bucket: b},
		{Time: at, Key: metrics.Key{Metric: metric, Subject: "a", Region: ""}, Bucket: b},
		{Time: at.Add(time.Hour), Key: metrics.Key{Metric: metric, Subject: "a", Region: "eu"}, Bucket: b},
	}
	if err := st.Upsert(ctx, rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM metric_samples WHERE metric = $1`, metric).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(rows) {
		t.Fatalf("got %d rows for %d distinct series/buckets; some were merged together", n, len(rows))
	}
}
