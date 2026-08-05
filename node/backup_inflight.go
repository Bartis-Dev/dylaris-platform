package main

import "sync"

// The command queue is Redis Streams consumer groups, which is at-least-once by
// construction: pkg/queue reclaims stale pending entries with XAUTOCLAIM and
// reprocesses a consumer's own pending list on reconnect. A Core restart while a
// long job is running is enough to have the same command handed to this node a
// second time.
//
// Observed: a Core restart mid-restore produced
//
//	09:02:37 backup_restore: starting run=1 ... stopping container
//	09:02:42 backup_restore: starting run=1 ... stopping container
//
// - two extractions into the same directory, each stopping and restarting the
// container under the other. Backups are milder (both write the same archive to
// the same key) but still burn the disk twice and can leave a half-written
// object behind the one that finishes second.
//
// The guard is deliberately IN-MEMORY and per-process. It suppresses exactly the
// hazard that exists - two runs of the same job concurrently inside one node -
// and does not suppress the case redelivery is FOR: if the node process died
// mid-job, the guard died with it and the redelivery must re-run.
type inflightSet struct {
	mu  sync.Mutex
	ids map[string]bool
}

func newInflightSet() *inflightSet { return &inflightSet{ids: map[string]bool{}} }

// enter marks id as running. It returns false when the id is already running,
// in which case the caller must not start, and must NOT call leave.
func (s *inflightSet) enter(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ids[id] {
		return false
	}
	s.ids[id] = true
	return true
}

func (s *inflightSet) leave(id string) {
	s.mu.Lock()
	delete(s.ids, id)
	s.mu.Unlock()
}

// Separate sets: a restore and a backup of the same run id are different work
// and must not block each other.
var (
	restoresInFlight = newInflightSet()
	backupsInFlight  = newInflightSet()
)
