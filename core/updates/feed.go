// Package updates holds the in-panel update-feed baseline. The platform feed is
// an append-only JSONL changelog (one entry per main-branch change) baked into
// the binary at build time via go:embed. Because it is append-only, the line
// count at build time is the "installed" baseline: the since-install delta is
// simply the tail of the newer remote feed beyond this count. The runtime fetch
// + diff lives in core/handlers/updates.go.
//
// The feed is intentionally EMPTY until go-live: the machinery ships now, the
// content and the public cross-push are wired up later (owner infra). An empty
// feed yields "no updates", never an error.
package updates

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed feed.jsonl
var platformFeed []byte

// FeedEntry is one changelog line in a JSONL update feed.
//
// Type is not validated here on purpose - an unknown value renders under its own
// name rather than being dropped, so a feed written by a newer build is never
// silently swallowed by an older panel. The known set:
//
//	feature | fix | change | security | breaking
//
// "breaking" is the only one that changes what the operator must DO: it means
// the update does not finish applying itself, and the panel leads the What's-new
// modal with a callout when a date contains one. Use it only for that.
type FeedEntry struct {
	Date    string `json:"date"`
	Service string `json:"service"`
	Type    string `json:"type"`
	Summary string `json:"summary"`
}

// PlatformFeed returns the raw bytes of the platform update feed baked into this
// build. Empty until the feed is populated (append-only, one line per change).
func PlatformFeed() []byte { return platformFeed }

// NonEmptyLines splits a JSONL feed into its non-empty, trimmed lines. Blank
// lines are ignored so a stray trailing newline never inflates the count.
func NonEmptyLines(data []byte) []string {
	raw := strings.Split(string(data), "\n")
	out := make([]string, 0, len(raw))
	for _, ln := range raw {
		if t := strings.TrimSpace(ln); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// LineCount returns the number of non-empty lines in a JSONL feed. Because the
// feed is append-only this is a stable, monotonically-growing baseline.
func LineCount(data []byte) int { return len(NonEmptyLines(data)) }

// ParseEntries parses JSONL lines into entries, skipping any malformed line so a
// single bad row never blanks the whole feed.
func ParseEntries(lines []string) []FeedEntry {
	out := make([]FeedEntry, 0, len(lines))
	for _, ln := range lines {
		var e FeedEntry
		if json.Unmarshal([]byte(ln), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// Delta returns the feed lines published after installedCount. It is clamped so
// an installedCount past the end (an older feed than the running build, or a bad
// baseline) yields no entries rather than panicking.
func Delta(remote []string, installedCount int) []string {
	if installedCount < 0 {
		installedCount = 0
	}
	if installedCount >= len(remote) {
		return nil
	}
	return remote[installedCount:]
}
