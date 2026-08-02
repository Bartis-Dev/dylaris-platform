// Package queue provides at-least-once work queues on top of Redis Streams +
// consumer groups. It replaces the previous RPUSH/BLPOP lists, which silently
// LOST a message if a worker died between BLPOP and finishing the work (the
// item was already popped, with no redelivery).
//
// Delivery model: a producer XADDs a message; a Consumer reads it through a
// consumer group (XREADGROUP), runs the handler, and only then XACKs. A message
// that is delivered but never ACKed (worker crash / SIGTERM mid-work) stays in
// the group's Pending Entries List and is re-delivered on the next run: the
// consumer reprocesses its own pending entries, and XAUTOCLAIM grabs entries
// orphaned by a dead/renamed consumer.
//
// At-least-once plus a per-message dedup marker gives effective once-processing
// for the common lost-ACK case. Handlers must still tolerate a rare double
// delivery (most node command handlers already do: create force-removes first,
// delete ignores not-found, start/stop/restart are idempotent). A message that
// keeps failing is dead-lettered after MaxDeliveries so it can't block the queue.
package queue

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultMaxLen is the approximate cap on stream length (XADD MAXLEN ~). Streams
// are work queues, not history; processed+acked entries are trimmed off the tail.
const DefaultMaxLen = 10000

// Publish appends payload to the stream as a single "data" field and approx-caps
// the stream length. Returns the new entry ID. The payload is whatever the
// previous RPUSH pushed (e.g. a JSON-encoded command), so the wire format is
// unchanged from the consumer's point of view.
func Publish(ctx context.Context, rdb *redis.Client, stream string, payload []byte) (string, error) {
	return rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		MaxLen: DefaultMaxLen,
		Approx: true,
		Values: map[string]interface{}{"data": payload},
	}).Result()
}

// Handler processes one message payload. Returning nil ACKs the message;
// returning an error leaves it pending for redelivery (until MaxDeliveries).
type Handler func(ctx context.Context, data []byte) error

// Consumer reads a single stream as one named member of a consumer group.
type Consumer struct {
	rdb    *redis.Client
	stream string
	group  string
	name   string

	// Block is how long XREADGROUP blocks waiting for new entries.
	Block time.Duration
	// Count is the max batch size per read.
	Count int64
	// ClaimMinIdle: pending entries idle longer than this are claimed from a
	// dead/other consumer via XAUTOCLAIM.
	ClaimMinIdle time.Duration
	// DedupTTL is how long a processed-marker lives (dedup window for redelivery).
	DedupTTL time.Duration
	// MaxDeliveries: after this many failed attempts a message is dead-lettered
	// (moved to "<stream>:dead") and ACKed, so a poison message can't wedge the
	// queue.
	MaxDeliveries int64
	// Concurrency is the number of messages processed in parallel. 1 (default)
	// = strictly sequential. Set higher for independent commands (e.g. node
	// server commands); keep 1 where ordering/exclusivity matters (e.g. the
	// migration orchestrator, which serializes via per-server locks).
	Concurrency int
}

// NewConsumer builds a Consumer with sane defaults. stream is the Redis key,
// group is shared by all members, name identifies this member (stable across
// restarts so its pending entries are recovered, e.g. the node id).
func NewConsumer(rdb *redis.Client, stream, group, name string) *Consumer {
	return &Consumer{
		rdb:           rdb,
		stream:        stream,
		group:         group,
		name:          name,
		Block:         5 * time.Second,
		Count:         16,
		ClaimMinIdle:  60 * time.Second,
		DedupTTL:      24 * time.Hour,
		MaxDeliveries: 5,
		Concurrency:   1,
	}
}

func (c *Consumer) doneKey(id string) string     { return c.stream + ":done:" + id }
func (c *Consumer) attemptsKey(id string) string { return c.stream + ":attempts:" + id }
func (c *Consumer) deadKey() string              { return c.stream + ":dead" }

// EnsureGroup creates the consumer group (and the stream) if absent. Idempotent.
func (c *Consumer) EnsureGroup(ctx context.Context) error {
	// "$" = only new messages for a freshly created group; existing entries are
	// not replayed to a brand-new group (matches the old fire-and-forward
	// semantics — only the group's own pending is ever reprocessed).
	err := c.rdb.XGroupCreateMkStream(ctx, c.stream, c.group, "$").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

// Run blocks until ctx is cancelled, processing messages with up to
// Concurrency workers. On start and whenever the read times out it also
// reclaims stale pending (XAUTOCLAIM) and reprocesses its own pending entries,
// so a crash mid-work is recovered. The worker pool gives natural back-pressure:
// when all workers are busy, Run stops reading ">" so undelivered entries simply
// wait durably in the stream.
func (c *Consumer) Run(ctx context.Context, handler Handler) error {
	if err := c.EnsureGroup(ctx); err != nil {
		return err
	}

	workers := c.Concurrency
	if workers < 1 {
		workers = 1
	}
	work := make(chan redis.XMessage)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range work {
				c.handleOne(ctx, m, handler)
			}
		}()
	}
	defer func() { close(work); wg.Wait() }()

	dispatch := func(msgs []redis.XMessage) bool {
		for _, m := range msgs {
			select {
			case work <- m:
			case <-ctx.Done():
				return false
			}
		}
		return true
	}

	// Recover anything left from a previous life before taking new work.
	// Recovery is infrequent and processed synchronously (correctness over
	// parallelism); only live ">" messages flow through the worker pool.
	c.claimStale(ctx, handler)
	c.recoverPending(ctx, handler)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    c.group,
			Consumer: c.name,
			Streams:  []string{c.stream, ">"},
			Count:    c.Count,
			Block:    c.Block,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) || ctx.Err() != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				// Idle tick: sweep for stragglers (orphaned / retryable), then wait.
				c.claimStale(ctx, handler)
				c.recoverPending(ctx, handler)
				continue
			}
			// Transient Redis error — back off briefly so we don't hot-loop.
			log.Printf("queue %s: read error: %v", c.stream, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
			continue
		}
		for _, st := range res {
			if !dispatch(st.Messages) {
				return ctx.Err()
			}
		}
	}
}

// recoverPending reprocesses this consumer's already-delivered-but-unacked
// entries (XREADGROUP id "0"). Dedup skips ones that actually completed.
func (c *Consumer) recoverPending(ctx context.Context, handler Handler) {
	res, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    c.group,
		Consumer: c.name,
		Streams:  []string{c.stream, "0"},
		Count:    c.Count,
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return
	}
	for _, st := range res {
		for _, m := range st.Messages {
			c.handleOne(ctx, m, handler)
		}
	}
}

// claimStale takes over pending entries idle longer than ClaimMinIdle that are
// owned by a dead or renamed consumer. Best-effort: a backend without
// XAUTOCLAIM (or a transient error) is logged and skipped.
func (c *Consumer) claimStale(ctx context.Context, handler Handler) {
	msgs, _, err := c.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   c.stream,
		Group:    c.group,
		Consumer: c.name,
		MinIdle:  c.ClaimMinIdle,
		Start:    "0",
		Count:    c.Count,
	}).Result()
	if err != nil {
		return
	}
	for _, m := range msgs {
		c.handleOne(ctx, m, handler)
	}
}

// handleOne runs the dedup → handler → mark-done → ACK sequence for one message.
func (c *Consumer) handleOne(ctx context.Context, m redis.XMessage, handler Handler) {
	// Already processed (ACK lost / redelivered): just ACK and move on.
	if n, _ := c.rdb.Exists(ctx, c.doneKey(m.ID)).Result(); n == 1 {
		c.ack(ctx, m.ID)
		return
	}

	data := msgData(m)
	if data == nil {
		// Unparseable entry — dead-letter it so it can't loop forever.
		log.Printf("queue %s: message %s has no data field, dead-lettering", c.stream, m.ID)
		c.deadLetter(ctx, m.ID, []byte("<missing data field>"))
		return
	}

	if err := handler(ctx, data); err != nil {
		attempts, _ := c.rdb.Incr(ctx, c.attemptsKey(m.ID)).Result()
		c.rdb.Expire(ctx, c.attemptsKey(m.ID), c.DedupTTL)
		if attempts >= c.MaxDeliveries {
			log.Printf("queue %s: message %s failed %d times, dead-lettering: %v", c.stream, m.ID, attempts, err)
			c.deadLetter(ctx, m.ID, data)
			return
		}
		log.Printf("queue %s: message %s handler error (attempt %d/%d), will retry: %v", c.stream, m.ID, attempts, c.MaxDeliveries, err)
		// Leave it pending (no ACK) for redelivery.
		return
	}

	// Success: mark processed (dedup window) and ACK.
	c.rdb.Set(ctx, c.doneKey(m.ID), "1", c.DedupTTL)
	c.ack(ctx, m.ID)
	c.rdb.Del(ctx, c.attemptsKey(m.ID))
}

func (c *Consumer) ack(ctx context.Context, id string) {
	if err := c.rdb.XAck(ctx, c.stream, c.group, id).Err(); err != nil {
		log.Printf("queue %s: XACK %s failed: %v", c.stream, id, err)
	}
}

// deadLetter parks a poison/unparseable message on a side stream and ACKs the
// original so the main queue keeps flowing. Operators can inspect "<stream>:dead".
//
// The ACK is CONDITIONAL on the park succeeding. Acking regardless would mean a
// failed XAdd silently destroys the payload - the one message an operator most
// needs to look at, discarded precisely when the system is already unhealthy.
// Leaving it pending instead costs a retry on the next recovery pass and parks
// it as soon as Redis accepts the write again.
func (c *Consumer) deadLetter(ctx context.Context, id string, data []byte) {
	if err := c.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: c.deadKey(),
		MaxLen: DefaultMaxLen,
		Approx: true,
		Values: map[string]interface{}{"data": data, "orig_id": id},
	}).Err(); err != nil {
		log.Printf("queue %s: could NOT park message %s on %s (%v) - leaving it pending rather than dropping it",
			c.stream, id, c.deadKey(), err)
		return
	}
	c.ack(ctx, id)
	c.rdb.Del(ctx, c.attemptsKey(id))
}

func msgData(m redis.XMessage) []byte {
	if v, ok := m.Values["data"].(string); ok {
		return []byte(v)
	}
	return nil
}
