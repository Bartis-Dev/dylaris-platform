package services

import (
	"regexp"
	"strings"
	"testing"
)

// slugPattern pins the documented "crimson-otter-7a3f" shape: two lowercase
// words joined by dashes, plus a 4-char lowercase hex suffix.
var slugPattern = regexp.MustCompile(`^[a-z]+-[a-z]+-[0-9a-f]{4}$`)

// TestGenerateContainerSlugFormat generates many slugs (rather than asserting
// a fixed value, since GenerateContainerSlug is crypto/rand-backed) and pins
// the format, vocabulary, and suffix charset/length. It also does a soft
// uniqueness check: the adjective x noun x hex combinatoric space (~97 x 98 x
// 65536) makes a collision in a small sample statistically negligible.
func TestGenerateContainerSlugFormat(t *testing.T) {
	adjSet := make(map[string]bool, len(slugAdjectives))
	for _, w := range slugAdjectives {
		adjSet[w] = true
	}
	nounSet := make(map[string]bool, len(slugNouns))
	for _, w := range slugNouns {
		nounSet[w] = true
	}

	const n = 300
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		slug := GenerateContainerSlug()

		if !slugPattern.MatchString(slug) {
			t.Fatalf("iteration %d: slug %q does not match expected format %s", i, slug, slugPattern.String())
		}

		parts := strings.Split(slug, "-")
		if len(parts) != 3 {
			t.Fatalf("iteration %d: slug %q expected 3 dash-separated parts, got %d", i, slug, len(parts))
		}
		adj, noun, suffix := parts[0], parts[1], parts[2]

		if !adjSet[adj] {
			t.Errorf("iteration %d: slug %q: adjective %q is not from slugAdjectives", i, slug, adj)
		}
		if !nounSet[noun] {
			t.Errorf("iteration %d: slug %q: noun %q is not from slugNouns", i, slug, noun)
		}
		if len(suffix) != 4 {
			t.Errorf("iteration %d: slug %q: hex suffix %q expected length 4, got %d", i, slug, suffix, len(suffix))
		}

		seen[slug] = true
	}

	if len(seen) != n {
		t.Errorf("expected %d unique slugs out of %d generated (collision probability in this combinatoric space is negligible), got %d unique", n, n, len(seen))
	}
}

// TestPickWordLowercases pins pickWord's explicit strings.ToLower step.
func TestPickWordLowercases(t *testing.T) {
	cases := [][]string{
		{"MiXeDCase"},
		{"ALLCAPS"},
		{"already-lower"},
	}
	for _, words := range cases {
		got := pickWord(words)
		want := strings.ToLower(words[0])
		if got != want {
			t.Errorf("pickWord(%v) = %q, want %q", words, got, want)
		}
	}
}

// TestPickWordSelectsFromList runs many iterations to confirm pickWord only
// ever returns a (lowercased) member of the supplied list.
func TestPickWordSelectsFromList(t *testing.T) {
	words := []string{"alpha", "beta", "gamma", "delta"}
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	for i := 0; i < 100; i++ {
		got := pickWord(words)
		if !set[got] {
			t.Fatalf("iteration %d: pickWord(%v) = %q, not a member of the input list", i, words, got)
		}
	}
}

// TestRandomHexFormat pins randomHex's charset (lowercase hex) and length
// (2*n chars for n bytes).
func TestRandomHexFormat(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{4}$`)
	for i := 0; i < 50; i++ {
		h := randomHex(slugSuffixBytes)
		if !re.MatchString(h) {
			t.Fatalf("iteration %d: randomHex(%d) = %q, does not match %s", i, slugSuffixBytes, h, re.String())
		}
	}
}
