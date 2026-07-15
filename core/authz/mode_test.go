package authz

import "testing"

func TestValidMode(t *testing.T) {
	for _, m := range []string{ModeOff, ModeSimple, ModeAdvanced} {
		if !ValidMode(m) {
			t.Errorf("ValidMode(%q) = false, want true", m)
		}
	}
	for _, m := range []string{"", "OFF", "Simple", "bogus"} {
		if ValidMode(m) {
			t.Errorf("ValidMode(%q) = true, want false", m)
		}
	}
}

func TestNormalizeMode(t *testing.T) {
	if got := NormalizeMode(ModeAdvanced); got != ModeAdvanced {
		t.Errorf("NormalizeMode(advanced) = %q, want advanced", got)
	}
	if got := NormalizeMode("bogus"); got != ModeSimple {
		t.Errorf("NormalizeMode(bogus) = %q, want simple (default)", got)
	}
	if got := NormalizeMode(""); got != ModeSimple {
		t.Errorf("NormalizeMode(\"\") = %q, want simple (default)", got)
	}
}
