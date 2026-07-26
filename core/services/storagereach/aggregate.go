package storagereach

import "sort"

// Aggregate turns per-Core self-reports into one verdict per Core plus the
// round result. Pure: no clock, no I/O, no Redis - so every taxonomy case is
// testable as a table.
//
// done separates "still collecting" from "finished": an unreported Core is
// pending mid-round and no-response once the window closed. OK is false in
// both cases, so a caller can never commit on a partial round.
func Aggregate(participants []string, reports map[string]Report, fingerprint string, done bool) RoundResult {
	res := RoundResult{
		RoundID:     "",
		Fingerprint: fingerprint,
		Total:       len(participants),
		Done:        done,
		Results:     make([]CoreResult, 0, len(participants)),
	}

	isParticipant := make(map[string]bool, len(participants))
	for _, id := range participants {
		isParticipant[id] = true
	}

	// A Core can be convicted by a PEER's report: if someone read its beacon
	// and found a different backend, that is authoritative about it regardless
	// of what it says about itself (it may be perfectly happy on the wrong
	// storage). Collected first so the per-Core pass can consult it.
	mismatchedByPeer := make(map[string]bool)
	for reporter, rep := range reports {
		if !isParticipant[reporter] {
			// A non-participant's opinion must not decide a participant's
			// verdict: Aggregate is clockless and cannot tell "this Core left
			// the round" from "this Core is still valid", so a stale report
			// from a Core outside this round (e.g. a persistent fleet beacon
			// left by a Core that has since gone offline) must not convict
			// anyone.
			continue
		}
		if rep.Fingerprint != fingerprint {
			// A reporter that is itself on the wrong backend cannot be
			// trusted to judge anyone else's.
			continue
		}
		for _, peer := range rep.MismatchedPeers {
			mismatchedByPeer[peer] = true
		}
	}

	confirmed := 0
	for _, id := range participants {
		cr := CoreResult{CoreID: id}
		rep, reported := reports[id]

		switch {
		case !reported:
			if mismatchedByPeer[id] {
				// A peer proved it is on the wrong backend; that is a better
				// answer than "did not answer".
				cr.Status = StatusFingerprintMismatch
			} else {
				cr.Status = StatusNoResponse
			}
		case rep.Fingerprint != fingerprint || mismatchedByPeer[id]:
			cr.Status = StatusFingerprintMismatch
		case !rep.Reachable:
			cr.Status = StatusUnreachable
			cr.Detail = rep.WriteErr
		case !rep.Wrote:
			// Reachable but could not write: a read-only mount or a
			// permission problem, which is a different fix from unreachable.
			cr.Status = StatusWriteDenied
			cr.Detail = rep.WriteErr
		default:
			// A peer this report names in MismatchedPeers was SEEN - its beacon
			// was readable, just carrying the wrong fingerprint - which is not
			// the same as never appearing at all. That peer already gets its
			// own fingerprint-mismatch verdict above (via mismatchedByPeer), so
			// folding it into "seen" here stops the reporter that correctly
			// identified it from also being wrongly convicted of not-shared
			// over the very peer it named.
			seen := append(append([]string(nil), rep.SeenPeers...), rep.MismatchedPeers...)
			missing := missingFrom(participants, id, seen)
			if len(missing) > 0 {
				cr.Status = StatusNotShared
				cr.MissingPeers = missing
				break
			}
			if len(rep.CrossWriteDenied) > 0 {
				cr.Status = StatusCrossWriteDenied
				cr.DeniedPeers = append([]string(nil), rep.CrossWriteDenied...)
				break
			}
			cr.Status = StatusOK
			confirmed++
		}

		res.Results = append(res.Results, cr)
	}

	sort.Slice(res.Results, func(i, j int) bool { return res.Results[i].CoreID < res.Results[j].CoreID })
	res.Confirmed = confirmed
	// Fail-closed: only a finished round in which every participant is ok
	// counts as proof.
	res.OK = done && confirmed == len(participants) && len(participants) > 0
	return res
}

// missingFrom returns the participants (excluding self) that seen does not
// cover, sorted, so the panel can name them.
func missingFrom(participants []string, self string, seen []string) []string {
	have := make(map[string]bool, len(seen))
	for _, s := range seen {
		have[s] = true
	}
	var missing []string
	for _, p := range participants {
		if p == self || have[p] {
			continue
		}
		missing = append(missing, p)
	}
	sort.Strings(missing)
	return missing
}
