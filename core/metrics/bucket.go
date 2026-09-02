// Package metrics records long-lived platform statistics.
//
// It is deliberately NOT the live view. Redis already carries seconds-fresh
// numbers for every screen that shows "right now", and `server_stats` and
// `gateway_bandwidth_stats` keep raw rows for 24 hours so a graph can be drawn.
// This package answers a different question - what did this platform handle
// over months - and it answers it from buckets rather than rows, because a year
// of raw samples is a query nobody waits for.
//
// Nothing here reaches outside the installation. Telemetry that phoned home was
// removed in full on 2026-07-30 and the README states the platform sends
// nothing anywhere; that is a product property, and a local record does not
// change it.
package metrics

import (
	"math"
	"time"
)

// Key identifies one series: what is measured, about whom, and where.
//
// Subject is empty for a platform-wide number and otherwise names the thing -
// an edge id, a node token, a server uuid, a user id. Region is empty for
// anything not measured per region. Both are part of the identity, so a series
// that gains a region later is a NEW series rather than a silent redefinition
// of the old one.
type Key struct {
	Metric  string
	Subject string
	Region  string
}

// Bucket is one series' accumulated observations for one time bucket.
//
// Four numbers, and every one of them merges exactly across flushes: min takes
// the smaller, max the larger, sum and count add. Average is derived, never
// stored - storing it would be the one field that cannot be merged.
//
// A percentile is missing for the same reason and its absence is deliberate:
// p95 cannot be combined from two partial buckets, so a stored one would be
// silently wrong the first time a flush split a bucket in two. If a percentile
// is ever needed it needs a histogram, not a column.
type Bucket struct {
	Min   float64
	Max   float64
	Sum   float64
	Count int64
}

// Add folds one observation in.
//
// Zero is an observation like any other. An empty bucket is Count == 0, which
// is what "nothing was measured" means; a bucket holding a measured zero has
// Count == 1 and says something different.
func (b *Bucket) Add(v float64) {
	if b.Count == 0 {
		b.Min, b.Max = v, v
	} else {
		b.Min = math.Min(b.Min, v)
		b.Max = math.Max(b.Max, v)
	}
	b.Sum += v
	b.Count++
}

// Merge combines two accumulations of the same bucket.
//
// This is the same arithmetic the database does on conflict, kept here so the
// in-process path and the SQL path cannot disagree about what merging means.
func (b Bucket) Merge(o Bucket) Bucket {
	if b.Count == 0 {
		return o
	}
	if o.Count == 0 {
		return b
	}
	return Bucket{
		Min:   math.Min(b.Min, o.Min),
		Max:   math.Max(b.Max, o.Max),
		Sum:   b.Sum + o.Sum,
		Count: b.Count + o.Count,
	}
}

// Avg is the mean, or 0 for an empty bucket. Derived rather than stored.
func (b Bucket) Avg() float64 {
	if b.Count == 0 {
		return 0
	}
	return b.Sum / float64(b.Count)
}

// BucketStart is the bucket an observation at t belongs to.
//
// Always UTC. Truncate on a local time would put the boundary wherever the
// host's offset happens to be, so two Cores in different zones would write two
// different rows for the same minute - and the UPSERT that is supposed to merge
// them would insert instead.
func BucketStart(t time.Time, res time.Duration) time.Time {
	return t.UTC().Truncate(res)
}

// Row is one bucket ready to be written.
type Row struct {
	Time   time.Time
	Key    Key
	Bucket Bucket
}
