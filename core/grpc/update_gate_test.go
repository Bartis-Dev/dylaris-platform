package nodegrpc

import (
	"strings"
	"testing"
	"time"

	pb "dylaris-proto/node"

	"dylaris-pkg/release"
)

// A release declaring a mandatory node update, due at a fixed instant so the
// tests can stand on either side of it.
const gatedNotes = "## 2026.08.28\n" +
	"**Update required by 2026-09-05 14:00 UTC** - older nodes stop connecting.\n" +
	"### Features\n- A protocol change. `node`\n" +
	"### Breaking\n- Nothing.\n" +
	"### Security\n- Nothing.\n" +
	"### Fixes\n- Nothing.\n" +
	"\n## 2026.08.20\n" +
	"### Features\n- Something for Core only. `core`\n" +
	"### Breaking\n- Nothing.\n" +
	"### Security\n- Nothing.\n" +
	"### Fixes\n- Nothing.\n"

var deadline = time.Date(2026, 9, 5, 14, 0, 0, 0, time.UTC)

func gate(t *testing.T, enforce bool, now time.Time) *UpdateGate {
	t.Helper()
	rs, err := release.Parse([]byte(gatedNotes))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return &UpdateGate{releases: rs, enforce: enforce, now: func() time.Time { return now }}
}

// The single most expensive thing this could get wrong. Every image built
// before release stamping reports nothing, so ordering an unknown version would
// refuse the entire installed base on the day this ships.
func TestUnknownVersionIsAlwaysAdmitted(t *testing.T) {
	after := deadline.Add(24 * time.Hour)
	for _, reported := range []string{"", "   ", "unknown", "v1.2.3", "2026.8.28"} {
		v := gate(t, true, after).Check("node", reported)
		if v.Refuse || v.Warn {
			t.Errorf("reported %q: got %+v, want silent admission", reported, v)
		}
	}
}

func TestGateAdmitsWhatItShould(t *testing.T) {
	before := deadline.Add(-24 * time.Hour)

	t.Run("a node already on the required release", func(t *testing.T) {
		if v := gate(t, true, before).Check("node", "2026.08.28"); v.Refuse || v.Warn {
			t.Errorf("got %+v, want silence", v)
		}
	})

	t.Run("a node ahead of the required release", func(t *testing.T) {
		if v := gate(t, true, before).Check("node", "2026.09.01"); v.Refuse || v.Warn {
			t.Errorf("got %+v, want silence", v)
		}
	})

	// The requirement names `node`. A component it does not name is not subject
	// to it, however old that component is.
	t.Run("a component the requirement does not name", func(t *testing.T) {
		if v := gate(t, true, deadline.Add(time.Hour)).Check("core", "2026.08.20"); v.Refuse || v.Warn {
			t.Errorf("got %+v, want silence: the requirement never names core", v)
		}
	})
}

func TestGateWarnsBeforeTheDeadline(t *testing.T) {
	v := gate(t, true, deadline.Add(-72*time.Hour)).Check("node", "2026.08.20")
	if v.Refuse {
		t.Fatal("refused before the deadline")
	}
	if !v.Warn {
		t.Fatal("a node behind the floor was not warned")
	}
	if v.MinVersion != "2026.08.28" {
		t.Errorf("MinVersion = %q", v.MinVersion)
	}
	if !v.Deadline.Equal(deadline) {
		t.Errorf("Deadline = %v, want %v", v.Deadline, deadline)
	}
	// The reason from the file has to survive: it is the half that says WHY.
	if !strings.Contains(v.Message, "older nodes stop connecting") {
		t.Errorf("the note was dropped from %q", v.Message)
	}
}

func TestGateRefusesAfterTheDeadline(t *testing.T) {
	v := gate(t, true, deadline.Add(time.Second)).Check("node", "2026.08.20")
	if !v.Refuse {
		t.Fatal("a node past the deadline was not refused")
	}
	// The message is what the operator reads instead of an unexplained
	// connection failure, so it has to carry both versions and the deadline.
	for _, want := range []string{"2026.08.20", "2026.08.28", "2026-09-05"} {
		if !strings.Contains(v.Message, want) {
			t.Errorf("message %q does not mention %q", v.Message, want)
		}
	}
}

// The off switch. It exists because a wrong deadline or a mis-stamped image
// locks paying customers out of their own hardware, and that must be
// recoverable without a rebuild.
func TestEnforcementCanBeDisabled(t *testing.T) {
	v := gate(t, false, deadline.Add(48*time.Hour)).Check("node", "2026.08.20")
	if v.Refuse {
		t.Fatal("refused with enforcement off")
	}
	if !v.Warn {
		t.Fatal("enforcement off should still warn")
	}
	// ...and it must not read like there is time left.
	if strings.Contains(v.Message, "will stop connecting") {
		t.Errorf("an overdue node was told it still has time: %q", v.Message)
	}
}

// A nil gate is what every test server and any Core without notes carries.
func TestNilGateAdmits(t *testing.T) {
	var g *UpdateGate
	if v := g.Check("node", "2026.08.20"); v.Refuse || v.Warn {
		t.Errorf("got %+v, want silence", v)
	}
}

// Both success paths carry the warning. Attaching it to one would deliver it on
// the connect where a node happens to enroll and never again.
func TestApplyUpdateWarning(t *testing.T) {
	ar := &pb.AuthResult{Ok: true}
	applyUpdateWarning(ar, UpdateVerdict{Warn: true, Message: "update me", MinVersion: "2026.08.28", Deadline: deadline})
	if ar.UpdateRequired != "update me" || ar.UpdateRequiredVersion != "2026.08.28" {
		t.Errorf("got %+v", ar)
	}
	if ar.UpdateRequiredDeadline != "2026-09-05T14:00:00Z" {
		t.Errorf("deadline = %q", ar.UpdateRequiredDeadline)
	}

	// A verdict with nothing to say must leave the result untouched, or every
	// node would carry an empty warning field it has to reason about.
	clean := &pb.AuthResult{Ok: true}
	applyUpdateWarning(clean, UpdateVerdict{})
	if clean.UpdateRequired != "" || clean.UpdateRequiredVersion != "" || clean.UpdateRequiredDeadline != "" {
		t.Errorf("a silent verdict wrote something: %+v", clean)
	}

	// A refusal is not a warning: it never reaches a successful auth result.
	refused := &pb.AuthResult{Ok: true}
	applyUpdateWarning(refused, UpdateVerdict{Refuse: true, Message: "no"})
	if refused.UpdateRequired != "" {
		t.Errorf("a refusal was written onto a success: %+v", refused)
	}
}
