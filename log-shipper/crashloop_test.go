package main

import (
	"testing"
	"time"
)

// TestCrashStreak_GivesUpOnAServerThatCannotStart pins the policy that makes a
// running container mean a running server.
//
// This supervisor is PID 1 of the server container. While it keeps restarting a
// process that dies instantly, the container stays Up, and every layer above
// reads that as healthy: the node reconciler only inspects containers that are
// NOT running, so does the stats collector, so the panel says "online". Measured
// on a server whose jar was missing - eight minutes of a JVM exiting every five
// seconds, reported as online the whole time, and it would not have stopped.
func TestCrashStreak_GivesUpOnAServerThatCannotStart(t *testing.T) {
	var s crashStreak
	now := time.Now()

	for i := 1; i < maxCrashesInWindow; i++ {
		if _, giveUp := s.record(now.Add(time.Duration(i) * 5 * time.Second)); giveUp {
			t.Fatalf("gave up after %d crashes, want %d", i, maxCrashesInWindow)
		}
	}
	count, giveUp := s.record(now.Add(maxCrashesInWindow * 5 * time.Second))
	if !giveUp {
		t.Fatalf("count %d did not trigger give-up at the %d limit", count, maxCrashesInWindow)
	}
}

// TestCrashStreak_SpreadOutCrashesNeverGiveUp is the case the window exists for:
// a server that crashes now and then over a day, running normally in between,
// must keep being restarted. Each crash lands outside the previous streak's
// window, so each is its own incident.
func TestCrashStreak_SpreadOutCrashesNeverGiveUp(t *testing.T) {
	var s crashStreak
	now := time.Now()

	for i := range 10 {
		at := now.Add(time.Duration(i) * time.Hour)
		count, giveUp := s.record(at)
		if giveUp {
			t.Fatalf("crash %d (an hour after the last) triggered give-up", i+1)
		}
		if count != 1 {
			t.Errorf("crash %d has streak count %d, want 1 - each is a separate incident", i+1, count)
		}
	}
}

// TestCrashStreak_ASurvivorInsideTheWindowStillCounts is the trap the previous
// version fell into. It counted CONSECUTIVE crashes and reset the streak after
// any run longer than 60s, which meant a server surviving 61 seconds each time
// reset forever and was restarted forever. Anchoring to wall clock instead: runs
// of a few minutes are still three crashes in a quarter of an hour, and that is
// a loop.
func TestCrashStreak_ASurvivorInsideTheWindowStillCounts(t *testing.T) {
	var s crashStreak
	now := time.Now()

	s.record(now)
	s.record(now.Add(4 * time.Minute))
	count, giveUp := s.record(now.Add(8 * time.Minute))
	if !giveUp {
		t.Fatalf("three crashes in 8 minutes did not give up (count %d)", count)
	}
}

// TestCrashStreak_TheWindowIsMeasuredFromTheStreakStart: the window bounds the
// whole streak, not the gap between neighbours, so a slow drift cannot walk past
// the limit one small step at a time.
func TestCrashStreak_TheWindowIsMeasuredFromTheStreakStart(t *testing.T) {
	var s crashStreak
	now := time.Now()

	s.record(now)
	// Just inside the window: still the same streak.
	if count, _ := s.record(now.Add(crashWindow - time.Second)); count != 2 {
		t.Errorf("count = %d for a crash inside the window, want 2", count)
	}
	// Just outside it, measured from the streak START: a new incident.
	if count, giveUp := s.record(now.Add(crashWindow + time.Second)); count != 1 || giveUp {
		t.Errorf("count = %d giveUp = %v past the window, want a fresh streak", count, giveUp)
	}
}
