// Package storagereach proves that every online Core can write to and read
// from the SAME shared Core file storage.
//
// The failure it exists to prevent is silent: with more than one Core and a
// per-host volume that only LOOKS shared, blobs split across hosts - each Core
// serves only what it wrote, and nothing errors, because every Core reads its
// own writes back perfectly. Counting Cores (the guard this replaces) cannot
// tell a real shared mount from a fake one; only a cross-Core write/read can.
package storagereach

// Status is the per-Core verdict. The values are a wire contract: they are
// stored in Redis fault records and rendered by the panel, so they must not be
// renamed without changing both.
type Status string

const (
	// StatusOK - wrote its own file, read every peer's file, and cross-wrote
	// into every peer's namespace.
	StatusOK Status = "ok"
	// StatusOffline - no live heartbeat, so it was never a participant.
	StatusOffline Status = "offline"
	// StatusNoResponse - heartbeating, but did not report within the round
	// window.
	StatusNoResponse Status = "no-response"
	// StatusUnreachable - up, but cannot reach the storage at all (mount
	// absent, S3 endpoint timeout, provider would not construct).
	StatusUnreachable Status = "unreachable"
	// StatusWriteDenied - reached the backend but cannot write its own file
	// (read-only mount, missing permission).
	StatusWriteDenied Status = "write-denied"
	// StatusNotShared - wrote its own file but cannot see peers' files. The
	// fake-shared local volume. MissingPeers names who is invisible.
	StatusNotShared Status = "not-shared"
	// StatusFingerprintMismatch - reached a backend, but not the one this
	// round is about (stale config, wrong bucket/prefix/path).
	StatusFingerprintMismatch Status = "fingerprint-mismatch"
	// StatusCrossWriteDenied - reads and writes its own namespace but cannot
	// write into a peer's (NFS uid-squash).
	StatusCrossWriteDenied Status = "cross-write-denied"
)

// Config is the resolved backend a round is about. It is the subset of the
// core-storage settings the probe needs, decoupled from the handlers package
// so this one stays a leaf.
type Config struct {
	Backend     string `json:"backend"` // "path" | "local" | "s3"
	Path        string `json:"path,omitempty"`
	S3Endpoint  string `json:"s3Endpoint,omitempty"`
	S3Bucket    string `json:"s3Bucket,omitempty"`
	S3Region    string `json:"s3Region,omitempty"`
	S3AccessKey string `json:"s3AccessKey,omitempty"`
	S3SecretKey string `json:"s3SecretKey,omitempty"`
	S3PathStyle bool   `json:"s3PathStyle,omitempty"`
	S3Prefix    string `json:"s3Prefix,omitempty"`
}

// Report is what one participant writes back about itself. Every field is
// self-observed: a participant reports what IT could do, never a verdict about
// a peer. Turning reports into verdicts is Aggregate's job.
type Report struct {
	CoreID      string `json:"coreId"`
	Fingerprint string `json:"fingerprint"`
	// Reachable is false when the provider would not even construct or the
	// first write failed with a connectivity-shaped error.
	Reachable bool `json:"reachable"`
	// Wrote is true once this Core's own probe file is on the backend.
	Wrote    bool   `json:"wrote"`
	WriteErr string `json:"writeErr,omitempty"`
	// SeenPeers are the participant ids whose probe file this Core read back
	// with a matching round token. Never includes itself.
	SeenPeers []string `json:"seenPeers"`
	// MismatchedPeers are peers whose beacon was readable but carried a
	// different backend fingerprint. Distinct from simply not being seen: the
	// peer is right there, it is just pointed somewhere else, and that needs a
	// different fix.
	MismatchedPeers []string `json:"mismatchedPeers"`
	// CrossWroteTo are peers whose namespace this Core successfully wrote a
	// marker into; CrossWriteDenied are those it could see but not write to.
	CrossWroteTo     []string `json:"crossWroteTo"`
	CrossWriteDenied []string `json:"crossWriteDenied"`
	At               int64    `json:"at"` // unix seconds, for staleness
}

// CoreResult is one Core's verdict after aggregation.
type CoreResult struct {
	CoreID string `json:"coreId"`
	Status Status `json:"status"`
	// MissingPeers is populated for StatusNotShared: the peers this Core could
	// not see. Naming them is what turns "something is wrong" into a fixable
	// report.
	MissingPeers []string `json:"missingPeers,omitempty"`
	// DeniedPeers is populated for StatusCrossWriteDenied.
	DeniedPeers []string `json:"deniedPeers,omitempty"`
	// Detail carries the backend's own error text when there is one.
	Detail string `json:"detail,omitempty"`
}

// RoundResult is the aggregate a coordinator returns and the panel renders.
type RoundResult struct {
	RoundID string `json:"roundId"`
	// Fingerprint is the backend identity every participant had to agree on.
	Fingerprint string `json:"fingerprint"`
	// OK is true only when every participant reported StatusOK. It is the
	// single value the save path gates on.
	OK        bool         `json:"ok"`
	Confirmed int          `json:"confirmed"` // the X in "X/N"
	Total     int          `json:"total"`     // the N
	Results   []CoreResult `json:"results"`
	// Done is false while the round is still collecting reports, so the panel
	// can tell "3/10 so far" from "3/10 and finished".
	Done bool `json:"done"`
}

// BeaconPayload is the content of a participant's probe file. Peers read it
// back to confirm both that they can see the file AND that the writer was
// pointed at the same backend.
type BeaconPayload struct {
	CoreID      string `json:"coreId"`
	Hostname    string `json:"hostname"`
	Token       string `json:"token"`
	Fingerprint string `json:"fingerprint"`
	TS          int64  `json:"ts"`
}
