package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"dylaris-core/models"
)

// TestReapAbandonedRuns_KeepsBackendDetailOutOfTheOperatorMessage.
//
// The message this writes lands in backup_runs.error_message, which the panel
// renders to anyone holding backups.read on the server - a tenant, not an
// operator. The backend's endpoint and bucket live behind settings.read. A
// transport failure from the S3 SDK carries the full request URL, so putting
// the raw error in the message would route an internal hostname, a bucket name
// and often an internal IP straight around that boundary.
//
// The failing storage here is a job whose storage row is missing, which is the
// cheapest way to reach the "could not be determined" branch; what is asserted
// is that the branch says nothing about the backend beyond the key, which is
// already in the API response.
func TestReapAbandonedRuns_KeepsBackendDetailOutOfTheOperatorMessage(t *testing.T) {
	now := time.Now()
	fs := &reaperFakeStore{
		abandoned: []models.BackupRun{{
			ID: 9, JobID: 7, Status: "running",
			StartedAt: now.Add(-8 * time.Hour), StorageKey: "backups/uuid/run.tar.gz",
		}},
		// A job pointing at a storage row that is gone: Open is never reached,
		// and the error names the condition rather than a backend. It has to be
		// a DANGLING id rather than no storage at all - a job with no storage of
		// its own now legitimately falls back to the platform default, which is
		// the bug ResolveJobStorage exists to fix.
		job: &models.BackupJob{ID: 7, StorageID: func() *int { i := 42; return &i }()},
	}

	newReaper(fs).reapAbandonedRuns(context.Background(), now)

	if len(fs.updates) != 1 {
		t.Fatalf("updates = %d, want 1", len(fs.updates))
	}
	msg := fs.updates[0].message

	if !strings.Contains(msg, "could not be determined") {
		t.Fatalf("message = %q, want the undetermined branch", msg)
	}
	if !strings.Contains(msg, "backups/uuid/run.tar.gz") {
		t.Errorf("message = %q, want the storage key named (it is already in the API response)", msg)
	}
	// The raw error is logged instead. Nothing that identifies the backend may
	// survive into the stored message.
	for _, leak := range []string{
		"storage:", "provider", "endpoint", "bucket", "dial", "tcp",
		"http://", "https://", "connection refused", "no storage configured",
	} {
		if strings.Contains(strings.ToLower(msg), strings.ToLower(leak)) {
			t.Errorf("message contains %q, want no backend detail: %s", leak, msg)
		}
	}
}
