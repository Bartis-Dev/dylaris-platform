package storagemigrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// ChecksumAlgo is persisted into storage_manifests.algo. SHA-256 rather than
// MD5 on purpose: this work is I/O-bound (every byte hashed has already
// crossed a disk or a network) so SHA-NI makes the CPU cost irrelevant, MD5
// in a shipped open-core product attracts scanner and FIPS findings, and the
// digest stays reusable for content-addressed dedup later.
//
// It is never derived from an S3 ETag: a multipart ETag is md5-of-md5s + "-N",
// not a content MD5, so it is not comparable across backends.
const ChecksumAlgo = "sha256"

// checksumBufSize is the streaming copy buffer. 1 MiB keeps the syscall count
// low on multi-GB archives without holding an object in memory.
const checksumBufSize = 1 << 20

// Checksum streams r through SHA-256 and returns the lower-case hex digest
// plus the number of bytes read. The reader is consumed but never closed;
// the caller owns it.
func Checksum(r io.Reader) (string, int64, error) {
	return ChecksumInto(io.Discard, r)
}

// ChecksumInto copies r into w while hashing it (SHA-256), in a single pass.
// Manifest capture (see manifest.go) uses this when a key carries a
// pre-existing checksum hint in another algorithm: w is a second hash.Hash
// (currently always SHA-512), so both digests come from the one read of the
// source instead of hashing it twice.
func ChecksumInto(w io.Writer, r io.Reader) (string, int64, error) {
	h := sha256.New()
	buf := make([]byte, checksumBufSize)
	n, err := io.CopyBuffer(io.MultiWriter(w, h), r, buf)
	if err != nil {
		return "", n, fmt.Errorf("storagemigrate: checksum stream: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
