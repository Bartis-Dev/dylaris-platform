package storagemigrate

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"dylaris-core/models"
)

// ErrDeleteNotAuthorized is returned when the safety preconditions for
// removing the source are not all met.
var ErrDeleteNotAuthorized = errors.New("storagemigrate: deleting the source is not authorized")

// AuthorizeSourceDelete enforces every safety invariant for the
// deleting_source phase in ONE place, so the API boundary and the job runner
// cannot drift apart. The source may be deleted only when ALL of the
// following hold, checked in this order:
//
//  1. The operator explicitly opted in (deleteSource == true). Never a
//     default, and never implied by a passing verification alone.
//  2. The config switch has already SUCCEEDED (configSwitched == true). This
//     is the phase-ordering guarantee, taken as a parameter rather than
//     assumed from call order: deleting before the switch would leave the
//     live config pointing at deleted data for the width of that window.
//     With configSwitched == false this refuses no matter what else is true,
//     so the bad ordering is unrepresentable rather than merely discouraged.
//  3. The request itself declared a FULL verification (verifyMode ==
//     VerifyModeFull). A sampled run did not check every object, so it can
//     never authorize a delete no matter how it came out.
//  4. A verification report exists and, per its OWN sanctioned predicate -
//     StorageVerifyReport.AuthorizesSourceDelete(), which is
//     "OK && Mode == VerifyModeFull" - authorizes a delete. This method is
//     called rather than re-derived here so the rule exists in exactly one
//     place; a report can arrive from a stored job record, so its own Mode is
//     re-checked rather than trusted from the request's declared verifyMode.
//  5. The manifest the report was computed against is not empty
//     (report.ObjectsInManifest > 0). Verify has no source handle and so
//     structurally cannot tell "the source really is empty" from "the
//     capture step failed and produced nothing" - a full verify of a
//     zero-object manifest reports OK=true, CheckedFraction=0, which
//     AuthorizesSourceDelete cannot see through because it only looks at OK
//     and Mode. This project has already shipped exactly that shape once (a
//     node-local backup source that enumerated as silently empty), so this
//     check is independent of, and in addition to, step 4's predicate rather
//     than trusting the verify verdict alone.
func AuthorizeSourceDelete(deleteSource bool, verifyMode string, report *StorageVerifyReport, configSwitched bool) error {
	if !deleteSource {
		return fmt.Errorf("%w: the run did not opt in to deleting the source", ErrDeleteNotAuthorized)
	}
	if !configSwitched {
		return fmt.Errorf("%w: the active config has not been switched to the target yet; deleting first would point the live config at deleted data", ErrDeleteNotAuthorized)
	}
	if verifyMode != VerifyModeFull {
		return fmt.Errorf("%w: the request declared verify mode %q; a sampled verification does not check every object and can never authorize a delete", ErrDeleteNotAuthorized, verifyMode)
	}
	if report == nil {
		return fmt.Errorf("%w: no verification report", ErrDeleteNotAuthorized)
	}
	// The ONE sanctioned way to ask "does this report authorize a delete" -
	// see AuthorizesSourceDelete's own doc comment. Do NOT re-derive
	// "report.OK && report.Mode == VerifyModeFull" here: that would duplicate
	// the safety-critical rule at a second call site where it could drift out
	// of sync with the original.
	if !report.AuthorizesSourceDelete() {
		return fmt.Errorf("%w: the verification report does not authorize a delete (mode=%q, ok=%v, problems=%d)",
			ErrDeleteNotAuthorized, report.Mode, report.OK, report.ProblemsTotal)
	}
	// Independent of the predicate above: an empty manifest can never
	// authorize a delete, even from a passing full report. See the doc
	// comment's point 5.
	if report.ObjectsInManifest <= 0 {
		return fmt.Errorf("%w: the manifest captured zero objects; an empty manifest cannot prove the source was emptied and can never authorize a delete on its own", ErrDeleteNotAuthorized)
	}
	return nil
}

// DeleteOptions carries the orchestration hooks.
//
// There is deliberately NO Cancelled hook. Once deleting_source has begun it
// is already the point of no return: verification has passed, the target holds
// a verified copy, and a half-cancelled delete leaves the operator with two
// partial data sets instead of one complete one. The panel hides Cancel for
// this phase for the same reason. ctx is still honoured at the object
// boundary (e.g. process shutdown), but there is no voluntary early-abort
// callback offered to a caller the way CopyOptions/VerifyOptions provide one.
type DeleteOptions struct {
	Progress CopyProgress
	Log      func(line string)
}

// DeleteResult summarizes one DeleteSource run.
type DeleteResult struct {
	ObjectsDeleted int64
	BytesFreed     int64
}

// DeleteSource removes exactly the objects named in the manifest, and nothing
// else. Objects written to the source AFTER the manifest was captured are not
// in it and are therefore left alone - a deliberate consequence of migrating
// a live data set (see the "extra" classification at verification).
//
// This is the ONLY function in the engine that writes to the source, and it
// must be called only after AuthorizeSourceDelete has returned nil - this
// function itself does not re-check any of those preconditions, so every
// caller MUST go through the gate first.
//
// Per DataSet.Delete's contract, a genuinely missing key is not an error
// (idempotent), so ANY error returned here is a real failure - throttle, 503,
// permission, timeout - and is surfaced immediately, never silently treated
// as "already gone".
func DeleteSource(ctx context.Context, src DataSet, entries []models.StorageManifestEntry, opts DeleteOptions) (DeleteResult, error) {
	logf := func(format string, args ...interface{}) {
		if opts.Log != nil {
			opts.Log(fmt.Sprintf(format, args...))
		}
	}
	sorted := append([]models.StorageManifestEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })

	res := DeleteResult{}
	total := int64(len(sorted))
	var bytesTotal int64
	for _, e := range sorted {
		bytesTotal += e.Size
	}
	logf("Deleting %d verified object(s) from the source. This phase is not cancellable.", total)

	for _, e := range sorted {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if err := src.Delete(ctx, e.Key); err != nil {
			return res, fmt.Errorf("delete source %s: %w", e.Key, err)
		}
		res.ObjectsDeleted++
		res.BytesFreed += e.Size
		if opts.Progress != nil {
			opts.Progress(res.ObjectsDeleted, 0, total, res.BytesFreed, bytesTotal, e.Key)
		}
	}
	// Deliberately NOT "source cleared": this deletes exactly the keys the
	// manifest names. Anything written after the capture is still there, and
	// for modpacks the manifest only covers DB-referenced objects, so bucket
	// orphans are out of reach by construction. An operator reads this line
	// while deciding whether to tear the old bucket down, so it must not
	// claim an emptiness this function cannot observe.
	logf("Removed %d object(s) named in the manifest, %d bytes freed. Anything written to the source after the manifest was captured is untouched.", res.ObjectsDeleted, res.BytesFreed)
	return res, nil
}
