package main

import (
	"log"
	"sync"
	"time"

	pb "dylaris-proto/node"
)

// A mandatory-update warning from Core, remembered so it can be repeated.
//
// Logging it once at connect is not enough: a node that connected during a
// deploy scrolls that line out of the journal within the hour, and the operator
// who eventually looks is looking because something already stopped working.
// The whole point of the warning is to be seen BEFORE the deadline, which means
// it has to keep saying so.
var (
	noticeMu       sync.Mutex
	noticeMessage  string
	noticeDeadline time.Time
	noticeStarted  bool
)

// updateNoticeInterval is how often the pending requirement is repeated. Hourly
// is frequent enough to be found in a day's logs and quiet enough not to be
// filtered out as noise, which a per-minute line would become.
const updateNoticeInterval = time.Hour

// noteUpdateRequirement records what Core said on a successful auth and starts
// repeating it. An auth carrying no requirement CLEARS it: the operator may have
// just updated, and a stale warning about a deadline that no longer applies
// teaches people to ignore the next one.
func noteUpdateRequirement(ar *pb.AuthResult) {
	if ar == nil {
		return
	}
	noticeMu.Lock()
	defer noticeMu.Unlock()

	// The version Core requires is already spelled out inside the message, so
	// it is not stored separately - a second copy would only be a second thing
	// that can disagree.
	noticeMessage = ar.GetUpdateRequired()
	noticeDeadline = time.Time{}
	if d := ar.GetUpdateRequiredDeadline(); d != "" {
		if t, err := time.Parse(time.RFC3339, d); err == nil {
			noticeDeadline = t
		}
	}
	if noticeMessage == "" {
		return
	}

	logUpdateNoticeLocked()
	if !noticeStarted {
		noticeStarted = true
		go repeatUpdateNotice()
	}
}

func repeatUpdateNotice() {
	for range time.Tick(updateNoticeInterval) {
		noticeMu.Lock()
		if noticeMessage != "" {
			logUpdateNoticeLocked()
		}
		noticeMu.Unlock()
	}
}

// logUpdateNoticeLocked writes the warning. Caller holds noticeMu.
//
// The remaining time is spelled out rather than left as a timestamp: an operator
// reading "in 3 days" acts, and one reading a UTC instant does the arithmetic
// wrong or not at all.
func logUpdateNoticeLocked() {
	if noticeDeadline.IsZero() {
		log.Printf("UPDATE REQUIRED: %s", noticeMessage)
		return
	}
	left := time.Until(noticeDeadline)
	switch {
	case left <= 0:
		log.Printf("UPDATE REQUIRED (OVERDUE): %s - this node may stop connecting at any time", noticeMessage)
	case left < 48*time.Hour:
		log.Printf("UPDATE REQUIRED in %d hours: %s", int(left.Hours()), noticeMessage)
	default:
		log.Printf("UPDATE REQUIRED in %d days: %s", int(left.Hours()/24), noticeMessage)
	}
}
