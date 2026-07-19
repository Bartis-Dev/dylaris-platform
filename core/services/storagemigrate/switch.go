package storagemigrate

import (
	"errors"
	"fmt"
)

// ErrSwitchNotAuthorized is returned when the preconditions for pointing the
// live config at the migration target are not met.
var ErrSwitchNotAuthorized = errors.New("storagemigrate: switching the active config is not authorized")

// AuthorizeConfigSwitch gates the switching_config phase: the active storage
// config may be repointed at the target ONLY after a verification that passed.
//
// Note what is deliberately NOT required here: VerifyModeFull. A switch is
// reversible - the operator points the config back - and destroys nothing, so
// insisting on a full verification would only push operators into skipping
// verification altogether on large data sets. The irreversible step is the
// delete, and that one does insist on full (AuthorizeSourceDelete).
func AuthorizeConfigSwitch(report *StorageVerifyReport) error {
	if report == nil {
		return fmt.Errorf("%w: no verification report", ErrSwitchNotAuthorized)
	}
	if !report.OK {
		return fmt.Errorf("%w: verification found %d problem(s)", ErrSwitchNotAuthorized, report.ProblemsTotal)
	}
	return nil
}

// SwitchFailureReport is the message a failed switching_config phase reports.
//
// This is the ONE outcome where the operator must not be left guessing: the
// copy succeeded and verified, so the data now exists in BOTH places, and the
// old config is still the live one. Saying only "switch failed" would invite
// someone to start deleting. The phrasing names both locations, states that
// nothing was deleted, and says which config is active.
func SwitchFailureReport(sourceLabel, targetLabel string) string {
	return fmt.Sprintf(
		"The copy completed and verified, but switching the active config failed. "+
			"The data now exists in both places: %s (source) and %s (target). "+
			"Nothing was deleted. The SOURCE config is still active, so the system is "+
			"serving from %s. Fix the cause and re-run: the copy resumes and skips "+
			"everything already identical.",
		sourceLabel, targetLabel, sourceLabel)
}
