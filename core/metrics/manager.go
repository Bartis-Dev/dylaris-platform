package metrics

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"
)

// Manager owns the recorder that is currently in use and can replace it while
// Core runs.
//
// Without it the metrics target is whatever boot found, and an admin who points
// the panel at a different database is told to restart the platform to save a
// statistics setting. The swap is small because Recorder.Run already flushes
// once when its context ends: retiring one is cancel, wait, close.
//
// Everything here tolerates being empty. A Manager with no handle is the normal
// state on a fresh install (the feature is off) and after an unreachable
// database (Recorder.Observe ignores a nil receiver), so no caller has to check
// before recording.
type Manager struct {
	// base outlives every handle; each one gets a child context that is
	// cancelled when that handle is retired.
	base   context.Context
	coreDB *sql.DB
	coreTS bool

	mu   sync.RWMutex
	cur  *Handle
	dsn  string
	stop context.CancelFunc
	// done closes when the retired recorder's Run has returned, which is also
	// when its final flush has finished. Waiting on it is what stops a swap from
	// closing the database out from under a flush in progress.
	done chan struct{}
}

// NewManager returns a Manager with nothing open yet. Call Apply to open one.
func NewManager(base context.Context, coreDB *sql.DB, coreUsesTimescale bool) *Manager {
	return &Manager{base: base, coreDB: coreDB, coreTS: coreUsesTimescale}
}

// Handle is the current handle, or nil. Readers must cope with nil - see the
// note on AppState.Metrics.
func (m *Manager) Handle() *Handle {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cur
}

// Recorder is the current recorder, or nil. The collector reads through this on
// every sample rather than holding one, so a swap takes effect immediately
// instead of at the next restart.
func (m *Manager) Recorder() *Recorder {
	h := m.Handle()
	if h == nil {
		return nil
	}
	return h.Recorder
}

// DSN reports the target currently in use: empty means the Core database.
// Only for reporting - the settings layer owns what it SHOULD be.
func (m *Manager) DSN() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dsn
}

// Apply switches to dsn, opening it first and keeping the old one on failure.
//
// The order is the whole point: a bad target reported by the panel must not also
// have stopped the recording that was working. Nothing is retired until the
// replacement is open and its schema exists.
//
// An unchanged dsn is a no-op, so a settings save that touched something else
// does not interrupt recording for a few seconds and lose the buckets in flight.
func (m *Manager) Apply(dsn string) error {
	m.mu.RLock()
	same := m.cur != nil && m.dsn == dsn
	m.mu.RUnlock()
	if same {
		return nil
	}

	ctx, cancel := context.WithCancel(m.base)
	h, err := Open(ctx, m.coreDB, dsn, m.coreTS)
	if err != nil {
		cancel()
		return err
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Recorder.Run(ctx, FlushInterval)
	}()

	m.mu.Lock()
	old, oldStop, oldDone := m.cur, m.stop, m.done
	m.cur, m.dsn, m.stop, m.done = h, dsn, cancel, done
	m.mu.Unlock()

	retire(old, oldStop, oldDone)
	return nil
}

// Close retires whatever is open. Safe on a Manager that never opened one.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	old, oldStop, oldDone := m.cur, m.stop, m.done
	m.cur, m.dsn, m.stop, m.done = nil, "", nil, nil
	m.mu.Unlock()
	retire(old, oldStop, oldDone)
}

// retireTimeout bounds the wait for a retired recorder's final flush. Longer
// than the flush's own 5s budget so the normal case completes, short enough that
// a wedged database cannot hold a settings save open.
const retireTimeout = 10 * time.Second

// retire stops a recorder, waits for its last flush, then closes its database.
//
// Closing before that wait is the bug this exists to avoid: Run flushes once on
// cancellation, and a database closed underneath it turns the final minutes into
// a "store write failed" line instead of rows.
func retire(h *Handle, stop context.CancelFunc, done chan struct{}) {
	if h == nil {
		return
	}
	if stop != nil {
		stop()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(retireTimeout):
			log.Printf("metrics: the previous recorder did not finish its final flush within %s; closing anyway", retireTimeout)
		}
	}
	if err := h.Close(); err != nil {
		log.Printf("metrics: closing the previous metrics database: %v", err)
	}
}
