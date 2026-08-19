package handlers

import (
	"testing"
	"time"

	"dylaris-core/store"
)

// TestOwnerHardSuspended pins ownerHardSuspended (warp.go), which MUST stay
// equivalent to the SQL reconcile predicate (suspended AND
// suspended_at + grace <= now) so LinkBoot, the warp enroll gate, the billing
// enforcement pass and the ACL reconciler all agree on when a tenant is
// actually cut off. They cut different things - the link's credential, the
// tunnel itself, the servers - and a disagreement between them is a tenant who
// is half suspended.
func TestOwnerHardSuspended(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	const grace = 48 * time.Hour

	cases := []struct {
		name string
		b    *store.UserBilling
		want bool
	}{
		{"nil billing row is never hard-suspended", nil, false},
		{"active status is never hard-suspended", &store.UserBilling{Status: "active"}, false},
		{"past_due status is never hard-suspended", &store.UserBilling{Status: "past_due"}, false},
		{
			"suspended but nil SuspendedAt is never hard-suspended",
			&store.UserBilling{Status: "suspended", SuspendedAt: nil},
			false,
		},
		{
			"suspended and still within the grace window",
			&store.UserBilling{Status: "suspended", SuspendedAt: timePtr(now.Add(-1 * time.Hour))},
			false,
		},
		{
			"suspended and exactly at the grace boundary (suspended_at+grace == now) is hard-suspended",
			&store.UserBilling{Status: "suspended", SuspendedAt: timePtr(now.Add(-grace))},
			true,
		},
		{
			"suspended and past the grace window",
			&store.UserBilling{Status: "suspended", SuspendedAt: timePtr(now.Add(-grace - time.Hour))},
			true,
		},
		{
			"suspended one second short of the grace boundary is NOT hard-suspended",
			&store.UserBilling{Status: "suspended", SuspendedAt: timePtr(now.Add(-grace + time.Second))},
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ownerHardSuspended(c.b, grace, now); got != c.want {
				t.Errorf("ownerHardSuspended() = %v, want %v", got, c.want)
			}
		})
	}
}

func timePtr(t time.Time) *time.Time { return &t }
