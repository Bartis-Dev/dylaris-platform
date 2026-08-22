package handlers

import (
	"strings"
	"testing"
)

// The hint had no caller at all, so neither route handler told the customer
// anything: the route was accepted, and four hours later it was gone. These
// pin what it must say once it is shown.
func TestCustomDomainDeadlineHint(t *testing.T) {
	t.Run("names the full target, never the bare label", func(t *testing.T) {
		got := customDomainDeadlineHint([]string{"route.eu.dylaris.com"})
		if !strings.Contains(got, "route.eu.dylaris.com") {
			t.Errorf("hint does not name the target: %q", got)
		}
		if !strings.Contains(got, "4 hours") {
			t.Errorf("hint does not state the deadline: %q", got)
		}
	})

	t.Run("one target per region, and the customer picks", func(t *testing.T) {
		got := customDomainDeadlineHint([]string{"route.eu.dylaris.com", "route.us.dylaris.com"})
		for _, want := range []string{"route.eu.dylaris.com", "route.us.dylaris.com"} {
			if !strings.Contains(got, want) {
				t.Errorf("hint omits %s: %q", want, got)
			}
		}
	})

	// An operator who has configured no hoster domain yet leaves no target to
	// name. The deadline still applies, so it still has to be stated.
	t.Run("no targets still warns about the deadline", func(t *testing.T) {
		got := customDomainDeadlineHint(nil)
		if !strings.Contains(got, "4 hours") {
			t.Errorf("hint drops the deadline when there is no target: %q", got)
		}
		if strings.Contains(got, "CNAME to ") {
			t.Errorf("hint promises a CNAME target it does not have: %q", got)
		}
	})
}
