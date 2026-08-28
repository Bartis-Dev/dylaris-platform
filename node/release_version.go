package main

import "dylaris-pkg/release"

// releaseVersion is the release this node image was built from, injected by the
// build as -ldflags "-X main.releaseVersion=<version>".
//
// It is a string because that is the only shape -X can set. Empty on a local
// `go build` and on any image built before the stamp existed, and both must read
// as UNKNOWN rather than as very old: an image that predates the stamp would
// otherwise look like it predates every release ever published, and Core would
// flag - or after a deadline, refuse - a fleet nobody has touched.
var releaseVersion string

// nodeReleaseVersion parses the stamp, returning the zero Version for "not
// stamped" or unparseable. Callers omit the field entirely then, so Core can
// tell "this node does not report one" apart from a real answer.
func nodeReleaseVersion() release.Version {
	v, err := release.ParseVersion(releaseVersion)
	if err != nil {
		return release.Version{}
	}
	return v
}
