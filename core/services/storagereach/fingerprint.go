package storagereach

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Fingerprint is a stable, non-secret identity for a resolved backend. Two
// Cores that agree on it are pointed at the same place; two that disagree are
// not, which is a different (and more useful) failure than "cannot see you".
//
// Credentials are deliberately excluded: they authorise access, they do not
// identify the backend, and two Cores legitimately hold different keys for one
// bucket. Excluding them also keeps the fingerprint safe to show in the panel.
func Fingerprint(cfg Config) string {
	var b strings.Builder
	if isPathBackend(cfg.Backend) {
		b.WriteString("path\x00")
		b.WriteString(strings.TrimRight(strings.TrimSpace(cfg.Path), "/"))
	} else {
		b.WriteString("s3\x00")
		b.WriteString(strings.TrimSpace(cfg.S3Endpoint))
		b.WriteString("\x00")
		b.WriteString(strings.TrimSpace(cfg.S3Bucket))
		b.WriteString("\x00")
		b.WriteString(strings.Trim(strings.TrimSpace(cfg.S3Prefix), "/"))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// isPathBackend is close to handlers.isHostPathBackend ("local" is the legacy
// alias for "path", and a Core configured with either must fingerprint the
// same as its peers), but differs on the empty string: here it counts as a
// path backend too, since an unset Backend still needs a stable fingerprint.
func isPathBackend(backend string) bool {
	return backend == "path" || backend == "local" || backend == ""
}
