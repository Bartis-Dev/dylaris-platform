package storagereach

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path"
	"time"

	"dylaris-core/storage"
)

// FleetDir holds one stable beacon per Core: the live register of who is
// actually sharing this storage.
//
// It is deliberately NOT round-scoped. A config round can scope its files to a
// round id because a coordinator runs every participant at once; a self-check
// cannot, because each Core ticks on its own 120s schedule. With round-scoped
// files, no Core would ever see another Core's self-check artefacts and every
// Core past the first would permanently report not-shared.
const FleetDir = ".dylaris-fleet"

// FleetBeaconPath is where one Core's beacon lives.
func FleetBeaconPath(coreID string) string { return path.Join(FleetDir, coreID) }

// BeaconOptions configures one refresh pass.
type BeaconOptions struct {
	CoreID      string
	Fingerprint string
	// Participants is every Core expected to be sharing right now, including
	// CoreID itself.
	Participants []string
	// MaxAge is how old a peer's beacon may be and still count. A Core that
	// died leaves its beacon behind; counting it would let a dead instance
	// vouch for a share nobody is using.
	MaxAge time.Duration
	Now    func() time.Time
}

// RefreshBeacon writes this Core's beacon and reads back its peers'. It
// returns the same Report shape Probe does, so Aggregate handles both without
// knowing which produced it.
func RefreshBeacon(ctx context.Context, prov storage.StorageProvider, opts BeaconOptions) Report {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	if opts.MaxAge <= 0 {
		opts.MaxAge = 300 * time.Second
	}
	at := now()

	rep := Report{
		CoreID:      opts.CoreID,
		Fingerprint: opts.Fingerprint,
		At:          at.Unix(),
		SeenPeers:   []string{}, MismatchedPeers: []string{},
		CrossWroteTo: []string{}, CrossWriteDenied: []string{},
	}

	hostname, _ := os.Hostname()
	// Token is the fingerprint rather than a round id: a beacon is not scoped
	// to a round, and the fingerprint is what a reader must agree with.
	body, err := json.Marshal(BeaconPayload{
		CoreID: opts.CoreID, Hostname: hostname,
		Token: opts.Fingerprint, Fingerprint: opts.Fingerprint, TS: at.Unix(),
	})
	if err != nil {
		rep.WriteErr = err.Error()
		return rep
	}

	// One file per Core, overwritten every pass - never one per tick.
	if err := prov.WriteFile(ctx, FleetBeaconPath(opts.CoreID), bytes.NewReader(body)); err != nil {
		rep.WriteErr = err.Error()
	} else {
		rep.Wrote = true
		rep.Reachable = true
		_ = prov.CreateDir(ctx, path.Join(FleetDir, opts.CoreID+ackSuffix))
	}

	names, listErr := listNames(ctx, prov, FleetDir)
	if listErr != nil {
		return finishReport(rep)
	}
	rep.Reachable = true

	expected := make(map[string]bool, len(opts.Participants))
	for _, p := range opts.Participants {
		if p != opts.CoreID {
			expected[p] = true
		}
	}

	cutoff := at.Add(-opts.MaxAge).Unix()
	for _, name := range names {
		if !expected[name] {
			// A beacon from a Core that is no longer online is not evidence
			// about the current fleet, and not a fault either - it just ages
			// out.
			continue
		}
		b, ok := readFleetBeacon(ctx, prov, FleetBeaconPath(name))
		if !ok || b.TS < cutoff {
			continue
		}
		if b.Fingerprint != opts.Fingerprint {
			rep.MismatchedPeers = appendOnce(rep.MismatchedPeers, name)
			continue
		}
		rep.SeenPeers = appendOnce(rep.SeenPeers, name)
		crossWrite(ctx, prov, FleetDir, name, opts.CoreID, body, &rep)
	}
	return finishReport(rep)
}

// readFleetBeacon reads and decodes a beacon. Unlike the round probe there is
// no token to match: freshness and fingerprint are the only checks, and the
// caller applies both.
func readFleetBeacon(ctx context.Context, prov storage.StorageProvider, p string) (BeaconPayload, bool) {
	rc, err := prov.GetFile(ctx, p)
	if err != nil {
		return BeaconPayload{}, false
	}
	defer rc.Close()
	data, err := readLimited(rc)
	if err != nil {
		return BeaconPayload{}, false
	}
	var b BeaconPayload
	if err := json.Unmarshal(data, &b); err != nil {
		return BeaconPayload{}, false
	}
	return b, true
}
