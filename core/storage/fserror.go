package storage

import (
	"errors"
	"io/fs"
	"syscall"
)

// This file answers one question the rest of the storage layer keeps needing:
// when a filesystem call fails, is the BACKEND broken, or was the object
// simply not there?
//
// The distinction drives real behaviour and the two must not be conflated:
//   - absent is routine. A missing key is a 404, and for a delete it is
//     success.
//   - unreachable means every subsequent request to this backend will fail
//     too. That is what the watchdog trips on and what the panel has to show
//     the operator, because retrying is pointless until the mount comes back.
//
// Guessing wrong in either direction is expensive. Treating an unreachable
// mount as "absent" makes Core report an empty library and, worse, makes an
// overwrite look safe when the old data is merely unreadable. Treating a
// missing file as "unreachable" would trip the breaker on a normal 404.

// unreachableErrnos are the errno values a broken or disconnected network
// mount actually produces. Each entry is here for a documented reason, not
// because it sounded network-ish:
//
//	EIO          NFS with the default-adjacent "soft" option returns EIO to the
//	             application once retransmissions are exhausted. Also the generic
//	             transport/media failure.
//	ETIMEDOUT    NFS "softerr" returns this instead of EIO; CIFS reports it while
//	             a server is unresponsive.
//	ESTALE       NFS file handle invalidated, typically a server restart or an
//	             export change. The mount is present but no longer usable as-is.
//	EHOSTDOWN    what a CIFS mount reports when the server is gone (errno 112,
//	             the "Host is down" every SMB operator has seen).
//	EHOSTUNREACH, ENETUNREACH, ENETDOWN, ENETRESET, ECONNRESET, ECONNABORTED,
//	ECONNREFUSED, ENOTCONN, ESHUTDOWN
//	             the transport is down or was torn out from under an in-flight
//	             operation.
//	ENODEV, ENXIO, EREMOTEIO
//	             the underlying device or remote endpoint is gone.
//
// Deliberately ABSENT, and each one matters:
//
//	ENOENT           the object is not there. That is the normal case, not a fault.
//	EACCES, EPERM    a permission problem. Real, but a configuration mistake, and
//	                 tripping the breaker on it would take the whole backend
//	                 offline because one key was chmod'ed wrong.
//	ENOSPC, EDQUOT   the backend is reachable and answering; it is full. Writes
//	                 fail, reads do not, so it is not an availability fault.
var unreachableErrnos = map[syscall.Errno]struct{}{
	syscall.EIO:          {},
	syscall.ETIMEDOUT:    {},
	syscall.ESTALE:       {},
	syscall.EHOSTDOWN:    {},
	syscall.EHOSTUNREACH: {},
	syscall.ENETUNREACH:  {},
	syscall.ENETDOWN:     {},
	syscall.ENETRESET:    {},
	syscall.ECONNRESET:   {},
	syscall.ECONNABORTED: {},
	syscall.ECONNREFUSED: {},
	syscall.ENOTCONN:     {},
	syscall.ESHUTDOWN:    {},
	syscall.ENODEV:       {},
	syscall.ENXIO:        {},
	syscall.EREMOTEIO:    {},
}

// IsBackendUnreachable reports whether err says the storage backend itself is
// unusable rather than that one object is missing.
//
// It answers only for errors that carry a syscall.Errno, which in practice
// means the local/host-path backend. errors.As unwraps *fs.PathError,
// *os.LinkError and *os.SyscallError on the way, so callers pass the error
// they got without unwrapping it first.
//
// It returns false for anything it cannot classify, including every S3 error.
// That is deliberate: false means "this is not evidence of an outage", not
// "the backend is fine". A caller must never read a false as a health signal.
// S3 reachability is the SDK's business and is judged separately.
func IsBackendUnreachable(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	_, ok := unreachableErrnos[errno]
	return ok
}

// IsNotExist reports whether err means the object is absent.
//
// os.IsNotExist alone is not enough here: the S3 backend translates a missing
// key to fs.ErrNotExist by wrapping (see S3Provider.GetFile), and os.IsNotExist
// does not unwrap. Everything in the storage layer should ask this rather than
// picking one of the two idioms per call site and getting it wrong on one
// backend.
func IsNotExist(err error) bool {
	return err != nil && errors.Is(err, fs.ErrNotExist)
}
