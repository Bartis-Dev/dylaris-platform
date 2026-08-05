package main

import (
	"testing"
	"time"
)

// TestRecordCrash_GivesUpOnAServerThatCannotStart pins the policy that makes a
// running container mean a running server.
//
// This supervisor is PID 1 of the server container. While it keeps restarting a
// process that dies instantly, the container stays Up, and every layer above
// reads that as healthy: the node reconciler only inspects containers that are
// NOT running, so does the stats collector, so the panel says "online". Measured
// on a server whose jar was missing - eight minutes of a JVM exiting every five
// seconds, reported as online the whole time, and it would not have stopped.
func TestRecordCrash_GivesUpOnAServerThatCannotStart(t *testing.T) {
	count := 0
	for i := 1; i < maxConsecutiveCrashes; i++ {
		var giveUp bool
		count, giveUp = recordCrash(count, 200*time.Millisecond)
		if giveUp {
			t.Fatalf("gave up after %d fast crashes, want %d", i, maxConsecutiveCrashes)
		}
	}
	count, giveUp := recordCrash(count, 200*time.Millisecond)
	if !giveUp {
		t.Fatalf("count %d did not trigger give-up at the %d limit", count, maxConsecutiveCrashes)
	}
}

// TestRecordCrash_AServerThatRanIsAlwaysRestarted: the limit is for a server
// that cannot start, not for one that crashes. A process that stayed up past the
// threshold resets the count, so a server running for hours and then dying gets
// its restart no matter how many times it has crashed in its life.
func TestRecordCrash_AServerThatRanIsAlwaysRestarted(t *testing.T) {
	count := maxConsecutiveCrashes - 1 // one away from giving up
	count, giveUp := recordCrash(count, crashAliveThreshold+time.Second)
	if giveUp {
		t.Fatal("gave up on a process that had been alive past the threshold")
	}
	if count != 1 {
		t.Errorf("count = %d after a long-lived run, want 1 (the streak resets)", count)
	}
}

// TestRecordCrash_ThresholdIsInclusive: a run of exactly the threshold counts as
// alive. Nothing turns on the boundary, but leaving it unstated invites the next
// reader to change it by accident.
func TestRecordCrash_ThresholdIsInclusive(t *testing.T) {
	if count, _ := recordCrash(3, crashAliveThreshold); count != 1 {
		t.Errorf("a run of exactly crashAliveThreshold gave count %d, want 1", count)
	}
	if count, _ := recordCrash(3, crashAliveThreshold-time.Nanosecond); count != 4 {
		t.Errorf("a run just under the threshold gave count %d, want 4 (streak continues)", count)
	}
}
