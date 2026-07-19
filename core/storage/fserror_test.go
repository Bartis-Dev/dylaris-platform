package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"testing"
)

func TestIsBackendUnreachable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		// Wrapped exactly as the os package hands them over, because that is
		// the only shape this function is ever called with in production.
		{"nfs soft timeout as PathError", &fs.PathError{Op: "open", Path: "/mnt/nas/a", Err: syscall.EIO}, true},
		{"nfs softerr timeout", &fs.PathError{Op: "read", Path: "/mnt/nas/a", Err: syscall.ETIMEDOUT}, true},
		{"nfs stale handle after server restart", &fs.PathError{Op: "stat", Path: "/mnt/nas", Err: syscall.ESTALE}, true},
		{"cifs host is down", &fs.PathError{Op: "open", Path: "/mnt/smb/a", Err: syscall.EHOSTDOWN}, true},
		{"transport reset mid-operation", &fs.PathError{Op: "write", Path: "/mnt/smb/a", Err: syscall.ECONNRESET}, true},
		{"device gone", &fs.PathError{Op: "open", Path: "/mnt/nas/a", Err: syscall.ENODEV}, true},
		{"bare errno, not wrapped", syscall.ETIMEDOUT, true},
		{"wrapped through fmt.Errorf", fmt.Errorf("storage: list %q: %w", "x", &fs.PathError{Err: syscall.EIO}), true},
		{"rename LinkError", &os.LinkError{Op: "rename", Err: syscall.ESTALE}, true},

		// The four that must NOT trip a breaker.
		{"missing file is routine", &fs.PathError{Op: "open", Path: "/data/a", Err: syscall.ENOENT}, false},
		{"permission problem is config, not outage", &fs.PathError{Op: "open", Path: "/data/a", Err: syscall.EACCES}, false},
		{"disk full still answers reads", &fs.PathError{Op: "write", Path: "/data/a", Err: syscall.ENOSPC}, false},
		{"is a directory", &fs.PathError{Op: "read", Path: "/data", Err: syscall.EISDIR}, false},

		// Anything with no errno is unclassifiable, and unclassifiable is not
		// evidence of an outage. Every S3 error lands here.
		{"nil", nil, false},
		{"plain error", errors.New("s3: RequestError: connection reset"), false},
		{"fs.ErrNotExist sentinel without an errno", fs.ErrNotExist, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsBackendUnreachable(tt.err); got != tt.want {
				t.Errorf("IsBackendUnreachable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsNotExist(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"os PathError ENOENT", &fs.PathError{Op: "open", Path: "/data/a", Err: syscall.ENOENT}, true},
		{"bare sentinel", fs.ErrNotExist, true},
		// The S3 backend reports a missing key by wrapping the sentinel, which
		// os.IsNotExist does not see. This case is the reason this helper exists.
		{"s3-style wrapped sentinel", fmt.Errorf("s3 GetFile %s: %w", "a/b.zip", fs.ErrNotExist), true},
		{"nil", nil, false},
		{"unreachable is not absent", &fs.PathError{Op: "open", Path: "/mnt/nas/a", Err: syscall.EIO}, false},
		{"permission denied is not absent", &fs.PathError{Op: "open", Path: "/data/a", Err: syscall.EACCES}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNotExist(tt.err); got != tt.want {
				t.Errorf("IsNotExist(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// The two predicates answer different questions and must never both be true:
// a caller that treats an unreachable mount as "absent" would report an empty
// library, and would let an overwrite proceed over data it merely cannot read.
func TestIsBackendUnreachableAndIsNotExistAreDisjoint(t *testing.T) {
	errs := []error{
		&fs.PathError{Err: syscall.ENOENT},
		&fs.PathError{Err: syscall.EIO},
		&fs.PathError{Err: syscall.ESTALE},
		&fs.PathError{Err: syscall.ETIMEDOUT},
		&fs.PathError{Err: syscall.EACCES},
		fs.ErrNotExist,
		errors.New("unclassifiable"),
	}
	for _, err := range errs {
		if IsBackendUnreachable(err) && IsNotExist(err) {
			t.Errorf("%v classified as BOTH unreachable and absent", err)
		}
	}
}
