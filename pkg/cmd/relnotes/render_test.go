package main

import (
	"strings"
	"testing"

	"dylaris-pkg/release"
)

func entries(n int, text string) []release.Entry {
	out := make([]release.Entry, n)
	for i := range out {
		out[i] = release.Entry{Text: text}
	}
	return out
}

// The failure this cap exists for: a section long enough to hit Discord's field
// limit used to end in a bare "- ...", which says nothing about how much is
// missing and reads like the message itself is broken.
func TestRenderEntriesCapsAtThree(t *testing.T) {
	got := renderEntries(entries(6, "short line"))
	if n := strings.Count(got, "\n- ") + 1; n != 4 {
		t.Errorf("rendered %d lines, want 3 entries + 1 pointer:\n%s", n, got)
	}
	if !strings.Contains(got, "...and 3 more, in the full notes") {
		t.Errorf("the remainder is not named:\n%s", got)
	}
}

func TestRenderEntriesKeepsShortSectionsWhole(t *testing.T) {
	got := renderEntries(entries(3, "short line"))
	if strings.Contains(got, "more, in the full notes") {
		t.Errorf("three entries fit exactly and must not be advertised as trimmed:\n%s", got)
	}
	if n := strings.Count(got, "- "); n != 3 {
		t.Errorf("got %d entries, want 3:\n%s", n, got)
	}
}

// Discord rejects the whole payload over the field limit, so the guard has to
// hold even when three entries are individually enormous.
func TestRenderEntriesStaysUnderTheFieldLimit(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 10} {
		got := renderEntries(entries(n, strings.Repeat("x", 900)))
		if len(got) > maxFieldValue {
			t.Errorf("%d long entries rendered %d bytes, over the %d limit", n, len(got), maxFieldValue)
		}
	}
}

// A single entry too long to fit is cut rather than dropped: a category showing
// only a pointer would say less than a cut sentence does.
func TestRenderEntriesCutsRatherThanDrops(t *testing.T) {
	got := renderEntries([]release.Entry{{Text: strings.Repeat("y", 2000)}})
	if !strings.HasPrefix(got, "- yyy") {
		t.Errorf("the entry was dropped instead of cut:\n%.80s", got)
	}
	if !strings.Contains(got, "...") {
		t.Error("a cut entry must show that it was cut")
	}
}

func TestRenderEntriesEmpty(t *testing.T) {
	if got := renderEntries(nil); got != "Nothing this time." {
		t.Errorf("empty section rendered %q", got)
	}
}
