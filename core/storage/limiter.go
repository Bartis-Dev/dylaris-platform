package storage

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// This file bounds how many goroutines can be inside a host-path filesystem
// syscall at the same time.
//
// The watchdog in health.go closes the door on new work reaching a mount that
// is already KNOWN to be dead. Two holes are left over: requests that were
// already inside a syscall when the mount died, and requests arriving in the
// window before the probe notices. Each of those pins an OS thread for as long
// as the kernel keeps the syscall outstanding, and nothing bounded how many
// there could be. Crossing Go's 10,000-thread ceiling is a fatal,
// unrecoverable crash.
//
// What this achieves, stated exactly: the NUMBER of blocked syscalls becomes a
// constant instead of a function of traffic, so the process degrades instead of
// dying. It unsticks nothing. A call abandoned at its deadline leaves a
// goroutine and a thread parked until the kernel returns, which on NFS mounted
// "hard" means never. That is a real improvement over a crash and it is not
// availability of any kind.

const (
	// maxConcurrentFSOps is how many goroutines may be inside a host-path
	// filesystem syscall at one time.
	//
	// The load-bearing distinction is that WAITING for a slot parks a goroutine
	// and costs a few KB of heap, while ENTERING the syscall pins an OS thread.
	// Queueing is therefore cheap exactly where blocking is expensive.
	//
	// This is not a performance tuning knob. It is a bound on thread
	// consumption: far above realistic healthy concurrency for this product,
	// and two orders of magnitude below the ceiling it protects.
	maxConcurrentFSOps = 128

	// fsOpDeadline is how long a DETACHED filesystem call may run before the
	// caller stops waiting for it. Well above any plausible healthy metadata
	// call, well below a user's patience. Giving up does not stop the call:
	// nothing can, no context can interrupt a blocking file syscall.
	fsOpDeadline = 15 * time.Second
)

// errFSOpDeadlineExceeded is the cause recorded when a detached call is
// abandoned. Like the probe's own causes it carries no errno, because no
// syscall returned to produce one. IsBackendUnreachable therefore cannot
// recognise it, which is why the deadline path trips the gate directly through
// setHealth instead of going through ReportFailure.
var errFSOpDeadlineExceeded = errors.New("filesystem call did not return within the deadline")

// acquireFS takes a slot, waiting for one if none is free.
//
// Waiters deliberately do NOT fail fast. A healthy burst above the cap is a
// capacity condition, and answering it with "storage unreachable" would be a
// lie. During a real outage the gate check runs first and fails fast, so nobody
// reaches this queue anyway.
func (g *Gate) acquireFS(ctx context.Context) error {
	select {
	case g.fsSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *Gate) releaseFS() { <-g.fsSem }

// Run acquires a slot and runs fn INLINE, on the caller's goroutine, with no
// deadline. It is for WriteFile and CopyToLocal only.
//
// Those two must not be detached: they consume a caller-owned io.Reader, in
// practice the HTTP request body, which net/http invalidates once the handler
// returns. Reading it from a goroutine that outlived the handler is a
// use-after-free, so detaching them would trade a bounded hang for memory
// corruption.
//
// They get no deadline either, because a deadline cannot tell a legitimately
// slow multi-GB upload from a wedged mount, and killing the former is a worse
// bug than tolerating the latter. They still hold a slot, so they are still
// bounded, and the ctxReader in provider.go still aborts them between chunks.
func (g *Gate) Run(ctx context.Context, fn func() error) error {
	if g == nil {
		return fn()
	}
	if err := g.acquireFS(ctx); err != nil {
		return err
	}
	defer g.releaseFS()
	return fn()
}

// Do acquires a slot and runs fn detached, with the deadline. For calls that
// pass only a path and return something the caller can discard.
func (g *Gate) Do(ctx context.Context, fn func() error) error {
	_, err := doValue(g, ctx, func() (struct{}, error) { return struct{}{}, fn() }, nil)
	return err
}

// fsOutcome carries a detached call's result over the one channel the caller is
// allowed to read it from.
type fsOutcome[T any] struct {
	val T
	err error

	// panicValue is what fn panicked with, handed back so the caller can
	// re-panic on its own goroutine. See callGuarded.
	panicValue any
	panicked   bool
}

// callGuarded runs fn and turns a panic into part of the outcome instead of
// letting it escape.
//
// This exists because detaching the call moved it off the request goroutine.
// There, net/http recovers a panic into one failed request; on a goroutine of
// this package's making the same panic kills the whole process. Bounding the
// pile-up must not quietly widen the blast radius of an unrelated bug from one
// request to the entire Core.
//
// Recovering here also keeps the deadline honest: a panicking fn never reaches
// the send below, so without this the caller would wait out the full deadline
// and trip the gate over a bug that has nothing to do with the mount.
//
// The re-panic carries the original value but not its stack, which is the
// price of the handover.
func callGuarded[T any](fn func() (T, error)) (out fsOutcome[T]) {
	defer func() {
		if p := recover(); p != nil {
			out.panicValue, out.panicked = p, true
		}
	}()
	out.val, out.err = fn()
	return
}

// doValue is the detached path for calls that return a value alongside the
// error. It releases the slot before returning, so its result owns nothing.
//
// abandon, when non-nil, receives a value that arrived after the caller gave up
// on it, so an fd that lands late is closed rather than leaked.
func doValue[T any](g *Gate, ctx context.Context, fn func() (T, error), abandon func(T)) (T, error) {
	val, release, err := detach(g, ctx, fn, abandon)
	if release != nil {
		release()
	}
	return val, err
}

// detach acquires a slot, runs fn on its own goroutine and waits for it with
// the deadline. release is non-nil exactly when the result arrived in time, and
// the caller then owns the slot until it calls release.
//
// Ownership of the slot is handed to whoever ends up responsible for it, and
// that handover is the whole of the concurrency design here:
//
//   - The result arrived in time. The caller owns the slot and releases it,
//     either immediately (doValue) or when the returned ReadCloser is closed
//     (openDetached).
//   - The deadline expired, or ctx was cancelled. The call is abandoned but not
//     stopped. The worker goroutine keeps the slot until fn actually returns,
//     then hands the late value to abandon and releases. That is the bound: a
//     slot stays taken for exactly as long as the kernel keeps the syscall
//     outstanding.
//
// Both channels are buffered and each is written exactly once, so neither side
// can ever block on the other. The res buffering is load-bearing for the same
// reason it is in runProbe: on an unbuffered channel an abandoned worker would
// block on its send forever even after the syscall finally returned, turning a
// temporary outage into a permanent goroutine leak. Passing the result over
// that channel rather than through a captured variable is what keeps the
// abandoned worker and the caller free of a data race.
func detach[T any](g *Gate, ctx context.Context, fn func() (T, error), abandon func(T)) (T, func(), error) {
	if g == nil {
		val, err := fn()
		return val, nil, err
	}
	if err := g.acquireFS(ctx); err != nil {
		var zero T
		return zero, nil, err
	}

	res := make(chan fsOutcome[T], 1)
	taken := make(chan bool, 1)
	go func() {
		out := callGuarded(fn)
		res <- out
		if <-taken {
			return
		}
		if abandon != nil {
			abandon(out.val)
		}
		// A panic in an ABANDONED call loses its value here, with nothing left
		// to hand it back to. Releasing the slot and staying alive beats taking
		// the process down for a caller that already gave up.
		g.releaseFS()
	}()

	timer := time.NewTimer(g.fsDeadline)
	defer timer.Stop()
	select {
	case out := <-res:
		taken <- true
		if out.panicked {
			// Release before unwinding: the deferred release belongs to a
			// caller that is about to stop existing.
			g.releaseFS()
			panic(out.panicValue)
		}
		return out.val, g.releaseFS, out.err
	case <-timer.C:
		taken <- false
		// The mount not answering within the deadline is the evidence, but it
		// carries no errno, so ReportFailure would discard it. Trip directly.
		g.setHealth(false, errFSOpDeadlineExceeded)
		var zero T
		return zero, nil, gateBlockedError(errFSOpDeadlineExceeded)
	case <-ctx.Done():
		// A caller that went away (a cancelled request) is not evidence about
		// the mount, so this must not trip the gate.
		taken <- false
		var zero T
		return zero, nil, ctx.Err()
	}
}

// openDetached is the detached path for GetFile. On success the slot is NOT
// released at return: the streaming reads happen in the handler, outside this
// package, so a mount that wedges mid-download would otherwise block a
// goroutine nothing here ever sees. The slot is released on Close.
//
// That makes closing a correctness requirement rather than hygiene. A caller
// that forgets burns a slot permanently, and enough of them exhaust the pool.
// All call sites were audited and every one closes on every path.
func openDetached(g *Gate, ctx context.Context, fn func() (io.ReadCloser, error)) (io.ReadCloser, error) {
	rc, release, err := detach(g, ctx, fn, closeLate)
	if release == nil {
		// Abandoned, or no gate at all. Either way there is no slot to hand on.
		return rc, err
	}
	if err != nil || rc == nil {
		release()
		return rc, err
	}
	return &slotReadCloser{ReadCloser: rc, release: release}, nil
}

// closeLate closes a ReadCloser that arrived after its caller gave up on it.
// Without this the abandoned open leaks the fd it opened.
func closeLate(rc io.ReadCloser) {
	if rc != nil {
		_ = rc.Close()
	}
}

// slotReadCloser holds a slot until it is closed.
type slotReadCloser struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

// Close releases the slot at most once. A double Close is a real pattern in
// this codebase (a defer plus an explicit close on the happy path), and a
// second release would be giving back a slot this reader never held: with a
// channel semaphore that blocks the closing goroutine forever.
func (c *slotReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.once.Do(c.release)
	return err
}
