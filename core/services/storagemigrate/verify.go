package storagemigrate

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/rand"
	"sort"
	"time"

	"dylaris-core/models"
)

// StorageVerifyStatus classifies one key during verification.
type StorageVerifyStatus string

const (
	VerifyOK               StorageVerifyStatus = "ok"
	VerifyMissing          StorageVerifyStatus = "missing" // in manifest, not in target
	VerifyExtra            StorageVerifyStatus = "extra"   // in target, not in manifest
	VerifySizeMismatch     StorageVerifyStatus = "size_mismatch"
	VerifyChecksumMismatch StorageVerifyStatus = "checksum_mismatch"
	VerifyUnreadable       StorageVerifyStatus = "unreadable" // present but Open/read failed
)

// Verification modes.
const (
	VerifyModeFull   = "full"
	VerifyModeSample = "sample"
)

// ValidVerifyMode gates the API boundary; an unknown mode is rejected rather
// than silently treated as one of the two.
func ValidVerifyMode(m string) bool {
	return m == VerifyModeFull || m == VerifyModeSample
}

// Sampling constants. Hard-coded, NOT admin-configurable: a tunable sample
// size is a footgun that makes verify reports incomparable between runs. If a
// real data set makes these wrong, change them here, with a test.
const (
	// sampleSmallObjectBytes is the inclusive upper bound for "small".
	sampleSmallObjectBytes = 1 << 20
	sampleMinLargeObjects  = 200
	sampleLargeFraction    = 0.10
	// verifyMaxProblems caps the stored problem list. ProblemsTotal always
	// carries the true count; the full list is available via the CSV export.
	verifyMaxProblems = 500
)

// StorageVerifyEntry is one classified key.
type StorageVerifyEntry struct {
	Key          string              `json:"key"`
	Status       StorageVerifyStatus `json:"status"`
	ExpectedSize int64               `json:"expectedSize"`
	ActualSize   int64               `json:"actualSize"`
}

// StorageVerifyReport is the result of comparing a CURRENT target against a
// STORED manifest.
//
// OK is true only when ProblemsTotal == 0. In sample mode a true OK still
// means "nothing wrong in the sample", which is why CheckedFraction sits next
// to the verdict everywhere it is rendered, and why AuthorizesSourceDelete
// below - the ONE sanctioned way any caller may ask "can I delete the source
// now?" - hard-codes Mode == VerifyModeFull as well as OK. A sample verify
// exists for the manual out-of-band path (an operator confirming data that
// was moved by some other means landed correctly); it can tell you something
// is wrong, but it can never tell you everything is right, so it must never
// by itself justify destroying the source.
//
// Coverage note: for the modpacks data set, "extra" (and therefore how
// complete this report's picture of the target really is) is bounded by
// DataSet.List, which for modpacks enumerates DB-REFERENCED keys only (see
// modpackDataSet.List in adapters.go). A bucket object no DB row points at is
// invisible to the manifest AND to this report's "extra" check in every mode.
// That is a genuine limitation of the current schema; this report cannot and
// does not claim otherwise.
type StorageVerifyReport struct {
	OK                bool      `json:"ok"`
	Mode              string    `json:"mode"`
	ManifestID        int       `json:"manifestId"`
	CapturedAt        time.Time `json:"capturedAt"`
	ObjectsInManifest int64     `json:"objectsInManifest"`
	ObjectsChecked    int64     `json:"objectsChecked"`
	BytesInManifest   int64     `json:"bytesInManifest"`
	// BytesExamined is the manifest size of the objects this run ATTEMPTED to
	// read, i.e. the population ObjectsChecked counts. It is the honest
	// denominator for BytesChecked: pairing BytesChecked with BytesInManifest
	// instead mixes a sampled numerator with a whole-manifest denominator, the
	// same defect already fixed once for the progress bar below, and it makes a
	// clean sample report read as though most bytes had failed to read. In full
	// mode it equals BytesInManifest.
	BytesExamined   int64                `json:"bytesExamined"`
	BytesChecked    int64                `json:"bytesChecked"`
	CheckedFraction float64              `json:"checkedFraction"`
	BytesFraction   float64              `json:"bytesCheckedFraction"`
	Problems        []StorageVerifyEntry `json:"problems"`
	ProblemsTotal   int                  `json:"problemsTotal"`
	Log             []string             `json:"log"`
}

// AuthorizesSourceDelete reports whether this report may be used to justify
// deleting the source data set. This is the ONLY sanctioned way to ask that
// question.
//
// A sample-mode report can never return true here, no matter how clean OK
// looks: sampling only ever hashed the CONTENTS of a bounded subset of the
// larger objects (see SelectSample), so an OK sample proves less than an OK
// full run. Every caller that gates a delete must reach this method rather
// than re-deriving the rule from OK and Mode itself, so the safety-critical
// check exists in exactly one place instead of being reimplemented, and
// possibly gotten wrong, at every call site that ever needs to answer this
// question. Reaching it INDIRECTLY satisfies that rule: the migration job
// calls AuthorizeSourceDelete, which calls this method rather than restating
// the predicate, so the rule still has exactly one home.
func (r StorageVerifyReport) AuthorizesSourceDelete() bool {
	return r.OK && r.Mode == VerifyModeFull
}

// VerifyOptions carries the orchestration hooks. Rand is injectable so the
// sampling is deterministic under test; production leaves it nil and gets a
// crypto/rand-seeded generator, so an adversary cannot predict which large
// objects go unchecked.
type VerifyOptions struct {
	Progress  CopyProgress
	Log       func(line string)
	Cancelled func() bool
	Rand      *rand.Rand
}

// ErrVerifyCancelled is returned when Cancelled reported true mid-run.
var ErrVerifyCancelled = errors.New("storagemigrate: verification cancelled")

// sampleLargeCount implements n = min(|L|, max(200, ceil(0.10 * |L|))).
//
// Rationale: small objects (ticket attachments, individual mods) dominate by
// COUNT but are trivial to hash, so all of them are hashed. Large objects
// (server backup archives, mrpacks) dominate by BYTES, so a bounded fraction
// is hashed: at least 200 of them, or 10% if that is more, capped at all.
func sampleLargeCount(numLarge int) int {
	if numLarge <= 0 {
		return 0
	}
	n := int(math.Ceil(sampleLargeFraction * float64(numLarge)))
	if n < sampleMinLargeObjects {
		n = sampleMinLargeObjects
	}
	if n > numLarge {
		n = numLarge
	}
	return n
}

// SelectSample returns every small entry plus a uniformly-drawn, without-
// replacement subset of the large ones, sorted ascending by key.
//
// The draw is randomized (not, say, "the first N by key") on purpose: an
// operator or an attacker who could predict which large objects go unchecked
// could corrupt exactly those and still see the sample pass. rnd is supplied
// by the caller so tests are deterministic; production always passes nil and
// gets a crypto/rand-seeded generator (newSecureRand) instead, so that
// prediction is not possible outside of tests.
func SelectSample(entries []models.StorageManifestEntry, rnd *rand.Rand) []models.StorageManifestEntry {
	if rnd == nil {
		rnd = newSecureRand()
	}
	var small, large []models.StorageManifestEntry
	for _, e := range entries {
		if e.Size <= sampleSmallObjectBytes {
			small = append(small, e)
			continue
		}
		large = append(large, e)
	}
	// Sort the large set first so the shuffle is applied to a deterministic
	// starting order; the randomness then comes only from rnd.
	sort.Slice(large, func(i, j int) bool { return large[i].Key < large[j].Key })
	rnd.Shuffle(len(large), func(i, j int) { large[i], large[j] = large[j], large[i] })

	out := append([]models.StorageManifestEntry(nil), small...)
	out = append(out, large[:sampleLargeCount(len(large))]...)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// newSecureRand seeds math/rand from crypto/rand. math/rand is fine for
// choosing WHICH objects to check; what matters is that the choice is not
// predictable to someone who might have tampered with a specific object.
func newSecureRand() *rand.Rand {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return rand.New(rand.NewSource(int64(binary.LittleEndian.Uint64(b[:]))))
}

// Verify compares the CURRENT target against a STORED manifest.
//
// Presence checks (missing / extra) require only a full key listing, which
// costs no bytes, so they are ALWAYS computed in full in BOTH modes. Sampling
// only ever reduces how many objects get their CONTENTS hashed. This is stated
// explicitly because "sample" would otherwise be ambiguous.
//
// Per object, the CHECKSUM is authoritative, not Size: a SHA-256 match over
// the bytes actually read already proves the content is correct, regardless
// of what the manifest's Size column says. This deliberately does NOT mirror
// the copy loop's skip check (targetAlreadyMatches), which requires BOTH
// checksum and size to match - that is fine there because a false mismatch
// only costs a harmless re-copy. Here, a false mismatch would be reported to
// a human as a permanent verify failure and could block a delete over
// nothing but a corrupt Size column, so a checksum match short-circuits
// straight to "ok" and only a checksum MISMATCH falls through to distinguish
// size_mismatch (sizes disagree too - most likely a truncated read) from
// checksum_mismatch (same size, different bytes).
//
// Verification never writes to the target: an "extra" key is reported, never
// deleted. Orphan reaping is a separate feature.
func Verify(ctx context.Context, target DataSet, manifest *models.StorageManifest, entries []models.StorageManifestEntry, mode string, opts VerifyOptions) (StorageVerifyReport, error) {
	if !ValidVerifyMode(mode) {
		return StorageVerifyReport{}, fmt.Errorf("storagemigrate: unknown verify mode %q (want %q or %q)", mode, VerifyModeFull, VerifyModeSample)
	}
	cancelled := func() bool { return opts.Cancelled != nil && opts.Cancelled() }

	rep := StorageVerifyReport{
		Mode:     mode,
		Problems: []StorageVerifyEntry{},
		Log:      []string{},
	}
	if manifest != nil {
		rep.ManifestID = manifest.ID
		rep.CapturedAt = manifest.CapturedAt
	}
	logf := func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		rep.Log = append(rep.Log, line)
		if opts.Log != nil {
			opts.Log(line)
		}
	}

	expected := EntriesByKey(entries)
	for _, e := range entries {
		rep.ObjectsInManifest++
		rep.BytesInManifest += e.Size
	}

	problems := 0
	addProblem := func(p StorageVerifyEntry) {
		problems++
		if len(rep.Problems) < verifyMaxProblems {
			rep.Problems = append(rep.Problems, p)
		}
	}

	// --- presence: always full, in both modes ---
	present, err := target.List(ctx)
	if err != nil {
		return StorageVerifyReport{}, fmt.Errorf("storagemigrate: list target: %w", err)
	}
	presentKeys := make(map[string]int64, len(present))
	for _, p := range present {
		presentKeys[p.Key] = p.Size
	}
	for _, p := range present {
		if _, want := expected[p.Key]; !want {
			addProblem(StorageVerifyEntry{Key: p.Key, Status: VerifyExtra, ActualSize: p.Size})
		}
	}
	logf("Presence checked in full: %d objects in the manifest, %d on the target.", rep.ObjectsInManifest, len(present))

	// --- contents: full, or the sample ---
	toHash := entries
	if mode == VerifyModeSample {
		toHash = SelectSample(entries, opts.Rand)
		logf("Sample mode: hashing %d of %d objects (every object <= %d bytes, plus a bounded draw of the larger ones).",
			len(toHash), len(entries), int64(sampleSmallObjectBytes))
	}
	sorted := append([]models.StorageManifestEntry(nil), toHash...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })

	total := int64(len(sorted))
	// hashBytesTotal and objectsSkipped describe the SAME population that
	// objectsTotal (= total, above) already does: the objects this loop
	// actually hashes - every entry in full mode, only the sample in sample
	// mode. Pairing "total" here with rep.BytesInManifest (the FULL
	// manifest) would repeat the exact mixed-denominator bug already fixed
	// once in CaptureManifest (Task 9): a progress bar whose object count
	// and byte count describe two differently-sized populations, e.g.
	// objects reading 100% while bytes sit at 12%. objectsSkipped is 0 in
	// full mode (sorted == entries, nothing excluded) and, in sample mode,
	// the true count of manifest entries this loop will never visit at all
	// because SelectSample did not draw them - reported here instead of a
	// hard-coded 0 so a sample-mode progress bar can show that count
	// alongside the bar.
	var hashBytesTotal int64
	for _, e := range sorted {
		hashBytesTotal += e.Size
	}
	objectsSkipped := rep.ObjectsInManifest - total
	// The report gets the same population figure the progress bar uses, so the
	// panel can put BytesChecked over a denominator that describes the objects
	// this run actually attempted rather than the whole manifest.
	rep.BytesExamined = hashBytesTotal

	for _, e := range sorted {
		if cancelled() {
			return StorageVerifyReport{}, ErrVerifyCancelled
		}
		if err := ctx.Err(); err != nil {
			return StorageVerifyReport{}, err
		}

		rc, err := target.Open(ctx, e.Key)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				addProblem(StorageVerifyEntry{Key: e.Key, Status: VerifyMissing, ExpectedSize: e.Size})
			} else {
				// ANY other failure - throttle, 503, permission, timeout - is a
				// verification FAILURE, never "absent". Collapsing the two
				// would let a target that merely hiccupped be reported as
				// missing (or worse, silently dropped), exactly the mistake
				// this project has made once already.
				addProblem(StorageVerifyEntry{Key: e.Key, Status: VerifyUnreadable, ExpectedSize: e.Size})
			}
			rep.ObjectsChecked++
			if opts.Progress != nil {
				opts.Progress(rep.ObjectsChecked, objectsSkipped, total, rep.BytesChecked, hashBytesTotal, e.Key)
			}
			continue
		}
		sum, n, herr := Checksum(rc)
		rc.Close()
		switch {
		case herr != nil:
			addProblem(StorageVerifyEntry{Key: e.Key, Status: VerifyUnreadable, ExpectedSize: e.Size, ActualSize: n})
		case sum == e.Checksum:
			// Checksum matches: the content is proven correct. A Size
			// disagreement here can only mean the manifest's Size COLUMN is
			// wrong, not the object - see the function doc comment - so it is
			// logged, not reported as a problem.
			if n != e.Size {
				logf("NOTE: %s: checksum matches but the manifest Size (%d) does not match the %d bytes read; the stored Size looks wrong, not the object.", e.Key, e.Size, n)
			}
		case n != e.Size:
			addProblem(StorageVerifyEntry{Key: e.Key, Status: VerifySizeMismatch, ExpectedSize: e.Size, ActualSize: n})
		default:
			addProblem(StorageVerifyEntry{Key: e.Key, Status: VerifyChecksumMismatch, ExpectedSize: e.Size, ActualSize: n})
		}

		rep.ObjectsChecked++
		rep.BytesChecked += n
		if opts.Progress != nil {
			opts.Progress(rep.ObjectsChecked, objectsSkipped, total, rep.BytesChecked, hashBytesTotal, e.Key)
		}
	}

	// A manifest key that was neither hashed (not in the sample) nor present
	// on the target is still MISSING - presence is never sampled.
	if mode == VerifyModeSample {
		hashed := EntriesByKey(sorted)
		for _, e := range entries {
			if _, wasHashed := hashed[e.Key]; wasHashed {
				continue
			}
			if _, there := presentKeys[e.Key]; !there {
				addProblem(StorageVerifyEntry{Key: e.Key, Status: VerifyMissing, ExpectedSize: e.Size})
			}
		}
	}

	rep.ProblemsTotal = problems
	rep.OK = problems == 0
	if rep.ObjectsInManifest > 0 {
		rep.CheckedFraction = float64(rep.ObjectsChecked) / float64(rep.ObjectsInManifest)
	}
	if rep.BytesInManifest > 0 {
		rep.BytesFraction = float64(rep.BytesChecked) / float64(rep.BytesInManifest)
	}
	logf("Verification %s: %d problem(s) across %d checked object(s).", verdictWord(rep.OK), rep.ProblemsTotal, rep.ObjectsChecked)
	return rep, nil
}

func verdictWord(ok bool) string {
	if ok {
		return "passed"
	}
	return "FAILED"
}
