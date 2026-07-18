package updates

import "testing"

func TestLineCountAndNonEmptyLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"only newlines", "\n\n\n", 0},
		{"one line no newline", `{"summary":"a"}`, 1},
		{"trailing newline", "{\"a\":1}\n", 1},
		{"blank lines between", "{\"a\":1}\n\n{\"b\":2}\n", 2},
		{"whitespace-only line ignored", "{\"a\":1}\n   \n{\"b\":2}", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LineCount([]byte(c.in)); got != c.want {
				t.Fatalf("LineCount(%q) = %d, want %d", c.in, got, c.want)
			}
			if got := len(NonEmptyLines([]byte(c.in))); got != c.want {
				t.Fatalf("len(NonEmptyLines(%q)) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestParseEntriesSkipsMalformed(t *testing.T) {
	lines := []string{
		`{"date":"2026-07-18","service":"platform","type":"feature","summary":"A"}`,
		`not json`,
		`{"date":"2026-07-18","service":"platform","type":"fix","summary":"B"}`,
	}
	got := ParseEntries(lines)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (malformed line skipped)", len(got))
	}
	if got[0].Summary != "A" || got[1].Type != "fix" {
		t.Fatalf("unexpected parse: %+v", got)
	}
}

func TestDeltaClamps(t *testing.T) {
	remote := []string{"a", "b", "c"}
	cases := []struct {
		name      string
		installed int
		want      []string
	}{
		{"none installed", 0, []string{"a", "b", "c"}},
		{"one installed", 1, []string{"b", "c"}},
		{"all installed", 3, nil},
		{"past end", 9, nil},
		{"negative clamps to zero", -2, []string{"a", "b", "c"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Delta(remote, c.installed)
			if len(got) != len(c.want) {
				t.Fatalf("Delta(_, %d) len = %d, want %d", c.installed, len(got), len(c.want))
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("Delta(_, %d)[%d] = %q, want %q", c.installed, i, got[i], c.want[i])
				}
			}
		})
	}
}

// The committed platform feed must be valid JSONL: every non-empty line parses.
// A malformed appended line would silently drop from the panel, so fail the
// build instead. (Trivially passes while the feed is empty.)
func TestEmbeddedPlatformFeedIsValidJSONL(t *testing.T) {
	lines := NonEmptyLines(PlatformFeed())
	if got, want := len(ParseEntries(lines)), len(lines); got != want {
		t.Fatalf("embedded feed has malformed lines: %d/%d parsed", got, want)
	}
}
