package storagemigrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"sort"

	"dylaris-core/models"
)

// CopyProgress is called once per manifest entry, after it has been copied or
// skipped. A skipped object still advances objectsDone and bytesDone, so the
// panel's byte bar tracks completion rather than transfer.
type CopyProgress func(objectsDone, objectsSkipped, objectsTotal, bytesDone, bytesTotal int64, currentKey string)

// CopyOptions carries the orchestration hooks.
type CopyOptions struct {
	Progress  CopyProgress
	Log       func(line string)
	Cancelled func() bool // polled at every OBJECT boundary, never mid-object
}

// CopyResult summarizes one CopyAll run.
type CopyResult struct {
	ObjectsCopied  int64
	ObjectsSkipped int64
	BytesCopied    int64
	BytesTotal     int64
}

// ErrCopyCancelled is returned when Cancelled reported true mid-run.
var ErrCopyCancelled = errors.New("storagemigrate: copy cancelled")

// CopyAll copies every manifest entry from src to target, resumably and
// idempotently.
//
// Per entry, in ascending key order:
//
//  1. If cancelled, unwind. The check is at the OBJECT boundary only, so a
//     cancelled run never leaves a half-written object behind.
//  2. target.Open(key). errors.Is(err, fs.ErrNotExist) means "not there yet"
//     -> copy. ANY other error (503, throttle, permission) is NOT "missing"
//     and fails the object: collapsing the two would let a target that merely
//     hiccupped be overwritten. This is exactly why S3Provider.GetFile and
//     the backup data set normalize only genuine not-found, and why this uses
//     errors.Is rather than a nil check.
//  3. If it opens, stream-hash the target object. Skip ONLY when the checksum
//     AND the size match the manifest - never on mere existence, because a
//     truncated or half-written object from a crashed earlier run is
//     indistinguishable from a good one by existence alone.
//  4. Copy: stream src -> target while hashing in one pass, then compare
//     against the manifest. A mismatch deletes the just-written target object
//     and fails; a corrupt target is worse than an absent one.
//
// The SOURCE is never written to. Re-running a failed migrate with the same
// manifest resumes: everything already verified-identical is skipped.
func CopyAll(ctx context.Context, src, target DataSet, entries []models.StorageManifestEntry, opts CopyOptions) (CopyResult, error) {
	logf := func(format string, args ...interface{}) {
		if opts.Log != nil {
			opts.Log(fmt.Sprintf(format, args...))
		}
	}
	cancelled := func() bool { return opts.Cancelled != nil && opts.Cancelled() }

	// Sort a COPY so a resumed run visits keys in the same order regardless of
	// how the caller obtained them, and so the caller's slice is untouched.
	sorted := append([]models.StorageManifestEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })

	res := CopyResult{}
	for _, e := range sorted {
		res.BytesTotal += e.Size
	}
	total := int64(len(sorted))
	var objectsDone, bytesDone int64

	for _, e := range sorted {
		if cancelled() {
			return res, ErrCopyCancelled
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}

		skip, err := targetAlreadyMatches(ctx, target, e)
		if err != nil {
			return res, err
		}
		if skip {
			res.ObjectsSkipped++
		} else {
			n, err := copyOne(ctx, src, target, e, logf)
			if err != nil {
				return res, err
			}
			res.ObjectsCopied++
			res.BytesCopied += n
		}

		objectsDone++
		bytesDone += e.Size
		if opts.Progress != nil {
			opts.Progress(objectsDone, res.ObjectsSkipped, total, bytesDone, res.BytesTotal, e.Key)
		}
	}
	logf("Copy finished: %d copied, %d skipped (already identical), %d bytes transferred.",
		res.ObjectsCopied, res.ObjectsSkipped, res.BytesCopied)
	return res, nil
}

// targetAlreadyMatches reports whether the target already holds a
// byte-identical object. A genuinely missing key is (false, nil); any other
// open/read failure is returned as an error.
func targetAlreadyMatches(ctx context.Context, target DataSet, e models.StorageManifestEntry) (bool, error) {
	rc, err := target.Open(ctx, e.Key)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("copy: probe target %s: %w", e.Key, err)
	}
	defer rc.Close()
	sum, n, err := Checksum(rc)
	if err != nil {
		return false, fmt.Errorf("copy: hash target %s: %w", e.Key, err)
	}
	return sum == e.Checksum && n == e.Size, nil
}

// copyOne streams one object across, hashing as it writes (single pass), and
// removes the write again if the result does not match the manifest.
func copyOne(ctx context.Context, src, target DataSet, e models.StorageManifestEntry, logf func(string, ...interface{})) (int64, error) {
	rc, err := src.Open(ctx, e.Key)
	if err != nil {
		return 0, fmt.Errorf("copy: read source %s: %w", e.Key, err)
	}
	defer rc.Close()

	hr := newHashingReader(rc)
	if err := target.Write(ctx, e.Key, hr, e.Size); err != nil {
		return 0, fmt.Errorf("copy: write target %s: %w", e.Key, err)
	}
	got, n := hr.sum(), hr.count()
	if got != e.Checksum {
		// A corrupt target object is worse than an absent one: remove it so a
		// verification run between now and the retry cannot report a
		// checksum_mismatch on data the operator might already trust.
		//
		// Checksum only, deliberately NOT also requiring n == e.Size here: hr
		// hashes the exact bytes read from src and handed to target.Write, so
		// a checksum match already implies (SHA-256 collision resistance) that
		// n is the right length for THIS object. The manifest's Size is used
		// as an extra, cheaper belt-and-braces signal in the SKIP decision
		// (targetAlreadyMatches) where it can catch a stale/mismatched entry
		// without a fresh read; re-asserting it here as well would instead let
		// a corrupted Size field on an otherwise-correct manifest entry fail a
		// copy that just proved itself correct by content.
		if delErr := target.Delete(ctx, e.Key); delErr != nil {
			logf("WARNING: %s failed its checksum AND could not be removed from the target: %v. Remove it manually before retrying.", e.Key, delErr)
		}
		return 0, fmt.Errorf("copy: %s checksum mismatch after write (manifest checksum %s, computed %s from %d bytes read); the target object was removed",
			e.Key, e.Checksum, got, n)
	}
	return n, nil
}

// hashingReader tees a stream through SHA-256 as the consumer reads it, so an
// object is written and verified from the exact same bytes with no second
// read of the source.
type hashingReader struct {
	r io.Reader
	h hash.Hash
	n int64
}

func newHashingReader(r io.Reader) *hashingReader {
	return &hashingReader{r: r, h: sha256.New()}
}

func (hr *hashingReader) Read(p []byte) (int, error) {
	n, err := hr.r.Read(p)
	if n > 0 {
		hr.h.Write(p[:n])
		hr.n += int64(n)
	}
	return n, err
}

func (hr *hashingReader) sum() string  { return hex.EncodeToString(hr.h.Sum(nil)) }
func (hr *hashingReader) count() int64 { return hr.n }
