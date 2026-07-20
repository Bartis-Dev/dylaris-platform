package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/smithy-go"
)

// The Storage interface promises Stat reports a missing key os.ErrNotExist
// style. S3Storage returned the raw AWS error instead, so the promise held for
// LocalStorage and quietly failed for the backend most deployments actually
// use. These pin it for real, against an endpoint that answers the way S3 does.

// fakeS3 serves just enough of the S3 API for HeadObject, with the status code
// the test wants. Real S3 answers HEAD on a missing key with a bodyless 404,
// which is why the handler writes no body.
func fakeS3(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && status == http.StatusOK {
			w.Header().Set("Content-Length", "4096")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func newFakeS3Storage(t *testing.T, endpoint string) *S3Storage {
	t.Helper()
	// One attempt, not the SDK's default three with backoff: a 500 is
	// retryable and would otherwise make this test slow for no benefit.
	t.Setenv("AWS_MAX_ATTEMPTS", "1")
	cfg, err := json.Marshal(S3Config{
		Endpoint:        endpoint,
		Region:          "us-east-1",
		Bucket:          "backups",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		ForcePathStyle:  true,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	s, err := NewS3(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	return s
}

// TestS3Stat_MissingKeyIsNotExist is the contract. A caller has to be able to
// tell "the object is not there" from "the object store did not answer", and
// only the first of those means the work never happened.
func TestS3Stat_MissingKeyIsNotExist(t *testing.T) {
	s := newFakeS3Storage(t, fakeS3(t, http.StatusNotFound))

	_, err := s.Stat(context.Background(), "backups/gone.tar.gz")
	if err == nil {
		t.Fatal("Stat on a missing key returned no error")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat error = %v, want one satisfying fs.ErrNotExist", err)
	}
}

// TestS3Stat_OtherFailuresAreNotNotExist is the half that keeps the first one
// honest. If every failure reported not-exist, a caller would read an
// unreachable object store as proof that a backup was never written.
func TestS3Stat_OtherFailuresAreNotNotExist(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "the store is broken", status: http.StatusInternalServerError},
		{name: "the credentials are refused", status: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newFakeS3Storage(t, fakeS3(t, tt.status))

			_, err := s.Stat(context.Background(), "backups/some.tar.gz")
			if err == nil {
				t.Fatal("Stat returned no error")
			}
			if errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("Stat error = %v, want NOT fs.ErrNotExist: a failure to answer is not proof of absence", err)
			}
		})
	}
}

// TestS3Stat_PresentKeyReportsItsSize is the positive control.
func TestS3Stat_PresentKeyReportsItsSize(t *testing.T) {
	s := newFakeS3Storage(t, fakeS3(t, http.StatusOK))

	obj, err := s.Stat(context.Background(), "backups/there.tar.gz")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if obj.Size != 4096 {
		t.Fatalf("size = %d, want 4096", obj.Size)
	}
}

// TestIsS3NotFound covers the two codes S3 uses for absence and confirms
// nothing else is mistaken for one. HeadObject answers NotFound; GetObject and
// DeleteObject answer NoSuchKey.
func TestIsS3NotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "head object on a missing key", err: apiError("NotFound"), want: true},
		{name: "get or delete on a missing key", err: apiError("NoSuchKey"), want: true},
		{name: "wrapped", err: fmt.Errorf("stat: %w", apiError("NotFound")), want: true},
		{name: "the bucket is missing, which is not the object being absent", err: apiError("NoSuchBucket"), want: false},
		{name: "credentials refused", err: apiError("AccessDenied"), want: false},
		{name: "not an api error at all", err: errors.New("dial tcp: connection refused"), want: false},
		{name: "no error", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isS3NotFound(tt.err); got != tt.want {
				t.Fatalf("isS3NotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// apiError builds an error carrying an S3 error code, the shape the AWS SDK
// surfaces and the classifier matches on.
type stubAPIError struct{ code string }

func (e *stubAPIError) Error() string                 { return e.code }
func (e *stubAPIError) ErrorCode() string             { return e.code }
func (e *stubAPIError) ErrorMessage() string          { return e.code }
func (e *stubAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultUnknown }

func apiError(code string) error { return &stubAPIError{code: code} }
