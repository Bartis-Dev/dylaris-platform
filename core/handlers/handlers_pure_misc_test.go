package handlers

import (
	"errors"
	"strings"
	"testing"
	"time"

	"dylaris-core/store"
)

// TestComputeNextRun pins the schedule-string parser (backup.go): "every Nh"
// / "every Nd" via fmt.Sscanf; "manual"/empty/malformed all fall back to nil
// (the caller then treats the job as manual-only).
func TestComputeNextRun(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("empty schedule returns nil", func(t *testing.T) {
		if got := computeNextRun("", from); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("manual returns nil", func(t *testing.T) {
		if got := computeNextRun("manual", from); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("every 6h adds 6 hours", func(t *testing.T) {
		got := computeNextRun("every 6h", from)
		if got == nil || !got.Equal(from.Add(6*time.Hour)) {
			t.Errorf("got %v, want %v", got, from.Add(6*time.Hour))
		}
	})
	t.Run("every 2d adds 48 hours", func(t *testing.T) {
		got := computeNextRun("every 2d", from)
		if got == nil || !got.Equal(from.Add(48*time.Hour)) {
			t.Errorf("got %v, want %v", got, from.Add(48*time.Hour))
		}
	})
	t.Run("every 0h (non-positive n) returns nil", func(t *testing.T) {
		if got := computeNextRun("every 0h", from); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("every -1h (negative n) returns nil", func(t *testing.T) {
		if got := computeNextRun("every -1h", from); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("unknown unit returns nil", func(t *testing.T) {
		if got := computeNextRun("every 3x", from); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("garbage schedule returns nil", func(t *testing.T) {
		if got := computeNextRun("whenever I feel like it", from); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

// TestCannedRequestValidate pins the name/body boundary checks (1..128,
// 1..10000) on the canned-response create/update payload.
func TestCannedRequestValidate(t *testing.T) {
	catID := 7

	t.Run("empty name rejected", func(t *testing.T) {
		req := cannedRequest{Name: "", Body: "hello"}
		if _, err := req.validate(); err == nil {
			t.Fatalf("expected error for empty name")
		}
	})
	t.Run("name over 128 chars rejected", func(t *testing.T) {
		req := cannedRequest{Name: strings.Repeat("a", 129), Body: "hello"}
		if _, err := req.validate(); err == nil {
			t.Fatalf("expected error for over-length name")
		}
	})
	t.Run("name exactly 128 chars accepted", func(t *testing.T) {
		req := cannedRequest{Name: strings.Repeat("a", 128), Body: "hello"}
		if _, err := req.validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("empty body rejected", func(t *testing.T) {
		req := cannedRequest{Name: "greeting", Body: ""}
		if _, err := req.validate(); err == nil {
			t.Fatalf("expected error for empty body")
		}
	})
	t.Run("body over 10000 chars rejected", func(t *testing.T) {
		req := cannedRequest{Name: "greeting", Body: strings.Repeat("b", 10001)}
		if _, err := req.validate(); err == nil {
			t.Fatalf("expected error for over-length body")
		}
	})
	t.Run("body exactly 10000 chars accepted", func(t *testing.T) {
		req := cannedRequest{Name: "greeting", Body: strings.Repeat("b", 10000)}
		if _, err := req.validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("valid request trims whitespace and carries fields through", func(t *testing.T) {
		req := cannedRequest{Name: "  Greeting  ", Body: "  Hello there  ", CategoryID: &catID}
		got, err := req.validate()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Name != "Greeting" || got.Body != "Hello there" {
			t.Fatalf("got Name=%q Body=%q, want trimmed values", got.Name, got.Body)
		}
		if got.CategoryID == nil || *got.CategoryID != catID {
			t.Fatalf("got CategoryID=%v, want %d", got.CategoryID, catID)
		}
	})
}

// TestPlanBodyValid pins the limits->=0 + name-required guard (plans.go).
// Limits are 0=unlimited, so zero is valid; only negative values are rejected.
func TestPlanBodyValid(t *testing.T) {
	base := planBody{Name: "Starter", MaxNodes: 1, MaxLinks: 1, R2QuotaGb: 10, TrafficEdgeGb: 10, TrafficRelayGb: 10, TrafficCombinedGb: 10}

	cases := []struct {
		name string
		b    planBody
		want bool
	}{
		{"valid full plan", base, true},
		{"zero limits (unlimited) accepted", planBody{Name: "Unlimited"}, true},
		{"empty name rejected", func() planBody { b := base; b.Name = ""; return b }(), false},
		{"negative MaxNodes rejected", func() planBody { b := base; b.MaxNodes = -1; return b }(), false},
		{"negative MaxLinks rejected", func() planBody { b := base; b.MaxLinks = -1; return b }(), false},
		{"negative R2QuotaGb rejected", func() planBody { b := base; b.R2QuotaGb = -1; return b }(), false},
		{"negative TrafficEdgeGb rejected", func() planBody { b := base; b.TrafficEdgeGb = -1; return b }(), false},
		{"negative TrafficRelayGb rejected", func() planBody { b := base; b.TrafficRelayGb = -1; return b }(), false},
		{"negative TrafficCombinedGb rejected", func() planBody { b := base; b.TrafficCombinedGb = -1; return b }(), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.b.valid(); got != c.want {
				t.Errorf("planBody.valid() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestPlanBodyToPlan pins the field-by-field mapping into store.Plan so a
// future field-shuffle regresses loudly instead of silently swapping values.
func TestPlanBodyToPlan(t *testing.T) {
	b := planBody{
		Name: "Pro", PriceLabel: "$10/mo", MaxNodes: 5, MaxLinks: 3,
		R2QuotaGb: 100, TrafficEdgeGb: 50, TrafficRelayGb: 20, TrafficCombinedGb: 70,
		IsDefault: true,
	}
	got := b.toPlan(42)
	want := store.Plan{
		ID: 42, Name: "Pro", PriceLabel: "$10/mo", MaxNodes: 5, MaxLinks: 3,
		R2QuotaGB: 100, TrafficEdgeGB: 50, TrafficRelayGB: 20, TrafficCombinedGB: 70,
		IsDefault: true,
	}
	if got != want {
		t.Errorf("toPlan() = %+v, want %+v", got, want)
	}
}

func TestNormalizeLibraryPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already normalized", "foo/bar", "foo/bar"},
		{"leading slash stripped", "/foo/bar", "foo/bar"},
		{"trailing slash stripped", "foo/bar/", "foo/bar"},
		{"both leading and trailing stripped", "/foo/bar/", "foo/bar"},
		{"multiple leading and trailing slashes fully stripped", "///foo///", "foo"},
		{"whitespace trimmed first", "  foo/bar  ", "foo/bar"},
		{"empty stays empty", "", ""},
		{"single slash normalizes to empty (root)", "/", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeLibraryPath(c.in); got != c.want {
				t.Errorf("normalizeLibraryPath(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestJoinLibraryPath(t *testing.T) {
	cases := []struct {
		name   string
		parent string
		child  string
		want   string
	}{
		{"empty parent returns child unchanged", "", "file.txt", "file.txt"},
		{"root-normalized empty parent returns child unchanged", "/", "file.txt", "file.txt"},
		{"non-empty parent joined with slash", "foo", "bar.txt", "foo/bar.txt"},
		{"parent with slashes normalized before joining", "/foo/", "bar.txt", "foo/bar.txt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := joinLibraryPath(c.parent, c.child); got != c.want {
				t.Errorf("joinLibraryPath(%q, %q) = %q, want %q", c.parent, c.child, got, c.want)
			}
		})
	}
}

func TestIsPathBlocked(t *testing.T) {
	disabled := map[string]struct{}{
		"foo":        {},
		"baz/nested": {},
	}

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"root path is never blockable", "", false},
		{"exact match is blocked", "foo", true},
		{"descendant of a blocked ancestor is blocked", "foo/bar", true},
		{"deep descendant of a blocked ancestor is blocked", "foo/bar/baz", true},
		{"unrelated path is not blocked", "other/bar", false},
		{"blocked nested path with unrelated top-level sibling", "baz/nested/child", true},
		{"sibling of a blocked path is not blocked", "baz/other", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isPathBlocked(c.path, disabled); got != c.want {
				t.Errorf("isPathBlocked(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}

	t.Run("empty disabled set blocks nothing", func(t *testing.T) {
		if isPathBlocked("foo/bar", map[string]struct{}{}) {
			t.Errorf("expected false with an empty disabled set")
		}
	})
}

func TestNormalizeTagList(t *testing.T) {
	got := normalizeTagList([]string{"Foo", " Bar ", "", "  ", "BAZ"})
	want := []string{"foo", "bar", "baz"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}

	t.Run("nil input returns empty (non-nil) slice", func(t *testing.T) {
		got := normalizeTagList(nil)
		if len(got) != 0 {
			t.Errorf("got %#v, want empty slice", got)
		}
	})
}

func TestDedupe(t *testing.T) {
	got := dedupe([]string{"a", "b", "a", "c", "b"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}

	t.Run("case-sensitive - different case is NOT deduped", func(t *testing.T) {
		got := dedupe([]string{"X", "x"})
		if len(got) != 2 {
			t.Errorf("got %#v, want both entries kept (case-sensitive)", got)
		}
	})

	t.Run("empty input returns empty slice", func(t *testing.T) {
		got := dedupe(nil)
		if len(got) != 0 {
			t.Errorf("got %#v, want empty slice", got)
		}
	})
}

// TestDbUnavailable pins the transient-vs-genuine-failure classifier
// (auth.go) that drives the 503-vs-500 choice on login.
func TestDbUnavailable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"connection refused", errors.New("dial tcp 127.0.0.1:5432: connect: connection refused"), true},
		{"no such host", errors.New("dial tcp: lookup db.internal: no such host"), true},
		{"broken pipe", errors.New("write: broken pipe"), true},
		{"i/o timeout", errors.New("read tcp: i/o timeout"), true},
		{"database starting up", errors.New("pq: the database system is starting up"), true},
		{"schema not yet rebuilt", errors.New("pq: relation \"users\" does not exist"), true},
		{"case-insensitive match", errors.New("CONNECTION REFUSED"), true},
		{"genuine query error is not transient", errors.New("pq: syntax error at or near \"SELCT\""), false},
		{"generic not-found error is not transient", errors.New("record not found"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := dbUnavailable(c.err); got != c.want {
				t.Errorf("dbUnavailable(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
