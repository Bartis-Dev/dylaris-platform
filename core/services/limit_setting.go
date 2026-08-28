package services

import "strconv"

// Operator-typed limits live in the settings table as strings, and this is the
// one place that reads and writes them. The platform limit convention
// (services.Limits) is a POINTER: nil is no cap, 0 is a real "none", n is the
// cap. A settings row cannot be a pointer, so it carries a sentinel - and the
// sentinel exists exactly once, here, instead of at every call site.
//
// Why it needs saying: every one of these settings previously spelled unlimited
// as 0 in its comment and then guarded the read with `n > 0`, which silently
// discarded the zero and fell back to the product default. Measured on
// srv.max_sub_servers before this: saving 0 produced a cap of THREE. Neither
// meaning the operator might have intended was reachable, and nothing said so.

// LimitUnlimited is what a settings row holds for "no cap at all". A word rather
// than a magic number, so the intent is legible in the database and cannot be
// confused with a quantity.
const LimitUnlimited = "unlimited"

// ParseLimitSetting reads an operator-typed limit.
//
//	""            the setting was never saved -> def, the product default
//	"unlimited"   no cap
//	"0"           a cap of NONE
//	"n"           a cap of n
//
// Anything unparseable or negative falls back to def rather than to a guess: an
// operator cannot type either through the panel, so reaching them means the row
// was edited by hand or written by an older build, and the product default is
// the only answer that is defensible without knowing which.
func ParseLimitSetting(raw string, def *int64) *int64 {
	switch raw {
	case "":
		return def
	case LimitUnlimited:
		return nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return def
	}
	return &n
}

// FormatLimitSetting is the inverse, and the only writer. A nil cap stores the
// word, never an empty string - empty means "never saved", and collapsing the
// two would make "the operator chose no limit" indistinguishable from "nobody
// has been here", which is how a deliberate choice gets overwritten by a default.
func FormatLimitSetting(v *int64) string {
	if v == nil {
		return LimitUnlimited
	}
	return strconv.FormatInt(*v, 10)
}

// LimitPtr is a convenience for the defaults declared in this package.
func LimitPtr(v int64) *int64 { return &v }
