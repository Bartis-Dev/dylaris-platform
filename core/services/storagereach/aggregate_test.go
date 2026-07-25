package storagereach

import "testing"

// okReport builds a report in which coreID saw and cross-wrote to everyone
// else in participants. Tests then break exactly one thing.
func okReport(coreID string, participants []string) Report {
	rep := Report{
		CoreID: coreID, Fingerprint: "fp-1", Reachable: true, Wrote: true,
		SeenPeers: []string{}, MismatchedPeers: []string{},
		CrossWroteTo: []string{}, CrossWriteDenied: []string{},
	}
	for _, p := range participants {
		if p == coreID {
			continue
		}
		rep.SeenPeers = append(rep.SeenPeers, p)
		rep.CrossWroteTo = append(rep.CrossWroteTo, p)
	}
	return rep
}

func statusOf(t *testing.T, res RoundResult, coreID string) CoreResult {
	t.Helper()
	for _, r := range res.Results {
		if r.CoreID == coreID {
			return r
		}
	}
	t.Fatalf("no result for %q in %+v", coreID, res.Results)
	return CoreResult{}
}

func TestAggregate_AllHealthy(t *testing.T) {
	parts := []string{"core-a", "core-b", "core-c"}
	reports := map[string]Report{
		"core-a": okReport("core-a", parts),
		"core-b": okReport("core-b", parts),
		"core-c": okReport("core-c", parts),
	}

	res := Aggregate(parts, reports, "fp-1", true)

	if !res.OK {
		t.Fatalf("OK = false, want true: %+v", res.Results)
	}
	if res.Confirmed != 3 || res.Total != 3 {
		t.Errorf("counter = %d/%d, want 3/3", res.Confirmed, res.Total)
	}
	for _, r := range res.Results {
		if r.Status != StatusOK {
			t.Errorf("%s = %s, want ok", r.CoreID, r.Status)
		}
	}
}

func TestAggregate_SingleParticipantIsOK(t *testing.T) {
	parts := []string{"core-a"}
	res := Aggregate(parts, map[string]Report{"core-a": okReport("core-a", parts)}, "fp-1", true)
	if !res.OK || res.Confirmed != 1 {
		t.Fatalf("res = %+v, want a passing 1/1 round", res)
	}
}

func TestAggregate_ClassifiesEveryFailure(t *testing.T) {
	parts := []string{"core-a", "core-b"}

	tests := []struct {
		name     string
		mutate   func(r *Report)
		want     Status
		wantList func(t *testing.T, cr CoreResult)
	}{
		{
			name: "unreachable",
			mutate: func(r *Report) {
				r.Reachable = false
				r.Wrote = false
				r.WriteErr = "connection refused"
				r.SeenPeers = nil
				r.CrossWroteTo = nil
			},
			want: StatusUnreachable,
			wantList: func(t *testing.T, cr CoreResult) {
				if cr.Detail != "connection refused" {
					t.Errorf("Detail = %q, want the backend error text", cr.Detail)
				}
			},
		},
		{
			// Reachable but the write failed: a read-only mount. Distinct
			// from unreachable because the fix is different.
			name: "write-denied",
			mutate: func(r *Report) {
				r.Wrote = false
				r.WriteErr = "read-only file system"
				r.SeenPeers = nil
				r.CrossWroteTo = nil
			},
			want: StatusWriteDenied,
			wantList: func(t *testing.T, cr CoreResult) {
				if cr.Detail != "read-only file system" {
					t.Errorf("Detail = %q, want the backend error text", cr.Detail)
				}
			},
		},
		{
			name:   "not-shared names the invisible peer",
			mutate: func(r *Report) { r.SeenPeers = []string{}; r.CrossWroteTo = []string{} },
			want:   StatusNotShared,
			wantList: func(t *testing.T, cr CoreResult) {
				if len(cr.MissingPeers) != 1 || cr.MissingPeers[0] != "core-b" {
					t.Errorf("MissingPeers = %v, want [core-b]", cr.MissingPeers)
				}
			},
		},
		{
			name:   "cross-write-denied names the peer",
			mutate: func(r *Report) { r.CrossWroteTo = []string{}; r.CrossWriteDenied = []string{"core-b"} },
			want:   StatusCrossWriteDenied,
			wantList: func(t *testing.T, cr CoreResult) {
				if len(cr.DeniedPeers) != 1 || cr.DeniedPeers[0] != "core-b" {
					t.Errorf("DeniedPeers = %v, want [core-b]", cr.DeniedPeers)
				}
			},
		},
		{
			// The reporting Core is itself on the wrong backend.
			name:     "own fingerprint mismatch",
			mutate:   func(r *Report) { r.Fingerprint = "fp-STALE" },
			want:     StatusFingerprintMismatch,
			wantList: func(t *testing.T, cr CoreResult) {},
		},
		{
			// The reporting Core is right, but it saw a peer that is not.
			// The MISMATCHED peer is the one at fault, not the reporter.
			name: "a peer on the wrong backend",
			mutate: func(r *Report) {
				r.SeenPeers = []string{}
				r.CrossWroteTo = []string{}
				r.MismatchedPeers = []string{"core-b"}
			},
			want:     StatusNotShared,
			wantList: func(t *testing.T, cr CoreResult) {},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bad := okReport("core-a", parts)
			tc.mutate(&bad)
			reports := map[string]Report{"core-a": bad, "core-b": okReport("core-b", parts)}

			res := Aggregate(parts, reports, "fp-1", true)

			if res.OK {
				t.Fatal("OK = true although one Core failed")
			}
			cr := statusOf(t, res, "core-a")
			if cr.Status != tc.want {
				t.Fatalf("core-a status = %s, want %s", cr.Status, tc.want)
			}
			tc.wantList(t, cr)
		})
	}
}

func TestAggregate_PeerMismatchIsBlamedOnThePeer(t *testing.T) {
	// core-a is correct and reports that core-b's beacon carries the wrong
	// fingerprint. core-b's own (absent) report must not let it pass: the
	// operator has to be told WHICH Core is misconfigured.
	parts := []string{"core-a", "core-b"}
	a := okReport("core-a", parts)
	a.SeenPeers = []string{}
	a.CrossWroteTo = []string{}
	a.MismatchedPeers = []string{"core-b"}

	res := Aggregate(parts, map[string]Report{"core-a": a}, "fp-1", true)

	if got := statusOf(t, res, "core-b").Status; got != StatusFingerprintMismatch {
		t.Fatalf("core-b status = %s, want fingerprint-mismatch (reported by a peer)", got)
	}
}

func TestAggregate_MissingReportIsNoResponseAndFailsClosed(t *testing.T) {
	parts := []string{"core-a", "core-b"}
	res := Aggregate(parts, map[string]Report{"core-a": okReport("core-a", parts)}, "fp-1", true)

	if res.OK {
		t.Fatal("OK = true with a participant that never reported")
	}
	if got := statusOf(t, res, "core-b").Status; got != StatusNoResponse {
		t.Fatalf("core-b status = %s, want no-response", got)
	}
	if res.Confirmed != 1 || res.Total != 2 {
		t.Errorf("counter = %d/%d, want 1/2", res.Confirmed, res.Total)
	}
}

func TestAggregate_InFlightRoundIsNotDoneAndNotOK(t *testing.T) {
	// While a round is still collecting, a not-yet-reported Core is pending,
	// not failed - but the round must never read as OK.
	parts := []string{"core-a", "core-b"}
	res := Aggregate(parts, map[string]Report{"core-a": okReport("core-a", parts)}, "fp-1", false)

	if res.Done {
		t.Error("Done = true for an in-flight round")
	}
	if res.OK {
		t.Error("OK = true for an in-flight round")
	}
	if res.Confirmed != 1 || res.Total != 2 {
		t.Errorf("counter = %d/%d, want 1/2", res.Confirmed, res.Total)
	}
}

func TestAggregate_ResultsAreSortedByCoreID(t *testing.T) {
	parts := []string{"core-c", "core-a", "core-b"}
	reports := map[string]Report{
		"core-a": okReport("core-a", parts),
		"core-b": okReport("core-b", parts),
		"core-c": okReport("core-c", parts),
	}
	res := Aggregate(parts, reports, "fp-1", true)
	for i, want := range []string{"core-a", "core-b", "core-c"} {
		if res.Results[i].CoreID != want {
			t.Fatalf("Results[%d] = %s, want %s (map iteration must not leak into the UI order)", i, res.Results[i].CoreID, want)
		}
	}
}
