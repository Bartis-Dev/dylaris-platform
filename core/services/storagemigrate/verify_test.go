package storagemigrate

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"dylaris-core/models"
)

func manifestHeader(id int, entries []models.StorageManifestEntry) *models.StorageManifest {
	var bytes int64
	for _, e := range entries {
		bytes += e.Size
	}
	return &models.StorageManifest{
		ID:          id,
		DataSet:     DataSetLibrary,
		Algo:        ChecksumAlgo,
		CapturedAt:  time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC),
		ObjectCount: int64(len(entries)),
		TotalBytes:  bytes,
	}
}

func statusOf(rep StorageVerifyReport, key string) StorageVerifyStatus {
	for _, p := range rep.Problems {
		if p.Key == key {
			return p.Status
		}
	}
	return VerifyOK
}

func TestVerify_AllGoodIsOK(t *testing.T) {
	ctx := context.Background()
	src := newMemDataSet(DataSetLibrary, "source")
	src.put("a.jar", "aaa")
	src.put("b.jar", "bbbb")
	entries := manifestOf(t, src)

	target := newMemDataSet(DataSetLibrary, "target")
	target.put("a.jar", "aaa")
	target.put("b.jar", "bbbb")

	rep, err := Verify(ctx, target, manifestHeader(1, entries), entries, VerifyModeFull, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.OK {
		t.Fatalf("OK = false, problems = %+v", rep.Problems)
	}
	if rep.ProblemsTotal != 0 {
		t.Errorf("ProblemsTotal = %d, want 0", rep.ProblemsTotal)
	}
	if rep.Mode != VerifyModeFull {
		t.Errorf("Mode = %q, want full", rep.Mode)
	}
	if rep.ManifestID != 1 {
		t.Errorf("ManifestID = %d, want 1", rep.ManifestID)
	}
	if rep.ObjectsInManifest != 2 || rep.ObjectsChecked != 2 {
		t.Errorf("objects = %d/%d, want 2/2", rep.ObjectsChecked, rep.ObjectsInManifest)
	}
	if rep.BytesInManifest != 7 || rep.BytesChecked != 7 {
		t.Errorf("bytes = %d/%d, want 7/7", rep.BytesChecked, rep.BytesInManifest)
	}
	if rep.CheckedFraction != 1 || rep.BytesFraction != 1 {
		t.Errorf("fractions = %v/%v, want 1/1", rep.CheckedFraction, rep.BytesFraction)
	}
}

func TestVerify_ClassifiesEveryStatus(t *testing.T) {
	ctx := context.Background()
	src := newMemDataSet(DataSetLibrary, "source")
	src.put("ok.jar", "good")
	src.put("missing.jar", "gone")
	src.put("sizediff.jar", "12345")
	src.put("contentdiff.jar", "AAAAAAAA")
	src.put("unreadable.jar", "boom")
	entries := manifestOf(t, src)

	target := newMemDataSet(DataSetLibrary, "target")
	target.put("ok.jar", "good")
	// missing.jar deliberately absent
	target.put("sizediff.jar", "123")         // shorter
	target.put("contentdiff.jar", "AAAAAAAB") // SAME SIZE, different bytes
	target.put("unreadable.jar", "boom")
	target.openErr["unreadable.jar"] = errors.New("api error AccessDenied")
	target.put("extra.jar", "nobody asked for this")

	rep, err := Verify(ctx, target, manifestHeader(2, entries), entries, VerifyModeFull, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK {
		t.Fatal("OK = true despite problems")
	}
	want := map[string]StorageVerifyStatus{
		"ok.jar":          VerifyOK,
		"missing.jar":     VerifyMissing,
		"sizediff.jar":    VerifySizeMismatch,
		"contentdiff.jar": VerifyChecksumMismatch,
		"unreadable.jar":  VerifyUnreadable,
		"extra.jar":       VerifyExtra,
	}
	for key, wantStatus := range want {
		if got := statusOf(rep, key); got != wantStatus {
			t.Errorf("%s status = %q, want %q", key, got, wantStatus)
		}
	}
	if rep.ProblemsTotal != 5 {
		t.Errorf("ProblemsTotal = %d, want 5", rep.ProblemsTotal)
	}
}

func TestVerify_SameSizeDifferentContentIsOnlyCaughtByTheChecksum(t *testing.T) {
	// The load-bearing case. A size-only comparison passes this.
	ctx := context.Background()
	src := newMemDataSet(DataSetLibrary, "source")
	src.put("a.jar", "AAAAAAAA")
	entries := manifestOf(t, src)

	target := newMemDataSet(DataSetLibrary, "target")
	target.put("a.jar", "AAAAAAAB")

	rep, err := Verify(ctx, target, manifestHeader(1, entries), entries, VerifyModeFull, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK {
		t.Fatal("OK = true for same-size different-content")
	}
	if statusOf(rep, "a.jar") != VerifyChecksumMismatch {
		t.Errorf("status = %q, want checksum_mismatch", statusOf(rep, "a.jar"))
	}
}

func TestVerify_OKIsFalseWheneverProblemsTotalIsPositive(t *testing.T) {
	ctx := context.Background()
	src := newMemDataSet(DataSetLibrary, "source")
	src.put("a.jar", "aaa")
	entries := manifestOf(t, src)
	target := newMemDataSet(DataSetLibrary, "target") // empty -> one "missing"

	rep, err := Verify(ctx, target, manifestHeader(1, entries), entries, VerifyModeFull, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.ProblemsTotal == 0 {
		t.Fatal("ProblemsTotal = 0, want 1")
	}
	if rep.OK {
		t.Fatal("OK must be false whenever ProblemsTotal > 0")
	}
}

func TestVerify_ProblemsAreCappedButProblemsTotalIsTruthful(t *testing.T) {
	ctx := context.Background()
	src := newMemDataSet(DataSetLibrary, "source")
	n := verifyMaxProblems + 37
	for i := 0; i < n; i++ {
		src.put(fmt.Sprintf("f%04d.jar", i), "x")
	}
	entries := manifestOf(t, src)
	target := newMemDataSet(DataSetLibrary, "target") // everything missing

	rep, err := Verify(ctx, target, manifestHeader(1, entries), entries, VerifyModeFull, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(rep.Problems) != verifyMaxProblems {
		t.Errorf("len(Problems) = %d, want the cap %d", len(rep.Problems), verifyMaxProblems)
	}
	if rep.ProblemsTotal != n {
		t.Errorf("ProblemsTotal = %d, want the TRUE count %d", rep.ProblemsTotal, n)
	}
}

func TestVerify_PresenceIsNeverSampled(t *testing.T) {
	// "sample" only ever reduces how many objects get their CONTENTS hashed.
	// missing and extra come from a full key listing, which costs no bytes,
	// and are always computed in full - in BOTH modes.
	ctx := context.Background()
	src := newMemDataSet(DataSetLibrary, "source")
	for i := 0; i < 50; i++ {
		src.put(fmt.Sprintf("f%03d.jar", i), "x")
	}
	entries := manifestOf(t, src)

	target := newMemDataSet(DataSetLibrary, "target")
	for i := 0; i < 50; i++ {
		target.put(fmt.Sprintf("f%03d.jar", i), "x")
	}
	target.Delete(ctx, "f007.jar")
	target.put("surprise.jar", "y")

	rep, err := Verify(ctx, target, manifestHeader(1, entries), entries, VerifyModeSample, VerifyOptions{
		Rand: rand.New(rand.NewSource(1)),
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if statusOf(rep, "f007.jar") != VerifyMissing {
		t.Errorf("f007.jar status = %q, want missing (presence is never sampled)", statusOf(rep, "f007.jar"))
	}
	if statusOf(rep, "surprise.jar") != VerifyExtra {
		t.Errorf("surprise.jar status = %q, want extra (presence is never sampled)", statusOf(rep, "surprise.jar"))
	}
}

func TestSampleLargeCount(t *testing.T) {
	// n = min(|L|, max(200, ceil(0.10 * |L|)))
	cases := []struct {
		numLarge int
		want     int
	}{
		{0, 0},
		{1, 1},        // ceil(0.1)=1 -> max(200,1)=200 -> min(1,200)=1
		{199, 199},    // max(200,20)=200 -> min(199,200)=199
		{200, 200},    // max(200,20)=200 -> min(200,200)=200
		{201, 200},    // max(200,21)=200 -> min(201,200)=200
		{2000, 200},   // max(200,200)=200
		{3000, 300},   // max(200,300)=300
		{10000, 1000}, // max(200,1000)=1000
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("L=%d", c.numLarge), func(t *testing.T) {
			if got := sampleLargeCount(c.numLarge); got != c.want {
				t.Errorf("sampleLargeCount(%d) = %d, want %d", c.numLarge, got, c.want)
			}
		})
	}
}

func TestSelectSample_AlwaysIncludesEverySmallObject(t *testing.T) {
	var entries []models.StorageManifestEntry
	for i := 0; i < 500; i++ {
		entries = append(entries, models.StorageManifestEntry{
			Key: fmt.Sprintf("small%03d", i), Size: sampleSmallObjectBytes, Checksum: "c",
		})
	}
	for i := 0; i < 1000; i++ {
		entries = append(entries, models.StorageManifestEntry{
			Key: fmt.Sprintf("large%04d", i), Size: sampleSmallObjectBytes + 1, Checksum: "c",
		})
	}

	got := SelectSample(entries, rand.New(rand.NewSource(7)))
	smalls, larges := 0, 0
	for _, e := range got {
		if e.Size <= sampleSmallObjectBytes {
			smalls++
		} else {
			larges++
		}
	}
	if smalls != 500 {
		t.Errorf("small objects sampled = %d, want all 500 (they are trivial to hash)", smalls)
	}
	if larges != sampleLargeCount(1000) {
		t.Errorf("large objects sampled = %d, want %d", larges, sampleLargeCount(1000))
	}
}

func TestSelectSample_BoundarySizeIsSmall(t *testing.T) {
	// "small" is <= 1 MiB, inclusive.
	entries := []models.StorageManifestEntry{
		{Key: "exact", Size: sampleSmallObjectBytes},
		{Key: "over", Size: sampleSmallObjectBytes + 1},
	}
	got := SelectSample(entries, rand.New(rand.NewSource(1)))
	var haveExact bool
	for _, e := range got {
		if e.Key == "exact" {
			haveExact = true
		}
	}
	if !haveExact {
		t.Error("a 1 MiB object was treated as large; the boundary is inclusive")
	}
}

func TestSelectSample_DrawsWithoutReplacement(t *testing.T) {
	var entries []models.StorageManifestEntry
	for i := 0; i < 3000; i++ {
		entries = append(entries, models.StorageManifestEntry{
			Key: fmt.Sprintf("large%04d", i), Size: sampleSmallObjectBytes * 2, Checksum: "c",
		})
	}
	got := SelectSample(entries, rand.New(rand.NewSource(3)))
	seen := map[string]bool{}
	for _, e := range got {
		if seen[e.Key] {
			t.Fatalf("%s appeared twice; the draw must be without replacement", e.Key)
		}
		seen[e.Key] = true
	}
	if len(got) != sampleLargeCount(3000) {
		t.Errorf("sample size = %d, want %d", len(got), sampleLargeCount(3000))
	}
}

func TestSelectSample_ReturnsSortedByKey(t *testing.T) {
	entries := []models.StorageManifestEntry{
		{Key: "c", Size: 1}, {Key: "a", Size: 1}, {Key: "b", Size: 1},
	}
	got := SelectSample(entries, rand.New(rand.NewSource(1)))
	var keys []string
	for _, e := range got {
		keys = append(keys, e.Key)
	}
	if strings.Join(keys, ",") != "a,b,c" {
		t.Errorf("sample = %v, want ascending", keys)
	}
}

func TestVerify_SampleReportsTheCheckedFraction(t *testing.T) {
	ctx := context.Background()
	src := newMemDataSet(DataSetLibrary, "source")
	// 3 small (always hashed) + nothing large, so a sample checks everything
	// here; the assertion is that the arithmetic is right, not that it skips.
	src.put("a", "a")
	src.put("b", "bb")
	src.put("c", "ccc")
	entries := manifestOf(t, src)

	target := newMemDataSet(DataSetLibrary, "target")
	target.put("a", "a")
	target.put("b", "bb")
	target.put("c", "ccc")

	rep, err := Verify(ctx, target, manifestHeader(5, entries), entries, VerifyModeSample, VerifyOptions{
		Rand: rand.New(rand.NewSource(1)),
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.Mode != VerifyModeSample {
		t.Errorf("Mode = %q, want sample", rep.Mode)
	}
	if rep.ObjectsInManifest != 3 || rep.ObjectsChecked != 3 {
		t.Errorf("objects = %d/%d, want 3/3", rep.ObjectsChecked, rep.ObjectsInManifest)
	}
	if math.Abs(rep.CheckedFraction-1) > 1e-9 {
		t.Errorf("CheckedFraction = %v, want 1", rep.CheckedFraction)
	}
	if math.Abs(rep.BytesFraction-1) > 1e-9 {
		t.Errorf("BytesFraction = %v, want 1", rep.BytesFraction)
	}
}

func TestVerify_FractionsAreZeroSafeOnAnEmptyManifest(t *testing.T) {
	rep, err := Verify(context.Background(), newMemDataSet(DataSetLibrary, "target"),
		manifestHeader(1, nil), nil, VerifyModeFull, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify(empty): %v", err)
	}
	if !rep.OK {
		t.Errorf("an empty manifest against an empty target must verify OK; problems = %+v", rep.Problems)
	}
	if rep.CheckedFraction != 0 || rep.BytesFraction != 0 {
		t.Errorf("fractions = %v/%v, want 0/0 with no division by zero", rep.CheckedFraction, rep.BytesFraction)
	}
}

func TestVerify_RejectsAnUnknownMode(t *testing.T) {
	_, err := Verify(context.Background(), newMemDataSet(DataSetLibrary, "target"),
		manifestHeader(1, nil), nil, "partial", VerifyOptions{})
	if err == nil {
		t.Fatal("Verify with an unknown mode err = nil, want an error")
	}
	if ValidVerifyMode("partial") {
		t.Error("ValidVerifyMode(\"partial\") = true, want false")
	}
	for _, m := range []string{VerifyModeFull, VerifyModeSample} {
		if !ValidVerifyMode(m) {
			t.Errorf("ValidVerifyMode(%q) = false", m)
		}
	}
}

func TestVerify_NeverMutatesTheTarget(t *testing.T) {
	ctx := context.Background()
	src := newMemDataSet(DataSetLibrary, "source")
	src.put("a.jar", "aaa")
	entries := manifestOf(t, src)

	target := newMemDataSet(DataSetLibrary, "target")
	target.put("a.jar", "wrong")
	target.put("extra.jar", "x")
	before := target.snapshot()

	if _, err := Verify(ctx, target, manifestHeader(1, entries), entries, VerifyModeFull, VerifyOptions{}); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	after := target.snapshot()
	if len(before) != len(after) {
		t.Fatalf("target object count changed: %d -> %d; verification never deletes an extra", len(before), len(after))
	}
	if len(target.deleted) != 0 {
		t.Errorf("verification deleted %v; orphan reaping is explicitly out of scope", target.deleted)
	}
}

func TestVerify_HonoursCancellation(t *testing.T) {
	ctx := context.Background()
	src := newMemDataSet(DataSetLibrary, "source")
	src.put("a.jar", "a")
	src.put("b.jar", "b")
	src.put("c.jar", "c")
	entries := manifestOf(t, src)

	target := newMemDataSet(DataSetLibrary, "target")
	target.put("a.jar", "a")
	target.put("b.jar", "b")
	target.put("c.jar", "c")

	calls := 0
	_, err := Verify(ctx, target, manifestHeader(1, entries), entries, VerifyModeFull, VerifyOptions{
		Cancelled: func() bool { calls++; return calls > 1 },
	})
	if !errors.Is(err, ErrVerifyCancelled) {
		t.Fatalf("Verify err = %v, want ErrVerifyCancelled", err)
	}
}

// --- Additions beyond the brief: the two safety properties the task's own
// requirements call out by name (see task-11-report.md for the rationale). ---

// TestVerify_ChecksumIsAuthoritativeOverACorruptManifestSizeField is the
// direct test for "do NOT compare Size the way the copy loop's skip check
// does." The copy loop only ever uses a Size/checksum disagreement to decide
// skip-vs-recopy, which is harmless either way. A verify report is different:
// it is read by a human and (in full mode) authorizes a source delete, so a
// corrupt Size column on an otherwise-perfect manifest entry must NOT turn
// into a stored, permanent "size_mismatch" failure when the checksum -
// computed from the bytes actually read - proves the content is correct.
func TestVerify_ChecksumIsAuthoritativeOverACorruptManifestSizeField(t *testing.T) {
	ctx := context.Background()
	target := newMemDataSet(DataSetLibrary, "target")
	body := "the real, byte-for-byte correct content of this object"
	target.put("a.jar", body)

	sum, n, err := Checksum(strings.NewReader(body))
	if err != nil {
		t.Fatalf("Checksum: %v", err)
	}
	// The manifest's Size column is wrong (corrupted, or a bad hand-edit) but
	// the Checksum column is exactly right for the real content above.
	entries := []models.StorageManifestEntry{
		{Key: "a.jar", Size: n + 999, Checksum: sum},
	}

	rep, err := Verify(ctx, target, manifestHeader(9, entries), entries, VerifyModeFull, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if statusOf(rep, "a.jar") != VerifyOK {
		t.Errorf("a.jar status = %q, want ok: the checksum matches, so a corrupt manifest Size field must not fail the object", statusOf(rep, "a.jar"))
	}
	if !rep.OK {
		t.Fatalf("OK = false; problems = %+v (a checksum match must win over a bad Size column)", rep.Problems)
	}
}

// TestVerify_AuthorizesSourceDeleteOnlyForAPassingFullRun is the structural
// test named in the task brief: "there must be a test that would FAIL if a
// sample verify could authorize a delete." AuthorizesSourceDelete is the one
// and only sanctioned way to ask that question; if it were implemented as
// merely "return r.OK" (ignoring Mode), this test fails on the sample case
// below even though every object actually matches.
func TestVerify_AuthorizesSourceDeleteOnlyForAPassingFullRun(t *testing.T) {
	ctx := context.Background()
	src := newMemDataSet(DataSetLibrary, "source")
	src.put("a.jar", "aaa")
	entries := manifestOf(t, src)

	target := newMemDataSet(DataSetLibrary, "target")
	target.put("a.jar", "aaa")

	sampleRep, err := Verify(ctx, target, manifestHeader(1, entries), entries, VerifyModeSample, VerifyOptions{
		Rand: rand.New(rand.NewSource(1)),
	})
	if err != nil {
		t.Fatalf("Verify (sample): %v", err)
	}
	if !sampleRep.OK {
		t.Fatalf("sample OK = false, problems = %+v", sampleRep.Problems)
	}
	if sampleRep.AuthorizesSourceDelete() {
		t.Fatal("a SAMPLE verify report must NEVER authorize a source delete, even when OK is true")
	}

	fullRep, err := Verify(ctx, target, manifestHeader(1, entries), entries, VerifyModeFull, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify (full): %v", err)
	}
	if !fullRep.AuthorizesSourceDelete() {
		t.Fatal("a passing FULL verify report must authorize a source delete")
	}

	// A full run that found a problem must not authorize a delete either.
	target.put("extra.jar", "surprise")
	failingFullRep, err := Verify(ctx, target, manifestHeader(1, entries), entries, VerifyModeFull, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify (full, failing): %v", err)
	}
	if failingFullRep.AuthorizesSourceDelete() {
		t.Fatal("a full verify report with problems must not authorize a source delete")
	}
}
