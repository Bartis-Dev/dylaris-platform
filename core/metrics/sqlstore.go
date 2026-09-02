package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
)

// SQLStore writes buckets to Postgres, with or without TimescaleDB.
//
// The two backends share one table and one write path. Timescale only adds
// chunking and compression on top, so a self-hoster on plain Postgres runs the
// same code with the same results at a coarser resolution - the fallback is
// what most installations get, not a degraded mode bolted on for them.
type SQLStore struct {
	db *sql.DB
	// batch bounds one statement. A flush after a long outage can carry tens of
	// thousands of rows, and one statement with 8 parameters each would exceed
	// Postgres' 65535-parameter limit long before that.
	batch int
}

const defaultBatch = 2000

func NewSQLStore(db *sql.DB) *SQLStore { return &SQLStore{db: db, batch: defaultBatch} }

// EnsureSchema creates the table and its unique index, and on Timescale also
// the hypertable and a compression policy.
//
// Safe to call on every boot. Every Timescale step is non-fatal: a database
// that turns out not to have the extension falls back to a plain table, which
// is the same shape and the same queries, only larger.
func EnsureSchema(ctx context.Context, db *sql.DB, useTimescale bool) error {
	// region and subject are NOT NULL with an empty default, and that is
	// load-bearing rather than tidiness: in Postgres a NULL is never equal to
	// another NULL, so a unique index over a nullable column does NOT dedupe
	// the rows that hold one. The whole design rests on ON CONFLICT finding the
	// existing bucket, and with nullable columns every flush of a region-less
	// series would insert a new row instead of merging.
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS metric_samples (
		time    TIMESTAMPTZ NOT NULL,
		metric  TEXT NOT NULL,
		subject TEXT NOT NULL DEFAULT '',
		region  TEXT NOT NULL DEFAULT '',
		min_v   DOUBLE PRECISION NOT NULL,
		max_v   DOUBLE PRECISION NOT NULL,
		sum_v   DOUBLE PRECISION NOT NULL,
		count_v BIGINT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create metric_samples: %w", err)
	}

	// The conflict target. `time` is last in the column order but present, which
	// Timescale requires of any unique index on a hypertable - the partitioning
	// column has to be part of it.
	if _, err := db.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS metric_samples_series ON metric_samples (metric, subject, region, time)`,
	); err != nil {
		return fmt.Errorf("create metric_samples index: %w", err)
	}
	// Reading is almost always "this series over this window".
	if _, err := db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS metric_samples_time ON metric_samples (time DESC)`,
	); err != nil {
		return fmt.Errorf("create metric_samples time index: %w", err)
	}

	if !useTimescale {
		log.Println("metrics: metric_samples is a plain table")
		return nil
	}

	if _, err := db.ExecContext(ctx,
		`SELECT create_hypertable('metric_samples', 'time', if_not_exists => TRUE)`); err != nil {
		log.Printf("metrics: hypertable not created (extension may be missing; the plain table works): %v", err)
		return nil
	}
	log.Println("metrics: hypertable 'metric_samples' ready")

	if _, err := db.ExecContext(ctx,
		`ALTER TABLE metric_samples SET (timescaledb.compress, timescaledb.compress_segmentby = 'metric, subject, region')`); err != nil {
		log.Printf("metrics: compression not enabled: %v", err)
		return nil
	}
	// Seven days, and the number matters. A bucket is UPSERTed repeatedly while
	// it fills, and a compressed chunk is not somewhere you want those writes
	// landing. Nothing is still being written to a chunk a week old, so
	// compression only ever touches history that has stopped changing.
	if _, err := db.ExecContext(ctx,
		`SELECT add_compression_policy('metric_samples', INTERVAL '7 days', if_not_exists => TRUE)`); err != nil {
		log.Printf("metrics: compression policy not set: %v", err)
		return nil
	}
	log.Println("metrics: compression active on chunks older than 7 days")

	// Deliberately NO retention policy. The whole point of this table is that
	// it survives; the raw 24-hour tables are where short-lived detail lives.
	return nil
}

// Upsert merges rows, in batches.
//
// ON CONFLICT does the same arithmetic Bucket.Merge does in memory: the smaller
// minimum, the larger maximum, and the sums and counts added. That is what lets
// a bucket be written many times while it fills without the history changing.
func (s *SQLStore) Upsert(ctx context.Context, rows []Row) error {
	for start := 0; start < len(rows); start += s.batch {
		end := start + s.batch
		if end > len(rows) {
			end = len(rows)
		}
		if err := s.upsertBatch(ctx, rows[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLStore) upsertBatch(ctx context.Context, rows []Row) error {
	if len(rows) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString(`INSERT INTO metric_samples (time, metric, subject, region, min_v, max_v, sum_v, count_v) VALUES `)
	args := make([]any, 0, len(rows)*8)
	for i, r := range rows {
		if i > 0 {
			sb.WriteByte(',')
		}
		n := i * 8
		fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)", n+1, n+2, n+3, n+4, n+5, n+6, n+7, n+8)
		args = append(args, r.Time.UTC(), r.Key.Metric, r.Key.Subject, r.Key.Region,
			r.Bucket.Min, r.Bucket.Max, r.Bucket.Sum, r.Bucket.Count)
	}
	sb.WriteString(` ON CONFLICT (metric, subject, region, time) DO UPDATE SET
		min_v   = LEAST(metric_samples.min_v, EXCLUDED.min_v),
		max_v   = GREATEST(metric_samples.max_v, EXCLUDED.max_v),
		sum_v   = metric_samples.sum_v + EXCLUDED.sum_v,
		count_v = metric_samples.count_v + EXCLUDED.count_v`)

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("upsert %d metric rows: %w", len(rows), err)
	}
	return nil
}
