package main

import "strconv"

// feedBaseline is the update-feed line count this node image was built at,
// injected by the build as -ldflags "-X main.feedBaseline=<n>".
//
// It is a string because that is the only shape -X can set. Empty on a local
// `go build` and on any image built before the stamp existed, and both must read
// as UNKNOWN rather than as zero: zero would claim the node predates the entire
// feed and make every entry ever written look pending.
var feedBaseline string

// feedBaselineValue parses the stamp, returning 0 for "not stamped". Callers
// omit the field entirely on 0 so Core can tell "this node does not report one"
// apart from a real answer.
func feedBaselineValue() int {
	n, err := strconv.Atoi(feedBaseline)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
