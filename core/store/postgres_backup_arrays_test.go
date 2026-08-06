package store

import (
	"database/sql/driver"
	"testing"

	"github.com/lib/pq"
)

// include_patterns and exclude_patterns are the only NOT NULL array columns in
// the schema, and both are optional in the API - a create request that omits
// them decodes to a nil slice. pq.Array(nil) is NULL on the wire, and naming
// the column in the INSERT means its DEFAULT '{}' never applies, so the row was
// rejected with a not-null violation and the caller got a bare 500.
func TestTextArrayNeverWritesNull(t *testing.T) {
	// The premise, asserted rather than assumed: this is what the helper is for.
	raw, err := pq.Array([]string(nil)).(driver.Valuer).Value()
	if err != nil {
		t.Fatalf("pq.Array(nil).Value() error: %v", err)
	}
	if raw != nil {
		t.Fatalf("pq.Array(nil) no longer produces NULL (got %v) - textArray may be obsolete", raw)
	}

	for _, in := range [][]string{nil, {}} {
		v, err := textArray(in).(driver.Valuer).Value()
		if err != nil {
			t.Fatalf("textArray(%v).Value() error: %v", in, err)
		}
		if v == nil {
			t.Errorf("textArray(%v) produced NULL, want an empty array", in)
			continue
		}
		if got, ok := v.([]byte); ok && string(got) != "{}" {
			t.Errorf("textArray(%v) = %q, want \"{}\"", in, got)
		} else if got, ok := v.(string); ok && got != "{}" {
			t.Errorf("textArray(%v) = %q, want \"{}\"", in, got)
		}
	}

	// A populated list must still round-trip unchanged.
	v, err := textArray([]string{"world/*", "*.log"}).(driver.Valuer).Value()
	if err != nil {
		t.Fatalf("textArray(populated).Value() error: %v", err)
	}
	want := `{"world/*","*.log"}`
	switch got := v.(type) {
	case []byte:
		if string(got) != want {
			t.Errorf("textArray(populated) = %q, want %q", got, want)
		}
	case string:
		if got != want {
			t.Errorf("textArray(populated) = %q, want %q", got, want)
		}
	default:
		t.Errorf("textArray(populated) returned %T, want a string or []byte", v)
	}
}
