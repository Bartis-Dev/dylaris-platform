package services

import (
	"context"
	"reflect"
	"testing"
)

func TestVersionLine(t *testing.T) {
	// Both Minecraft schemes are live at once: the classic 1.x.y and the
	// calendar 26.x.y. Grouping must hold for both.
	tests := []struct {
		version string
		want    string
	}{
		{"1.21.11", "1.21"},
		{"1.21.2", "1.21"},
		{"1.21", "1.21"},
		{"1.20.6", "1.20"},
		{"26.1.2", "26.1"},
		{"26.1", "26.1"},
		{"26.2", "26.2"},
		{"1.7.10", "1.7"},
	}
	for _, tc := range tests {
		if got := VersionLine(tc.version); got != tc.want {
			t.Errorf("VersionLine(%q) = %q, want %q", tc.version, got, tc.want)
		}
	}
}

// The real release list as Modrinth returns it, newest first. Order comes from
// this list on purpose: a hand-written comparator has to rank 1.21.11 above
// 1.21.2 and 26.2 above 1.21.11, and getting that wrong fails silently.
var testReleases = []string{
	"26.2", "26.1.2", "26.1.1", "26.1",
	"1.21.11", "1.21.10", "1.21.9", "1.21.8", "1.21.7", "1.21.6",
	"1.21.5", "1.21.4", "1.21.3", "1.21.2", "1.21.1", "1.21",
	"1.20.6", "1.20.5", "1.20.4", "1.20.3", "1.20.2", "1.20.1", "1.20",
	"1.19.4", "1.19.3",
}

func TestSelectCompatTargets(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		mode     string
		specific string
		want     []string
	}{
		{
			name:    "all-newer includes the rest of the current line",
			current: "1.20.4",
			mode:    CompatModeAllNewer,
			want: []string{
				"1.20.5", "1.20.6",
				"1.21", "1.21.1", "1.21.2", "1.21.3", "1.21.4", "1.21.5",
				"1.21.6", "1.21.7", "1.21.8", "1.21.9", "1.21.10", "1.21.11",
				"26.1", "26.1.1", "26.1.2", "26.2",
			},
		},
		{
			name:    "newer-lines drops the remainder of the current line",
			current: "1.20.4",
			mode:    CompatModeNewerLines,
			want: []string{
				"1.21", "1.21.1", "1.21.2", "1.21.3", "1.21.4", "1.21.5",
				"1.21.6", "1.21.7", "1.21.8", "1.21.9", "1.21.10", "1.21.11",
				"26.1", "26.1.1", "26.1.2", "26.2",
			},
		},
		{
			name:    "newer-lines from a calendar version keeps only later lines",
			current: "26.1.1",
			mode:    CompatModeNewerLines,
			want:    []string{"26.2"},
		},
		{
			name:    "all-newer from a calendar version keeps its own line too",
			current: "26.1",
			mode:    CompatModeAllNewer,
			want:    []string{"26.1.1", "26.1.2", "26.2"},
		},
		{
			name:    "newest release has nothing newer",
			current: "26.2",
			mode:    CompatModeAllNewer,
			want:    []string{},
		},
		{
			name:     "specific picks exactly one",
			current:  "1.20.2",
			mode:     CompatModeSpecific,
			specific: "1.21.4",
			want:     []string{"1.21.4"},
		},
		{
			name:     "specific refuses a version that is not a known release",
			current:  "1.20.2",
			mode:     CompatModeSpecific,
			specific: "1.21.4-pre1",
			want:     nil,
		},
		{
			name:     "specific refuses the current version",
			current:  "1.20.2",
			mode:     CompatModeSpecific,
			specific: "1.20.2",
			want:     nil,
		},
		{
			// An undeclared or snapshot current version cannot be placed in the
			// list, so nothing can honestly be called newer than it.
			name:    "unplaceable current offers nothing",
			current: "24w14a",
			mode:    CompatModeAllNewer,
			want:    nil,
		},
		{
			name:    "unknown mode offers nothing",
			current: "1.20.2",
			mode:    "sideways",
			want:    nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectCompatTargets(testReleases, tc.current, tc.mode, tc.specific)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBucketStatus(t *testing.T) {
	tests := []struct {
		name         string
		side         string
		total, avail int
		want         string
	}{
		{"empty bucket is not a failure", CompatSideBoth, 0, 0, CompatStatusEmpty},
		{"complete bucket is green", CompatSideBoth, 5, 5, CompatStatusGreen},
		{"a both-sided miss is red", CompatSideBoth, 5, 4, CompatStatusRed},
		{"a client-only miss is orange", CompatSideClient, 3, 1, CompatStatusOrange},
		{"a server-only miss is orange", CompatSideServer, 3, 0, CompatStatusOrange},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := BucketStatus(tc.side, tc.total, tc.avail); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildVersionRow(t *testing.T) {
	items := []CompatItem{
		{Key: "1", ProjectID: "pboth", Side: CompatSideBoth, Title: "Both mod"},
		{Key: "2", ProjectID: "pclient", Side: CompatSideClient, Title: "Client mod"},
		{Key: "3", ProjectID: "pserver", Side: CompatSideServer, Title: "Server mod"},
	}
	searchable := map[string]bool{"pboth": true, "pclient": true, "pserver": true}

	t.Run("all available is green", func(t *testing.T) {
		row := buildVersionRow("1.21.4", items, map[string]bool{"pboth": true, "pclient": true, "pserver": true}, searchable)
		if row.Status != CompatStatusGreen {
			t.Fatalf("status = %q, want green", row.Status)
		}
		if len(row.Missing) != 0 {
			t.Fatalf("missing = %v, want none", row.Missing)
		}
	})

	t.Run("a single-sided miss is orange", func(t *testing.T) {
		row := buildVersionRow("1.21.4", items, map[string]bool{"pboth": true, "pserver": true}, searchable)
		if row.Status != CompatStatusOrange {
			t.Fatalf("status = %q, want orange", row.Status)
		}
		if row.Buckets[CompatSideClient].Status != CompatStatusOrange {
			t.Errorf("client bucket = %q, want orange", row.Buckets[CompatSideClient].Status)
		}
		if row.Buckets[CompatSideBoth].Status != CompatStatusGreen {
			t.Errorf("both bucket = %q, want green", row.Buckets[CompatSideBoth].Status)
		}
	})

	t.Run("a both-sided miss outranks a single-sided one", func(t *testing.T) {
		row := buildVersionRow("1.21.4", items, map[string]bool{"pserver": true}, searchable)
		if row.Status != CompatStatusRed {
			t.Fatalf("status = %q, want red", row.Status)
		}
		if len(row.Missing) != 2 {
			t.Fatalf("missing = %d entries, want 2", len(row.Missing))
		}
	})

	t.Run("a project the search index cannot answer for is unresolvable, not missing", func(t *testing.T) {
		row := buildVersionRow("1.21.4", items,
			map[string]bool{"pclient": true, "pserver": true},
			map[string]bool{"pclient": true, "pserver": true}) // pboth not searchable
		if len(row.Missing) != 1 {
			t.Fatalf("missing = %d entries, want 1", len(row.Missing))
		}
		if row.Missing[0].Reason != CompatReasonUnresolvable {
			t.Errorf("reason = %q, want %q", row.Missing[0].Reason, CompatReasonUnresolvable)
		}
	})

	t.Run("an item with no declared side counts as both", func(t *testing.T) {
		row := buildVersionRow("1.21.4",
			[]CompatItem{{Key: "1", ProjectID: "p", Side: ""}},
			map[string]bool{}, map[string]bool{"p": true})
		if row.Buckets[CompatSideBoth].Total != 1 {
			t.Fatalf("both bucket total = %d, want 1", row.Buckets[CompatSideBoth].Total)
		}
		if row.Status != CompatStatusRed {
			t.Errorf("status = %q, want red", row.Status)
		}
	})
}

func TestSideFromModrinth(t *testing.T) {
	tests := []struct {
		client, server string
		want           string
	}{
		{"required", "unsupported", CompatSideClient},
		{"optional", "unsupported", CompatSideClient},
		{"unsupported", "required", CompatSideServer},
		{"required", "required", CompatSideBoth},
		{"optional", "optional", CompatSideBoth},
		// An unknown declaration must not downgrade the severity: treating a
		// both-sided mod as single-sided would turn a red row orange.
		{"unknown", "unknown", CompatSideBoth},
		{"", "", CompatSideBoth},
	}
	for _, tc := range tests {
		if got := SideFromModrinth(tc.client, tc.server); got != tc.want {
			t.Errorf("SideFromModrinth(%q,%q) = %q, want %q", tc.client, tc.server, got, tc.want)
		}
	}
}

func TestReleaseVersionsDropsSnapshots(t *testing.T) {
	in := []GameVersion{
		{Version: "26.3-snapshot-10", VersionType: "snapshot"},
		{Version: "26.2", VersionType: "release"},
		{Version: "1.21.11", VersionType: "release"},
		{Version: "1.21.11-rc1", VersionType: "beta"},
	}
	got := ReleaseVersions(in)
	want := []string{"26.2", "1.21.11"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// The grouping must not use Modrinth's `major` flag: it marks 1.20.6, 1.20.4,
// 1.20.2 and 1.20.1 all as major (they are notable, not line heads) and marks
// nothing at all in 26.x. This pins that the flag stays unused for grouping.
func TestVersionLineIgnoresModrinthMajorFlag(t *testing.T) {
	majors := []GameVersion{
		{Version: "1.20.6", VersionType: "release", Major: true},
		{Version: "1.20.4", VersionType: "release", Major: true},
		{Version: "1.20.2", VersionType: "release", Major: true},
		{Version: "1.20.1", VersionType: "release", Major: true},
	}
	for _, v := range majors {
		if got := VersionLine(v.Version); got != "1.20" {
			t.Errorf("VersionLine(%q) = %q, want 1.20 regardless of major=%v", v.Version, got, v.Major)
		}
	}
}

func TestChunkStrings(t *testing.T) {
	in := []string{"a", "b", "c", "d", "e"}
	got := chunkStrings(in, 2)
	want := [][]string{{"a", "b"}, {"c", "d"}, {"e"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if len(chunkStrings(nil, 2)) != 0 {
		t.Error("nil input should chunk to nothing")
	}
}

func TestUniqueProjectIDsIsDeterministic(t *testing.T) {
	// Chunking must be stable across calls, or the same pack would produce
	// different chunk groupings and defeat the per-triple cache.
	a := uniqueProjectIDs([]CompatItem{{ProjectID: "z"}, {ProjectID: "a"}, {ProjectID: "z"}, {ProjectID: "m"}})
	b := uniqueProjectIDs([]CompatItem{{ProjectID: "m"}, {ProjectID: "z"}, {ProjectID: "a"}})
	if !reflect.DeepEqual(a, b) {
		t.Errorf("orderings differ: %v vs %v", a, b)
	}
	if !reflect.DeepEqual(a, []string{"a", "m", "z"}) {
		t.Errorf("got %v, want sorted unique ids", a)
	}
}

func TestBuildMatrixCountsUnlinkedWithoutFailingThem(t *testing.T) {
	// A manual upload has no project id. It must be reported, but it must not
	// turn every target version red for a reason the operator cannot act on
	// from the matrix.
	m, err := BuildMatrix(context.Background(), CompatRequest{
		Items: []CompatItem{
			{Key: "1", ProjectID: "", Side: CompatSideBoth},
			{Key: "2", ProjectID: "", Side: CompatSideBoth},
		},
		Loader:  "fabric",
		Current: "1.20.2",
		Mode:    CompatModeAllNewer,
		Targets: []string{"1.21.4"},
	})
	if err != nil {
		t.Fatalf("BuildMatrix: %v", err)
	}
	if m.Unlinked != 2 {
		t.Errorf("unlinked = %d, want 2", m.Unlinked)
	}
	if m.Checked != 0 {
		t.Errorf("checked = %d, want 0", m.Checked)
	}
	if len(m.Lines) != 0 {
		t.Errorf("lines = %v, want none when there is nothing checkable", m.Lines)
	}
}
