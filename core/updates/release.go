// Package updates holds the release notes this build shipped with.
//
// Two audiences, two files. `platform.md` is for people who RUN the platform
// (self-hosters, operators); `hosted.md` is for DYLARIS customers, who run a
// node or nothing at all. They are separate because "restart Core after
// changing the mail settings" and "your node needs updating by Friday" have no
// reader in common.
//
// The embedded copy is BOTH the offline fallback and this build's own version:
// the top release in platform.md is, by construction, the release Core was
// built from. Core therefore needs no version stamp of its own, unlike the node
// (ldflags) and the panel (build-time env), neither of which carries the file.
package updates

import (
	_ "embed"
	"sync"

	"dylaris-pkg/release"
)

//go:embed platform.md
var platformNotes []byte

//go:embed hosted.md
var hostedNotes []byte

// Parsing happens once. The files are validated in CI, so a parse failure here
// is a build that should never have been produced - it is surfaced as an empty
// feed (the fail-open the whole update path uses) rather than as a panic that
// takes Core down over a changelog.
var (
	once     sync.Once
	platform []release.Release
	hosted   []release.Release
	parseErr error
)

func parse() {
	once.Do(func() {
		var err error
		if platform, err = release.Parse(platformNotes); err != nil {
			parseErr = err
			platform = nil
		}
		if hosted, err = release.Parse(hostedNotes); err != nil {
			parseErr = err
			hosted = nil
		}
	})
}

// Platform returns the embedded platform release notes, newest first.
func Platform() []release.Release {
	parse()
	return platform
}

// Hosted returns the embedded customer-facing release notes, newest first.
func Hosted() []release.Release {
	parse()
	return hosted
}

// ParseError reports a malformed embedded file, so the handler can log it once
// instead of silently serving nothing.
func ParseError() error {
	parse()
	return parseErr
}

// Version is the release THIS build was made from: the newest block in the
// embedded platform notes. Zero when the file has no releases yet, which reads
// as "unknown" everywhere downstream and never as "very old".
func Version() release.Version {
	parse()
	if r, ok := release.Latest(platform); ok {
		return r.Version
	}
	return release.Version{}
}
