package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"
)

// Resolution is the bucket size each backend records at.
//
// This is the whole difference between the two deployments, and it is decided
// by whether a dedicated database was configured rather than by a separate
// knob. A knob would let someone ask for minute resolution into the Core
// database, which is a query nobody waits for a year later - about 79 million
// rows a year at a modest fleet size, on a database that cannot chunk them.
const (
	ResolutionDedicated = time.Minute
	ResolutionShared    = time.Hour
)

// FlushInterval is how often accumulated buckets are written.
//
// Well inside both resolutions on purpose: a bucket is written repeatedly while
// it fills, so a Core that restarts mid-bucket loses at most this much rather
// than the whole bucket. It stays one row either way, because the write merges.
const FlushInterval = 5 * time.Minute

// Handle is a configured recorder plus whatever has to be closed with it.
type Handle struct {
	Recorder *Recorder
	// Dedicated is the separate connection, or nil when the buckets go into
	// the Core database and the caller owns that pool.
	Dedicated *sql.DB
	// Resolution is the bucket size actually in use.
	Resolution time.Duration
	// Read is the pool to QUERY through - the dedicated one when there is one,
	// otherwise Core's. Held separately from Dedicated so a reader never has to
	// know which backend it got, and never has to guess that a nil Dedicated
	// means "use the other handle".
	Read *sql.DB
}

func (h *Handle) Close() error {
	if h == nil || h.Dedicated == nil {
		return nil
	}
	return h.Dedicated.Close()
}

// Open prepares the recorder.
//
// metricsURL empty means "use coreDB at hour resolution", which is the
// supported default and what a self-hoster gets. A non-empty URL opens its own
// pool and switches to minute resolution.
//
// An unreachable dedicated database is NOT fatal. It is reported and the
// platform starts without long-term metrics, because a statistics store must
// never be a reason Core does not come up.
func Open(ctx context.Context, coreDB *sql.DB, metricsURL string, coreUsesTimescale bool) (*Handle, error) {
	if metricsURL == "" {
		if coreDB == nil {
			return nil, fmt.Errorf("no database to record metrics into")
		}
		if err := EnsureSchema(ctx, coreDB, coreUsesTimescale); err != nil {
			return nil, err
		}
		log.Printf("metrics: recording into the core database at %s resolution", ResolutionShared)
		return &Handle{
			Recorder:   NewRecorder(NewSQLStore(coreDB), ResolutionShared),
			Resolution: ResolutionShared,
			Read:       coreDB,
		}, nil
	}

	db, err := sql.Open("postgres", metricsURL)
	if err != nil {
		return nil, fmt.Errorf("open metrics database: %w", err)
	}
	// Small on purpose. This pool serves one writer flushing every few minutes;
	// sizing it like a request-serving pool would hold connections a metrics
	// database has no use for.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)

	ping, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(ping); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("reach metrics database: %w", err)
	}
	// A dedicated metrics database is a Timescale one in every deployment we
	// build for; if the extension turns out to be missing, EnsureSchema logs it
	// and the plain table carries on.
	if err := EnsureSchema(ctx, db, true); err != nil {
		_ = db.Close()
		return nil, err
	}
	log.Printf("metrics: recording into the dedicated database at %s resolution", ResolutionDedicated)
	return &Handle{
		Recorder:   NewRecorder(NewSQLStore(db), ResolutionDedicated),
		Dedicated:  db,
		Resolution: ResolutionDedicated,
		Read:       db,
	}, nil
}
