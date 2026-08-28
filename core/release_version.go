package main

import "dylaris-pkg/release"

// releaseVersion is the release this Core image was built from, injected by the
// build as -ldflags "-X main.releaseVersion=<version>".
//
// Core embeds the release-notes files, so it is tempting to read the version off
// the top of platform.md instead. That is WRONG, and subtly: the repo has one
// version and more than one audience, so a release that only concerns customers
// adds a block to hosted.md and none to platform.md. Core would then report the
// older platform block as its own version while running an image built from the
// newer release, and report itself behind for a release it already contains.
//
// The stamp says what the BUILD was; the embedded file says what the notes SAID.
// They are different questions.
var releaseVersion string

// coreReleaseVersion parses the stamp, returning the zero Version when this
// build carries none (a local `go build`). Unknown, never "very old".
func coreReleaseVersion() release.Version {
	v, err := release.ParseVersion(releaseVersion)
	if err != nil {
		return release.Version{}
	}
	return v
}
