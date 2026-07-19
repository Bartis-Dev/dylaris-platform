package storagemigrate

import (
	"errors"
	"strings"
	"testing"
)

func TestAuthorizeConfigSwitch(t *testing.T) {
	cases := []struct {
		name    string
		report  *StorageVerifyReport
		wantErr bool
	}{
		{"authorized: a passing full verification", &StorageVerifyReport{OK: true, Mode: VerifyModeFull}, false},
		// A switch is reversible (point the config back) and destroys nothing,
		// so a passing SAMPLE verification is enough for it. Only the delete,
		// which is irreversible, insists on a full run.
		{"authorized: a passing sample verification", &StorageVerifyReport{OK: true, Mode: VerifyModeSample, CheckedFraction: 0.1}, false},
		{"blocked: verification failed", &StorageVerifyReport{OK: false, Mode: VerifyModeFull, ProblemsTotal: 2}, true},
		{"blocked: no report at all", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := AuthorizeConfigSwitch(c.report)
			if (err != nil) != c.wantErr {
				t.Fatalf("AuthorizeConfigSwitch(%+v) err = %v, wantErr %v", c.report, err, c.wantErr)
			}
			if c.wantErr && !errors.Is(err, ErrSwitchNotAuthorized) {
				t.Errorf("err = %v, want it to wrap ErrSwitchNotAuthorized", err)
			}
		})
	}
}

func TestSwitchFailureReport_NamesBothPlacesAndTheActiveConfig(t *testing.T) {
	// A failed switch is the one outcome where the operator MUST be told the
	// system is duplicated rather than broken, and which side is live. A vague
	// "switch failed" would invite them to delete something.
	msg := SwitchFailureReport("path:/mnt/old/library", "s3:https://new.example.com/bucket/library")
	for _, want := range []string{"path:/mnt/old/library", "s3:https://new.example.com/bucket/library"} {
		if !strings.Contains(msg, want) {
			t.Errorf("report %q does not name %q", msg, want)
		}
	}
	for _, want := range []string{"both", "still active", "Nothing was deleted"} {
		if !strings.Contains(msg, want) {
			t.Errorf("report %q is missing the phrase %q", msg, want)
		}
	}
}
