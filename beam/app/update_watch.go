package main

import (
	"context"
	"sync"
	"time"
)

// Noticing an update while the window is showing the panel.
//
// GetUpdateInfo was called once, from the settings screen's mount effect. That
// is the only screen the app shows before it hands the window to the panel, so
// an app left running for a week never looked again - and even when it did, the
// notice lived on a screen the user was no longer on. The self-updater worked
// perfectly and nobody was told to use it.
//
// Two halves fix that, and both are deliberately small:
//
//   - A ticker re-checks in the background and caches the answer.
//   - The launcher button injected into every proxied page renders a dot when
//     that cached answer says an update is waiting.
//
// The dot is the whole notification. No modal, no toast over someone else's
// application: an update that matters enough to interrupt is a MANDATORY one,
// and that already has its own blocking screen.

// updateCheckInterval is how often the background check runs.
//
// Six hours, not minutes. The manifest is a static file on a release host and
// nothing here is time-critical - a mandatory update is enforced by Core at
// connect time, not by this poll. The first check runs at startup, so a user who
// opens the app the day after a release sees the dot immediately.
const updateCheckInterval = 6 * time.Hour

// updateWatcher caches "is there an update" for readers that must not block.
//
// The launcher tag is built while a proxied response is being rewritten, which
// is on the request path: asking the network there would stall every page load
// behind a release-host round trip, and fail the page when the host is down.
type updateWatcher struct {
	mu        sync.RWMutex
	available bool
	checked   bool
}

// available reports the cached answer. False until the first check completes,
// which is the right default: a dot that appears a moment late is invisible,
// while one shown before anything is known would be a lie.
func (w *updateWatcher) get() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.available
}

func (w *updateWatcher) set(v bool) {
	w.mu.Lock()
	w.available = v
	w.checked = true
	w.mu.Unlock()
}

// startUpdateWatch runs the check now and then on a ticker until ctx ends.
//
// Errors are swallowed on purpose. A release host that is unreachable is not
// something to report on somebody's server console - the app keeps working, and
// the next tick tries again. The one thing it must not do is report an update
// that is not there, and a failed fetch leaves the cached answer alone rather
// than clearing it: a network blip should not make a waiting update vanish.
func (a *App) startUpdateWatch(ctx context.Context) {
	check := func() {
		if info := a.GetUpdateInfo(); info != nil && info.Latest != "" {
			a.updates.set(info.UpdateAvailable)
		}
	}
	go func() {
		check()
		t := time.NewTicker(updateCheckInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				check()
			}
		}
	}()
}
