// Package storageplacement holds the one rule that decides which storage path a
// new server lands on.
//
// It lives in the shared module on purpose. Core has to predict the node's
// choice when it checks whether a node has room, and the node has to make that
// choice. Two copies of the rule would drift, and the failure mode is quiet:
// Core admits a server because the biggest disk has space while the node puts it
// on a smaller one. The panel renders the same order from its own copy, which is
// covered by its own test for the same reason.
package storageplacement

// Modes for placing NEW servers. Existing servers never move; their path is
// pinned when they are created.
const (
	ModeAuto   = "auto"   // most free space wins
	ModeManual = "manual" // the operator's order wins
)

// NormalizeMode maps anything unrecognised onto ModeAuto, so a typo in stored
// configuration cannot silently change where servers land.
func NormalizeMode(mode string) string {
	if mode == ModeManual {
		return ModeManual
	}
	return ModeAuto
}

// OrderPaths returns available in the operator's preferred order.
//
// Entries of order that available does not contain are dropped: a path the node
// no longer has must not be offered. Entries of available that order does not
// mention come LAST, keeping their original relative order - that is what lets a
// disk added later join at the bottom without anyone editing the configuration.
// Duplicates in order are collapsed, and the result always contains every
// available path exactly once.
func OrderPaths(available, order []string) []string {
	seen := make(map[string]bool, len(available))
	have := make(map[string]bool, len(available))
	for _, p := range available {
		have[p] = true
	}

	out := make([]string, 0, len(available))
	for _, want := range order {
		if have[want] && !seen[want] {
			seen[want] = true
			out = append(out, want)
		}
	}
	for _, p := range available {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
