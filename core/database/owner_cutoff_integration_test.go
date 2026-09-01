package database

import (
	"testing"
	"time"

	"dylaris-core/models"
	"dylaris-core/store"
)

// The cut-off rule exists twice: once in Go (store.OwnerCutOff, which LinkBoot
// and the warp enroll gate ask) and once in SQL (store.ownerCutOffSQL, which the
// ACL reconciler's two queries ask). They decide the same thing about the same
// tenant from two different places, and they must never disagree - a link the
// gate refuses but the reconciler re-provisions is a credential that comes back
// every 60 seconds, and one the gate admits but the reconciler tears down is a
// tenant whose tunnel drops on a timer nobody can see.
//
// Comments on both sides say they must match. This puts the same row through
// both and checks it, against a real Postgres, because "must match" written
// twice is not a mechanism.
//
// Skipped without DYLARIS_TEST_DB_HOST, like every test in this file's
// neighbourhood.
func TestOwnerCutOffMatchesItsSQLIntegration(t *testing.T) {
	db, st := integrationDB(t)

	const suspendGrace = 48 * time.Hour
	const overGrace = 72 * time.Hour
	now := time.Now()

	cases := []struct {
		name    string
		status  string
		suspend *time.Time
		over    *time.Time
	}{
		{name: "paying and within limits", status: "active"},
		{name: "suspended inside the payment grace", status: "suspended", suspend: ptrTime(now.Add(-1 * time.Hour))},
		{name: "suspended past the payment grace", status: "suspended", suspend: ptrTime(now.Add(-suspendGrace - time.Hour))},
		{name: "paying but over its limits inside that grace", status: "active", over: ptrTime(now.Add(-1 * time.Hour))},
		// The case the SQL had no arm for at all: still paying, past the
		// over-limit grace. The reconciler used to re-provision this tenant's
		// link on every tick, so the cutoff undid itself before anyone saw it.
		{name: "paying but over its limits past that grace", status: "active", over: ptrTime(now.Add(-overGrace - time.Hour))},
		{
			name:   "inside the payment grace and past the over-limit one",
			status: "suspended", suspend: ptrTime(now.Add(-1 * time.Hour)),
			over: ptrTime(now.Add(-overGrace - time.Hour)),
		},
	}

	// One link kit per case, plus the billing row that decides its fate.
	kitOwner := map[string]*store.UserBilling{} // node_id -> the row the Go side sees
	kitCase := map[string]string{}              // node_id -> case name, for the failure message
	for _, c := range cases {
		u := &models.User{Username: uniqueName("cut_"), Password: "x", Email: uniqueName("cut_") + "@example.test"}
		if err := st.CreateUser(u); err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		if _, err := db.Exec(
			`INSERT INTO user_billing (user_id, status, suspended_at, overlimit_since)
			 VALUES ($1,$2,$3,$4)`, u.ID, c.status, c.suspend, c.over); err != nil {
			t.Fatalf("insert billing for %s: %v", c.name, err)
		}
		nodeID := "link-" + uniqueName("k_")
		if _, err := db.Exec(
			`INSERT INTO warp_api_keys (name, key_hash, node_id, owner_id)
			 VALUES ($1,$2,$3,$4)`, nodeID, uniqueName("h_"), nodeID, u.ID); err != nil {
			t.Fatalf("insert link kit for %s: %v", c.name, err)
		}
		t.Cleanup(func() {
			db.Exec(`DELETE FROM warp_api_keys WHERE owner_id = $1`, u.ID)
			db.Exec(`DELETE FROM users WHERE id = $1`, u.ID)
		})

		b, err := st.GetUserBilling(u.ID)
		if err != nil {
			t.Fatalf("GetUserBilling for %s: %v", c.name, err)
		}
		kitOwner[nodeID] = b
		kitCase[nodeID] = c.name
	}

	// The same two cutoffs the reconciler computes.
	keep, err := st.ListLinkKitsForACLReconcile(now.Add(-suspendGrace), now.Add(-overGrace))
	if err != nil {
		t.Fatalf("ListLinkKitsForACLReconcile: %v", err)
	}
	// revokedAfter is in the future so the revoked arm can contribute nothing:
	// this test is about the cut-off arm alone.
	tear, err := st.ListLinkKitsForACLTeardown(now.Add(-suspendGrace), now.Add(-overGrace), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ListLinkKitsForACLTeardown: %v", err)
	}

	inKeep, inTear := map[string]bool{}, map[string]bool{}
	for _, k := range keep {
		inKeep[k.NodeID] = true
	}
	for _, k := range tear {
		inTear[k.NodeID] = true
	}

	for nodeID, b := range kitOwner {
		want := store.OwnerCutOff(b, suspendGrace, overGrace, now)
		name := kitCase[nodeID]

		if inKeep[nodeID] == want {
			t.Errorf("%s: Go says cut off = %v, but the reconcile query %s this kit, so on every 60s tick the ACL is %s",
				name, want, keepVerb(inKeep[nodeID]), keepConsequence(want))
		}
		if inTear[nodeID] != want {
			t.Errorf("%s: Go says cut off = %v, but the teardown query %s this kit",
				name, want, keepVerb(inTear[nodeID]))
		}
		// Belt and braces: a kit in both lists, or in neither, would flap on
		// every tick regardless of which side is right.
		if inKeep[nodeID] == inTear[nodeID] {
			t.Errorf("%s: the two queries agree with each other (both %v), so the reconciler both keeps and drops it",
				name, inKeep[nodeID])
		}
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func keepVerb(in bool) string {
	if in {
		return "returns"
	}
	return "omits"
}

func keepConsequence(cutOff bool) string {
	if cutOff {
		return "restored after every enforcement pass drops it"
	}
	return "torn down under a paying tenant"
}
