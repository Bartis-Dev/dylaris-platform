package services

import "testing"

func TestParseLimitSetting(t *testing.T) {
	def := LimitPtr(3)

	cases := []struct {
		name string
		raw  string
		want *int64 // nil means "no cap"
		// isDefault marks the cases that must come back as the product default
		// rather than as a decision the operator made.
		isDefault bool
	}{
		{name: "never saved falls back to the product default", raw: "", want: def, isDefault: true},
		{name: "the word means no cap", raw: LimitUnlimited, want: nil},
		// The case that was unreachable before. Saving 0 used to be swallowed by
		// an `n > 0` guard and produce the default, so an operator asking for
		// none silently got three.
		{name: "zero is a cap of NONE, not the default", raw: "0", want: LimitPtr(0)},
		{name: "a number is that cap", raw: "12", want: LimitPtr(12)},
		// A hand-edited or older-build row. Guessing here would be worse than
		// the default: we do not know which meaning was intended.
		{name: "garbage falls back to the default", raw: "banana", want: def, isDefault: true},
		{name: "a negative falls back to the default", raw: "-1", want: def, isDefault: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseLimitSetting(c.raw, def)
			switch {
			case c.want == nil:
				if got != nil {
					t.Fatalf("got a cap of %d, want no cap", *got)
				}
			case got == nil:
				t.Fatalf("got no cap, want %d", *c.want)
			case *got != *c.want:
				t.Fatalf("got %d, want %d", *got, *c.want)
			}
			if c.isDefault && got != def {
				t.Error("the default must be returned as-is, not copied: callers compare identity to tell 'unset' apart")
			}
		})
	}
}

// A nil default is legal and means "no cap unless the operator sets one", which
// is what most of these knobs want.
func TestParseLimitSettingWithNoDefault(t *testing.T) {
	if got := ParseLimitSetting("", nil); got != nil {
		t.Errorf("got %d, want no cap", *got)
	}
	if got := ParseLimitSetting("0", nil); got == nil || *got != 0 {
		t.Errorf("got %v, want a cap of 0 even with no default", got)
	}
}

func TestFormatLimitSetting(t *testing.T) {
	if got := FormatLimitSetting(nil); got != LimitUnlimited {
		t.Errorf("nil formatted as %q, want %q", got, LimitUnlimited)
	}
	if got := FormatLimitSetting(LimitPtr(0)); got != "0" {
		t.Errorf("a cap of none formatted as %q, want \"0\"", got)
	}
	if got := FormatLimitSetting(LimitPtr(42)); got != "42" {
		t.Errorf("formatted as %q", got)
	}
}

// The property that makes the pair safe: whatever the operator chose survives a
// save and a reload. A nil that stored as "" would come back as the DEFAULT on
// the next read, quietly replacing "no limit" with whatever the product ships.
func TestLimitSettingRoundTrip(t *testing.T) {
	def := LimitPtr(3)
	for _, v := range []*int64{nil, LimitPtr(0), LimitPtr(1), LimitPtr(9999)} {
		got := ParseLimitSetting(FormatLimitSetting(v), def)
		switch {
		case v == nil && got != nil:
			t.Errorf("no cap came back as %d", *got)
		case v != nil && got == nil:
			t.Errorf("a cap of %d came back as no cap", *v)
		case v != nil && got != nil && *v != *got:
			t.Errorf("a cap of %d came back as %d", *v, *got)
		}
	}
}
