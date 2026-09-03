package services

import (
	"strings"
	"testing"

	"dylaris-pkg/errlog"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// The whole chain, end to end: a background service fails, and the reader the
// panel's Errors screen uses finds it.
//
// Both halves have to hold at once, and testing only the write half is what
// made this worth a test. The producer names the service, the reader scans for
// that name, and the two are separate strings in separate places - that is
// exactly how the gate -> edge rename left the busiest component with no error
// reporting for months, writing to dylaris:errors:edge:* while Core scanned
// dylaris:errors:gate:*. Nothing failed; the section just looked healthy.
//
// So this reads through GetAllServiceErrorsFromRedis, the call the panel's
// infrastructure overview makes. That one walks ErrorStreamServices, so it is
// the reader a missing name actually breaks - GetServiceErrorsFromRedis takes
// the name as a parameter and would find the entry either way.
func TestABackgroundFailureReachesTheErrorsScreen(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	SetErrorSink(errlog.New(rdb, "core", "core-test"))
	defer SetErrorSink(nil)

	logErrf("backup-scheduler", "job %d dispatch failed: %v", 7, errTest)

	byService := GetAllServiceErrorsFromRedis(rdb, 10)
	entries := byService["core"]
	if len(entries) != 1 {
		t.Fatalf("want 1 core entry from the panel's reader, got %d (services seen: %v)",
			len(entries), keysOf(byService))
	}
	got := entries[0]
	if got.Level != "ERROR" {
		t.Errorf("level = %q, want ERROR", got.Level)
	}
	if got.Source != "backup-scheduler" {
		t.Errorf("source = %q, want backup-scheduler - the screen filters by it", got.Source)
	}
	if !strings.Contains(got.Message, "job 7 dispatch failed") {
		t.Errorf("message = %q, want the formatted failure", got.Message)
	}
}

// Core must be a name the reader scans for. Kept separate from the test above
// so a removal names itself instead of surfacing as a mysteriously empty read.
func TestCoreIsAServiceTheReaderScansFor(t *testing.T) {
	for _, s := range ErrorStreamServices {
		if s == "core" {
			return
		}
	}
	t.Fatalf("core missing from ErrorStreamServices %v - the background services write "+
		"dylaris:errors:core:* and nothing would read it", ErrorStreamServices)
}

// No sink wired is the normal state in tests and during early boot, before
// Redis exists. The failure still has to reach the log rather than take the
// service down with it.
func TestWithoutASinkTheFailureStillLogs(t *testing.T) {
	SetErrorSink(nil)
	logErrf("scheduled-tasks", "list due failed: %v", errTest)
}

func keysOf(m map[string][]errlog.Entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

type testErr struct{}

func (testErr) Error() string { return "boom" }

var errTest = testErr{}
