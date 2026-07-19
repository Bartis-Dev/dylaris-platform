package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// connErr is a transport failure the AWS SDK's own taxonomy calls retryable: a
// dial that did not connect. RetryableConnectionError matches it on the
// net.OpError "dial" branch.
func connErr() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
}

// accessDenied is the 403 case: the store ANSWERED, refusing. The connection
// plainly worked, so this must never enter the retry loop.
func accessDenied() error {
	return &smithy.GenericAPIError{Code: "AccessDenied", Message: "403 Forbidden", Fault: smithy.FaultClient}
}

// newTestRes builds a resilience instance with waits short enough that the
// suite never actually pauses, plus a captured log sink.
func newTestRes(interval, budget time.Duration) (*S3Resilience, *logSink) {
	r := newS3Resilience(interval, budget)
	sink := &logSink{}
	r.logf = sink.log
	return r, sink
}

// logSink captures the lines the resilience type writes, so "logged once, not
// per retry" is asserted on the real log calls rather than on a proxy counter.
type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *logSink) log(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, fmt.Sprintf(format, args...))
}

func (s *logSink) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lines...)
}

func (s *logSink) countContaining(sub string) int {
	n := 0
	for _, l := range s.all() {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// TestIsS3ConnectionClass_OnlyTransportFailuresQualify pins the classification
// boundary. Everything on the false side is a case where the store answered, or
// where the CALLER gave up; retrying any of them would be waiting out the
// budget to re-learn something already known.
func TestIsS3ConnectionClass_OnlyTransportFailuresQualify(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil is not a failure at all", nil, false},
		{"dial failure is transport-class", connErr(), true},
		{"connection reset is transport-class", errors.New("read tcp: connection reset by peer"), true},
		// The three the brief requires by name. Each is the store refusing or
		// reporting absence, never the connection failing.
		{"403 AccessDenied is not transport-class", accessDenied(), false},
		{"types.NoSuchKey is not transport-class", &types.NoSuchKey{}, false},
		{"the isS3NotFound NotFound code is not transport-class", &smithy.GenericAPIError{Code: "NotFound"}, false},
		{"NoSuchKey via GenericAPIError is not transport-class", &smithy.GenericAPIError{Code: "NoSuchKey"}, false},
		// context.DeadlineExceeded implements Timeout() and Temporary() both
		// returning true, so the SDK's generic fallback WOULD call it retryable.
		// A caller whose own deadline expired is not evidence about the backend.
		{"caller deadline is not transport-class", context.DeadlineExceeded, false},
		{"caller cancellation is not transport-class", context.Canceled, false},
		{"a wrapped 403 is still not transport-class", fmt.Errorf("get object: %w", accessDenied()), false},
		{"a wrapped dial failure is still transport-class", fmt.Errorf("get object: %w", connErr()), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isS3ConnectionClass(tt.err); got != tt.want {
				t.Errorf("isS3ConnectionClass(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestS3Resilience_RecoversOnSecondAttempt is the core promise: a replayable
// operation that hits a transport blip succeeds anyway, the caller never sees an
// error, and the state moved ok -> reconnecting -> ok exactly once each way.
func TestS3Resilience_RecoversOnSecondAttempt(t *testing.T) {
	fos := newFakeObjectStore()
	fos.failErr, fos.failLeft = connErr(), 1
	res, sink := newTestRes(time.Millisecond, time.Minute)

	var transitions []bool
	res.SetOnChange(func(reconnecting bool, _ time.Time, _ error) {
		transitions = append(transitions, reconnecting)
	})

	p := NewS3ResilientProvider(&S3Provider{os: fos, prefix: "library"}, res)
	files, err := p.ListFiles(context.Background(), "/")
	if err != nil {
		t.Fatalf("ListFiles after a recoverable blip = %v, want nil: the caller must never see it", err)
	}
	if files == nil {
		t.Error("ListFiles returned a nil slice on success")
	}
	if fos.attempts["List"] != 2 {
		t.Errorf("List attempts = %d, want 2 (one failure, one retry)", fos.attempts["List"])
	}
	if reconnecting, _, _ := res.State(); reconnecting {
		t.Error("state = reconnecting after recovery, want ok")
	}
	if len(transitions) != 2 || transitions[0] != true || transitions[1] != false {
		t.Errorf("transitions = %v, want [true false]: one into reconnecting, one back", transitions)
	}
	if n := sink.countContaining("connection lost"); n != 1 {
		t.Errorf("connection-lost log lines = %d, want exactly 1", n)
	}
	if n := sink.countContaining("connection restored"); n != 1 {
		t.Errorf("connection-restored log lines = %d, want exactly 1", n)
	}
}

// TestS3Resilience_LogsAndFiresHookOncePerTransition covers the "not per retry"
// half of the contract with a backend that stays down for many attempts before
// recovering. A per-retry log would drown the log at exactly the moment an
// operator is reading it.
func TestS3Resilience_LogsAndFiresHookOncePerTransition(t *testing.T) {
	const failures = 5
	fos := newFakeObjectStore()
	fos.failErr, fos.failLeft = connErr(), failures
	res, sink := newTestRes(time.Millisecond, time.Minute)

	var hookCalls int
	res.SetOnChange(func(bool, time.Time, error) { hookCalls++ })

	p := NewS3ResilientProvider(&S3Provider{os: fos, prefix: "library"}, res)
	if _, err := p.DownloadURL(context.Background(), "a.txt", time.Minute); err != nil {
		t.Fatalf("DownloadURL = %v, want nil after recovery", err)
	}
	if fos.attempts["DownloadURL"] != failures+1 {
		t.Fatalf("DownloadURL attempts = %d, want %d", fos.attempts["DownloadURL"], failures+1)
	}
	if hookCalls != 2 {
		t.Errorf("onChange calls = %d, want 2 across %d retries: the hook is per TRANSITION", hookCalls, failures)
	}
	if got := len(sink.all()); got != 2 {
		t.Errorf("log lines = %d, want 2 across %d retries: %v", got, failures, sink.all())
	}
}

// TestS3Resilience_BudgetExpiryYieldsTheOriginalError: a backend that never
// comes back must stop being waited on, hand back the FIRST error rather than a
// synthetic one, and leave the state reconnecting, because the budget running
// out is not evidence of recovery.
func TestS3Resilience_BudgetExpiryYieldsTheOriginalError(t *testing.T) {
	fos := newFakeObjectStore()
	fos.failErr, fos.failLeft = connErr(), -1 // never recovers
	res, _ := newTestRes(time.Millisecond, 10*time.Millisecond)

	p := NewS3ResilientProvider(&S3Provider{os: fos, prefix: "library"}, res)
	_, err := p.ListFiles(context.Background(), "/")
	if err == nil {
		t.Fatal("ListFiles = nil error after the budget expired, want the original failure")
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Errorf("ListFiles err = %v (%T), want the ORIGINAL *net.OpError, not a synthetic deadline error", err, err)
	}
	if reconnecting, _, last := res.State(); !reconnecting {
		t.Error("state = ok after the budget expired, want it to stay reconnecting")
	} else if last == nil {
		t.Error("state lastErr = nil while reconnecting, want the cause")
	}
	if fos.attempts["List"] < 2 {
		t.Errorf("List attempts = %d, want at least 2 before giving up", fos.attempts["List"])
	}
}

// TestS3Resilience_RefusalsPropagateImmediately is the guard against the worst
// failure mode of this whole mechanism: pausing ten minutes to re-confirm a
// permission error or a missing object. Exactly one attempt, error passed
// straight through, no state change.
func TestS3Resilience_RefusalsPropagateImmediately(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"403 AccessDenied", accessDenied()},
		{"types.NoSuchKey", &types.NoSuchKey{}},
		{"NotFound, the existing isS3NotFound case", &smithy.GenericAPIError{Code: "NotFound"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fos := newFakeObjectStore()
			fos.failErr, fos.failLeft = tt.err, -1
			// A budget long enough that a retry loop would hang the test rather
			// than quietly pass it.
			res, sink := newTestRes(time.Second, time.Hour)
			var hookCalls int
			res.SetOnChange(func(bool, time.Time, error) { hookCalls++ })

			p := NewS3ResilientProvider(&S3Provider{os: fos, prefix: "library"}, res)

			done := make(chan error, 1)
			go func() { _, err := p.ListFiles(context.Background(), "/"); done <- err }()

			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("ListFiles = nil, want %v propagated", tt.err)
				}
				if !errors.Is(err, tt.err) {
					t.Errorf("ListFiles err = %v, want the refusal passed through unchanged", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("ListFiles did not return promptly: a refusal was fed into the retry loop")
			}

			if fos.attempts["List"] != 1 {
				t.Errorf("List attempts = %d, want exactly 1: a refusal must never be retried", fos.attempts["List"])
			}
			if reconnecting, _, _ := res.State(); reconnecting {
				t.Error("state = reconnecting after a refusal, want no state change")
			}
			if hookCalls != 0 {
				t.Errorf("onChange calls = %d, want 0: a refusal is not a connection transition", hookCalls)
			}
			if got := len(sink.all()); got != 0 {
				t.Errorf("log lines = %d, want 0 for a refusal: %v", got, sink.all())
			}
		})
	}
}

// TestS3Resilience_GetFileRefusalStillNormalizesToNotExist guards the
// interaction between this wrapper and S3Provider.GetFile's fs.ErrNotExist
// normalization, which the storage migration's skip-if-exists probe depends on.
// Wrapping must not swallow or reshape it.
func TestS3Resilience_GetFileRefusalStillNormalizesToNotExist(t *testing.T) {
	fos := newFakeObjectStore()
	fos.failErr, fos.failLeft = &types.NoSuchKey{}, -1
	res, _ := newTestRes(time.Millisecond, time.Minute)

	p := NewS3ResilientProvider(&S3Provider{os: fos, prefix: "library"}, res)
	_, err := p.GetFile(context.Background(), "gone.txt")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("GetFile err = %v, want it to stay errors.Is(fs.ErrNotExist)", err)
	}
	if fos.attempts["Get"] != 1 {
		t.Errorf("Get attempts = %d, want exactly 1", fos.attempts["Get"])
	}
}

// TestS3Resilience_WriteFileIsNeverRetried is the data-integrity guard, and the
// most important test in this file.
//
// objectStore.Put consumes a non-seekable reader. A second attempt would upload
// whatever is LEFT of it as if it were the whole object, writing a silently
// truncated file under the correct name. Exactly one Put, error returned.
func TestS3Resilience_WriteFileIsNeverRetried(t *testing.T) {
	fos := newFakeObjectStore()
	fos.failErr, fos.failLeft = connErr(), 1 // would succeed on a retry
	res, _ := newTestRes(time.Millisecond, time.Minute)

	p := NewS3ResilientProvider(&S3Provider{os: fos, prefix: "library"}, res)
	err := p.WriteFile(context.Background(), "a.txt", strings.NewReader("the-whole-payload"))
	if err == nil {
		t.Fatal("WriteFile = nil, want the connection error: a retry here would truncate the object")
	}
	if fos.attempts["Put"] != 1 {
		t.Fatalf("Put attempts = %d, want exactly 1: WriteFile must never be replayed", fos.attempts["Put"])
	}
	if _, ok := fos.m["library/a.txt"]; ok {
		t.Error("a partial object was stored; the failed write must leave nothing behind")
	}
	// The outage is still RECORDED, which is not the same as retried.
	if reconnecting, _, _ := res.State(); !reconnecting {
		t.Error("state = ok after a transport failure in WriteFile, want reconnecting recorded")
	}
}

// TestS3Resilience_WriteFileWaitsWhileReconnectingThenProceeds: waiting is safe
// because it happens BEFORE the reader is touched. Once the backend is back the
// upload runs, exactly once, and stores the complete payload.
func TestS3Resilience_WriteFileWaitsWhileReconnectingThenProceeds(t *testing.T) {
	fos := newFakeObjectStore()
	res, _ := newTestRes(time.Millisecond, time.Minute)
	// Put the backend into reconnecting without going through a failing call,
	// so this test isolates the WAIT rather than re-testing the retry loop.
	res.report(connErr())
	if reconnecting, _, _ := res.State(); !reconnecting {
		t.Fatal("precondition: want the backend reconnecting")
	}

	p := NewS3ResilientProvider(&S3Provider{os: fos, prefix: "library"}, res)
	done := make(chan error, 1)
	go func() {
		done <- p.WriteFile(context.Background(), "a.txt", strings.NewReader("payload"))
	}()

	// It must still be waiting: nothing has recovered yet.
	select {
	case err := <-done:
		t.Fatalf("WriteFile returned (%v) while the backend was reconnecting, want it to wait", err)
	case <-time.After(20 * time.Millisecond):
	}

	res.recovered()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WriteFile after recovery = %v, want nil", err)
		}
	case <-time.After(testDeadline):
		t.Fatal("WriteFile did not proceed after recovery")
	}

	if fos.attempts["Put"] != 1 {
		t.Errorf("Put attempts = %d, want exactly 1", fos.attempts["Put"])
	}
	if got := string(fos.m["library/a.txt"]); got != "payload" {
		t.Errorf("stored object = %q, want the complete %q", got, "payload")
	}
}

// TestS3Resilience_ContextCancellationDuringWaitReturnsPromptly: a caller that
// goes away must not be held for the rest of the budget. Covers both the write
// path's wait and the retry loop's wait.
func TestS3Resilience_ContextCancellationDuringWaitReturnsPromptly(t *testing.T) {
	tests := []struct {
		name string
		call func(ctx context.Context, p StorageProvider) error
	}{
		{"WriteFile waiting on a reconnecting backend", func(ctx context.Context, p StorageProvider) error {
			return p.WriteFile(ctx, "a.txt", strings.NewReader("payload"))
		}},
		{"ListFiles waiting between retries", func(ctx context.Context, p StorageProvider) error {
			_, err := p.ListFiles(ctx, "/")
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fos := newFakeObjectStore()
			fos.failErr, fos.failLeft = connErr(), -1
			// A long interval so the test can only pass by observing the
			// cancellation, never by the timer firing.
			res, _ := newTestRes(time.Hour, time.Hour)
			res.report(connErr())

			p := NewS3ResilientProvider(&S3Provider{os: fos, prefix: "library"}, res)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- tt.call(ctx, p) }()

			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("err = %v, want context.Canceled", err)
				}
			case <-time.After(testDeadline):
				t.Fatal("call did not return promptly after cancellation")
			}
		})
	}
}

// TestS3Resilience_NilIsSafeAndNeverPauses: a provider built without a
// resilience instance (the connection probe, the migration's candidate config)
// must take the same code path, propagating failures untouched and immediately.
func TestS3Resilience_NilIsSafeAndNeverPauses(t *testing.T) {
	var res *S3Resilience
	if reconnecting, _, last := res.State(); reconnecting || last != nil {
		t.Errorf("nil State() = (%v, _, %v), want (false, _, nil)", reconnecting, last)
	}
	res.SetOnChange(func(bool, time.Time, error) { t.Error("hook fired on a nil receiver") })
	res.report(connErr())
	res.recovered()
	if err := res.waitUntilOK(context.Background()); err != nil {
		t.Errorf("nil waitUntilOK = %v, want nil", err)
	}

	fos := newFakeObjectStore()
	fos.failErr, fos.failLeft = connErr(), -1
	p := NewS3ResilientProvider(&S3Provider{os: fos, prefix: "library"}, nil)
	if _, err := p.ListFiles(context.Background(), "/"); err == nil {
		t.Error("ListFiles with a nil resilience = nil, want the failure propagated")
	}
	if fos.attempts["List"] != 1 {
		t.Errorf("List attempts = %d, want exactly 1 with no resilience wired", fos.attempts["List"])
	}
}
