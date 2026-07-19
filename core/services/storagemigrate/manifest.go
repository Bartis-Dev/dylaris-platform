package storagemigrate

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"time"

	"dylaris-core/models"
)

// ManifestStore is the narrow store surface the engine needs. Implemented by
// the full store.Store; kept abstract here so this package takes no hard
// dependency on the whole API and so tests need no database.
type ManifestStore interface {
	CreateStorageManifest(m *models.StorageManifest, entries []models.StorageManifestEntry) (int, error)
	GetStorageManifest(id int) (*models.StorageManifest, error)
	ListStorageManifestEntries(manifestID int) ([]models.StorageManifestEntry, error)
}

// CaptureProgress is called once per object, after it has been hashed.
type CaptureProgress func(objectsDone, objectsTotal, bytesDone, bytesTotal int64, currentKey string)

// CaptureOptions carries everything CaptureManifest needs beyond the data set.
type CaptureOptions struct {
	// BackendLabel is a HUMAN description of the backend being captured. It
	// MUST NOT contain credentials: endpoint + bucket + prefix at most. A
	// caller building this for an s3 backend MUST go through
	// SanitizeBackendLabel rather than concatenating the raw endpoint itself,
	// because an s3 endpoint can carry embedded userinfo
	// (https://AKIA...:secret@host) that would otherwise land in Postgres,
	// the panel's manifest list, and the manifest CSV export.
	BackendLabel string
	CreatedBy    string
	Progress     CaptureProgress
	Log          func(line string)
	// Cancelled is polled at every OBJECT boundary, never mid-object.
	Cancelled func() bool
}

// ErrCaptureCancelled is returned when Cancelled reported true mid-run.
var ErrCaptureCancelled = errors.New("storagemigrate: manifest capture cancelled")

// CaptureManifest streams EVERY object in ds, hashes it, and persists the
// resulting inventory as one manifest. A full streaming read of the source is
// its unavoidable cost; the panel states that up front.
//
// The source is never mutated: this function calls only List and Open.
//
// Partial failure is failure. One unreadable object aborts the whole capture
// and persists nothing, because a manifest missing objects would later report
// a perfectly good target as full of "extra" keys. This includes the case
// where an object vanishes between List and Open (see hashObject): the
// capture still aborts, but the error stays errors.Is(err, fs.ErrNotExist)-
// comparable so it is distinguishable from a genuine backend failure.
func CaptureManifest(ctx context.Context, ds DataSet, st ManifestStore, opts CaptureOptions) (*models.StorageManifest, []models.StorageManifestEntry, error) {
	logf := func(format string, args ...interface{}) {
		if opts.Log != nil {
			opts.Log(fmt.Sprintf(format, args...))
		}
	}
	cancelled := func() bool { return opts.Cancelled != nil && opts.Cancelled() }

	refs, err := ds.List(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("capture manifest %s: %w", ds.ID(), err)
	}
	refs = dedupeObjectRefs(refs, logf)

	var listedBytes int64
	for _, r := range refs {
		listedBytes += r.Size
	}
	logf("Inventorying %s: %d objects, roughly %d bytes to read.", ds.Label(), len(refs), listedBytes)

	hinter, _ := ds.(ChecksumHinter)

	entries := make([]models.StorageManifestEntry, 0, len(refs))
	var objectsDone, bytesDone int64
	total := int64(len(refs))

	for _, ref := range refs {
		if cancelled() {
			return nil, nil, ErrCaptureCancelled
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}

		sum, n, err := hashObject(ctx, ds, ref.Key, hinter, logf)
		if err != nil {
			return nil, nil, err
		}

		entries = append(entries, models.StorageManifestEntry{
			Key:      ref.Key,
			Size:     n,
			Checksum: sum,
		})
		objectsDone++
		bytesDone += n
		if opts.Progress != nil {
			opts.Progress(objectsDone, total, bytesDone, bytesDone, ref.Key)
		}
	}

	m := &models.StorageManifest{
		DataSet:      ds.ID(),
		BackendLabel: opts.BackendLabel,
		Algo:         ChecksumAlgo,
		CapturedAt:   time.Now().UTC(),
		ObjectCount:  objectsDone,
		TotalBytes:   bytesDone,
		CreatedBy:    opts.CreatedBy,
	}
	if _, err := st.CreateStorageManifest(m, entries); err != nil {
		return nil, nil, fmt.Errorf("capture manifest %s: persist: %w", ds.ID(), err)
	}
	logf("Manifest #%d captured: %d objects, %d bytes, algo %s.", m.ID, m.ObjectCount, m.TotalBytes, m.Algo)
	return m, entries, nil
}

// dedupeObjectRefs drops any ObjectRef whose Key already appeared, keeping
// the first occurrence, and logs a warning for every one it drops.
//
// This exists because of a carry-forward review finding on the store side:
// storage_manifest_entries has PRIMARY KEY (manifest_id, key), and
// CreateStorageManifest's insert uses ON CONFLICT (manifest_id, key) DO
// NOTHING, so a duplicate key would be silently dropped AT THE STORE while
// the header's ObjectCount (computed here, from objects actually processed)
// would still count it - an over-report of what the manifest actually holds,
// which matters because a later "verify complete" claim rests on this count.
// The fix belongs in the writer, not the store, because this function cannot
// assume every DataSet.List implementation is duplicate-free (a provider
// mirrored across N paths - see storage/modpack.ModpackStorageProvider's
// local backend - is the plausible real-world shape). Deduping here, before
// any object is opened, also means a duplicate key is never hashed twice.
func dedupeObjectRefs(refs []ObjectRef, logf func(string, ...interface{})) []ObjectRef {
	seen := make(map[string]bool, len(refs))
	out := make([]ObjectRef, 0, len(refs))
	for _, r := range refs {
		if seen[r.Key] {
			logf("WARNING: %s: duplicate key returned by the data set listing; only the first occurrence is captured.", r.Key)
			continue
		}
		seen[r.Key] = true
		out = append(out, r)
	}
	return out
}

// hashObject streams one object, returns its SHA-256 and true byte count, and
// runs the opportunistic hint cross-check.
//
// The size recorded is what was ACTUALLY read, not the size the listing
// reported: a backend listing is a hint, and an object that changed between
// List and Open must be recorded truthfully rather than half-believed.
func hashObject(ctx context.Context, ds DataSet, key string, hinter ChecksumHinter, logf func(string, ...interface{})) (string, int64, error) {
	rc, err := ds.Open(ctx, key)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// The object was listed a moment ago but is gone now: a genuine
			// race between List and Open (concurrent delete, a retention/
			// cleanup job, ...), deliberately handled as its OWN branch
			// rather than falling through the generic wrap below. Today it
			// aborts the capture exactly like any other read failure - a
			// manifest entry for an object that turned out not to exist
			// would be precisely the kind of inaccuracy this system exists
			// to prevent - but the error stays errors.Is(err, fs.ErrNotExist)
			// through the %w wrap, so it is still distinguishable from a
			// transient backend failure for any future caller that wants to
			// react differently (e.g. retry the whole capture) to "vanished"
			// versus "backend is unhealthy".
			return "", 0, fmt.Errorf("capture manifest: %s vanished between listing and read: %w", key, err)
		}
		return "", 0, fmt.Errorf("capture manifest: read %s: %w", key, err)
	}
	defer rc.Close()

	if hinter == nil {
		sum, n, err := Checksum(rc)
		if err != nil {
			return "", 0, fmt.Errorf("capture manifest: hash %s: %w", key, err)
		}
		return sum, n, nil
	}

	// A hint exists for some keys only, and it is always SHA-512 today.
	// Compute both in one pass so the source is read exactly once.
	algo, want, ok := hinter.ChecksumHint(key)
	if !ok || algo != "sha512" {
		sum, n, err := Checksum(rc)
		if err != nil {
			return "", 0, fmt.Errorf("capture manifest: hash %s: %w", key, err)
		}
		return sum, n, nil
	}
	h5 := sha512.New()
	sum, n, err := ChecksumInto(h5, rc)
	if err != nil {
		return "", 0, fmt.Errorf("capture manifest: hash %s: %w", key, err)
	}
	got := hex.EncodeToString(h5.Sum(nil))
	if !strings.EqualFold(got, want) {
		// Opportunistic signal about PRE-EXISTING data. Never persisted,
		// never fatal: the manifest checksum stays the fresh SHA-256.
		logf("WARNING: %s: stored sha512 %s does not match the object on disk (%s). The manifest records the freshly computed %s; investigate this object separately.",
			key, want, got, ChecksumAlgo)
	}
	return sum, n, nil
}

// EntriesByKey indexes manifest entries for O(1) lookup during copy and
// verification. Always returns a non-nil map.
func EntriesByKey(entries []models.StorageManifestEntry) map[string]models.StorageManifestEntry {
	out := make(map[string]models.StorageManifestEntry, len(entries))
	for _, e := range entries {
		out[e.Key] = e
	}
	return out
}

// SanitizeBackendLabel builds the backend_label persisted with a manifest for
// an s3-backed data set: "s3:" + endpoint + "/" + bucket + "/" + prefix. It is
// the ONE place in the codebase allowed to build that string.
//
// Why this exists (Task 7 review carry-forward, security): an operator can
// configure an S3-compatible endpoint with embedded userinfo
// (https://AKIA...:secret@minio.internal). Interpolated naively, that
// persists a credential into Postgres, renders it in the panel's manifest
// list, and exports it in the manifest CSV. This function parses endpoint
// with net/url and strips any userinfo before formatting, so any caller that
// builds an s3 BackendLabel through here cannot leak a credential that way.
//
// endpoint is tried two ways: first as given (covers the normal
// "scheme://user:pass@host" shape, where net/url populates URL.User
// directly), and, only if that parse produced no userinfo AND the string
// still contains "@", re-parsed with a "//" prefix (covers a schemeless
// "user:pass@host[:port]" endpoint, which net/url would otherwise read as an
// opaque "scheme:opaque" pair with no recognized userinfo at all). An
// endpoint that does not parse as a URL by either attempt - or has no "@" to
// begin with - is used verbatim: this function must never panic or drop a
// valid-but-unusual endpoint, only strip a credential it can actually
// recognize. (In practice the S3 client here requires a schemed endpoint, so
// the schemeless case is defense in depth, not the expected input shape.)
func SanitizeBackendLabel(endpoint, bucket, prefix string) string {
	return "s3:" + sanitizeEndpoint(endpoint) + "/" + bucket + "/" + prefix
}

func sanitizeEndpoint(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil && u.User != nil {
		u.User = nil
		return u.String()
	}
	if strings.Contains(endpoint, "@") {
		if u, err := url.Parse("//" + endpoint); err == nil && u.User != nil {
			u.User = nil
			return strings.TrimPrefix(u.String(), "//")
		}
	}
	return endpoint
}
