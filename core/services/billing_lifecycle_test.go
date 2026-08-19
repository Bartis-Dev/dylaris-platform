package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"dylaris-core/models"
	"dylaris-core/store"
)

// billingFakeStore embeds store.Store (nil) so it satisfies the full interface
// at compile time; only the methods the billing lifecycle touches are
// overridden. Any other call would panic - these tests never make one.
//
// GetUserByID defaults to an error so sendDunningEmail/sendSuspendedEmail
// always short-circuit before touching the mailer - email delivery is
// best-effort side-plumbing, not the lifecycle decision logic under test here.
type billingFakeStore struct {
	store.Store

	billing    *store.UserBilling
	billingErr error

	planID    *int
	planIDErr error
	plan      *store.Plan
	planErr   error
	def       *store.Plan

	settings map[string]string

	backupBytes int64
	backupErr   error

	pastDue   []store.UserBilling
	suspended []store.UserBilling

	statusCalls []statusCall
	statusErr   error

	listServersCalls []string
	servers          map[string][]models.Server

	listBackupsCalls []string
	backupRuns       map[string][]store.BackupRunRef

	warpKeys      []store.WarpAPIKey
	warpKeysErr   error
	warpKeyOwners []string
}

type statusCall struct {
	userID      string
	status      string
	graceUntil  *time.Time
	suspendedAt *time.Time
}

func (f *billingFakeStore) GetUserBilling(string) (*store.UserBilling, error) {
	return f.billing, f.billingErr
}
func (f *billingFakeStore) GetUserPlanID(string) (*int, error)    { return f.planID, f.planIDErr }
func (f *billingFakeStore) GetPlan(int) (*store.Plan, error)      { return f.plan, f.planErr }
func (f *billingFakeStore) GetDefaultPlan() (*store.Plan, error)  { return f.def, nil }
func (f *billingFakeStore) GetSetting(key string) (string, error) { return f.settings[key], nil }
func (f *billingFakeStore) BackupBytesByOwner(string) (int64, error) {
	return f.backupBytes, f.backupErr
}

func (f *billingFakeStore) ListUserBillingByStatus(status string) ([]store.UserBilling, error) {
	switch status {
	case "past_due":
		return f.pastDue, nil
	case "suspended":
		return f.suspended, nil
	}
	return nil, nil
}

func (f *billingFakeStore) SetUserBillingStatus(userID, status string, graceUntil, suspendedAt *time.Time) error {
	f.statusCalls = append(f.statusCalls, statusCall{userID, status, graceUntil, suspendedAt})
	return f.statusErr
}

func (f *billingFakeStore) ListServersByOwner(ownerID string) ([]models.Server, error) {
	f.listServersCalls = append(f.listServersCalls, ownerID)
	return f.servers[ownerID], nil
}

func (f *billingFakeStore) ListBackupRunsByOwner(ownerID string) ([]store.BackupRunRef, error) {
	f.listBackupsCalls = append(f.listBackupsCalls, ownerID)
	return f.backupRuns[ownerID], nil
}

func (f *billingFakeStore) GetUserByID(string) (*models.User, error) {
	return nil, errors.New("no user: email lookups are out of scope for lifecycle decision tests")
}

func timePtr(t time.Time) *time.Time { return &t }

// --- R2QuotaExceeded: quota resolution priority (per-user > plan > platform setting > unlimited) ---

func TestR2QuotaExceeded(t *testing.T) {
	const GB = int64(1024 * 1024 * 1024)

	cases := []struct {
		name         string
		store        *billingFakeStore
		wantExceeded bool
		wantUsed     int64
		wantQuotaGB  int64
	}{
		{
			name: "per-user override under quota",
			store: &billingFakeStore{
				billing:     &store.UserBilling{R2QuotaGB: ptr(10)},
				backupBytes: 5 * GB,
			},
			wantExceeded: false,
			wantUsed:     5 * GB,
			wantQuotaGB:  10,
		},
		{
			name: "per-user override at quota is exceeded (>=)",
			store: &billingFakeStore{
				billing:     &store.UserBilling{R2QuotaGB: ptr(10)},
				backupBytes: 10 * GB,
			},
			wantExceeded: true,
			wantUsed:     10 * GB,
			wantQuotaGB:  10,
		},
		{
			name: "per-user override explicit 0 means unlimited",
			store: &billingFakeStore{
				billing:     &store.UserBilling{R2QuotaGB: ptr(0)},
				backupBytes: 999 * GB,
			},
			wantExceeded: false,
			wantQuotaGB:  0,
		},
		{
			name: "assigned plan is used and exceeded",
			store: &billingFakeStore{
				billing:     &store.UserBilling{},
				planID:      iptr(7),
				plan:        &store.Plan{R2QuotaGB: 5},
				backupBytes: 6 * GB,
			},
			wantExceeded: true,
			wantUsed:     6 * GB,
			wantQuotaGB:  5,
		},
		{
			name: "assigned plan explicit 0 does not fall through to the platform setting",
			store: &billingFakeStore{
				billing:     &store.UserBilling{},
				planID:      iptr(7),
				plan:        &store.Plan{R2QuotaGB: 0},
				settings:    map[string]string{BillingR2QuotaKey: "1"},
				backupBytes: 999 * GB,
			},
			wantExceeded: false,
			wantQuotaGB:  0,
		},
		{
			name: "no assigned plan falls back to the default plan",
			store: &billingFakeStore{
				billing:     &store.UserBilling{},
				def:         &store.Plan{R2QuotaGB: 2},
				backupBytes: 3 * GB,
			},
			wantExceeded: true,
			wantUsed:     3 * GB,
			wantQuotaGB:  2,
		},
		{
			name: "no plan lookup at all falls back to the platform setting",
			store: &billingFakeStore{
				billing:     &store.UserBilling{},
				planIDErr:   errors.New("plan lookup unavailable"),
				settings:    map[string]string{BillingR2QuotaKey: "1"},
				backupBytes: 2 * GB,
			},
			wantExceeded: true,
			wantUsed:     2 * GB,
			wantQuotaGB:  1,
		},
		{
			name: "nothing set anywhere means unlimited",
			store: &billingFakeStore{
				billing:     &store.UserBilling{},
				planIDErr:   errors.New("plan lookup unavailable"),
				backupBytes: 999 * GB,
			},
			wantExceeded: false,
			wantQuotaGB:  0,
		},
		{
			name: "GetUserBilling error fails safe (never blocks)",
			store: &billingFakeStore{
				billingErr: errors.New("db down"),
			},
			wantExceeded: false,
			wantQuotaGB:  0,
		},
		{
			name: "BackupBytesByOwner error still reports the quota but never blocks",
			store: &billingFakeStore{
				billing:   &store.UserBilling{R2QuotaGB: ptr(10)},
				backupErr: errors.New("r2 down"),
			},
			wantExceeded: false,
			wantUsed:     0,
			wantQuotaGB:  10,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exceeded, used, quotaBytes := R2QuotaExceeded(tc.store, "owner-1")
			if exceeded != tc.wantExceeded {
				t.Errorf("exceeded = %v, want %v", exceeded, tc.wantExceeded)
			}
			if used != tc.wantUsed {
				t.Errorf("used = %d, want %d", used, tc.wantUsed)
			}
			wantQuotaBytes := tc.wantQuotaGB * GB
			if quotaBytes != wantQuotaBytes {
				t.Errorf("quotaBytes = %d, want %d", quotaBytes, wantQuotaBytes)
			}
		})
	}
}

// --- effectiveSpec: per-user override > platform setting > built-in default ---

func TestEffectiveSpec(t *testing.T) {
	cases := []struct {
		name     string
		override string
		setting  string
		def      string
		want     string
	}{
		{"valid override wins over everything", "7d", "1m", "3d", "7d"},
		{"invalid override falls back to the platform setting", "bogus", "2w", "3d", "2w"},
		{"invalid override and invalid setting fall back to the default", "bogus", "also-bogus", "3d", "3d"},
		{"empty override and empty setting fall back to the default", "", "", "3d", "3d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &billingFakeStore{settings: map[string]string{BillingR2RetentionKey: tc.setting}}
			svc := &BillingLifecycleService{store: fs}
			if got := svc.effectiveSpec(tc.override, BillingR2RetentionKey, tc.def); got != tc.want {
				t.Errorf("effectiveSpec = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- EnterPastDue: grace-window computation + state transition ---

func TestEnterPastDue(t *testing.T) {
	t.Run("uses the per-user grace override", func(t *testing.T) {
		fs := &billingFakeStore{billing: &store.UserBilling{UserID: "u1", GracePeriod: "10d"}}
		svc := &BillingLifecycleService{store: fs}
		before := time.Now()

		if err := svc.EnterPastDue("u1"); err != nil {
			t.Fatalf("EnterPastDue: %v", err)
		}
		if len(fs.statusCalls) != 1 {
			t.Fatalf("expected 1 status call, got %d", len(fs.statusCalls))
		}
		call := fs.statusCalls[0]
		if call.userID != "u1" || call.status != "past_due" {
			t.Fatalf("call = %+v, want userID=u1 status=past_due", call)
		}
		if call.suspendedAt != nil {
			t.Fatalf("suspendedAt must stay nil on EnterPastDue, got %v", call.suspendedAt)
		}
		if call.graceUntil == nil {
			t.Fatalf("graceUntil is nil")
		}
		want := before.AddDate(0, 0, 10)
		if diff := call.graceUntil.Sub(want); diff < -time.Minute || diff > time.Minute {
			t.Fatalf("graceUntil = %v, want ~%v", call.graceUntil, want)
		}
	})

	t.Run("falls back to the built-in default grace period", func(t *testing.T) {
		fs := &billingFakeStore{billing: &store.UserBilling{UserID: "u1"}}
		svc := &BillingLifecycleService{store: fs}
		before := time.Now()

		if err := svc.EnterPastDue("u1"); err != nil {
			t.Fatalf("EnterPastDue: %v", err)
		}
		call := fs.statusCalls[0]
		want := before.AddDate(0, 0, 3) // DefaultGracePeriod = "3d"
		if diff := call.graceUntil.Sub(want); diff < -time.Minute || diff > time.Minute {
			t.Fatalf("graceUntil = %v, want ~%v", call.graceUntil, want)
		}
	})

	t.Run("a store read error propagates and nothing is written", func(t *testing.T) {
		fs := &billingFakeStore{billingErr: errors.New("db down")}
		svc := &BillingLifecycleService{store: fs}

		if err := svc.EnterPastDue("u1"); err == nil {
			t.Fatalf("expected an error")
		}
		if len(fs.statusCalls) != 0 {
			t.Fatalf("expected no status call on error, got %d", len(fs.statusCalls))
		}
	})
}

// --- Suspend / SuspendNow / Reactivate: state transitions ---

func TestSuspend(t *testing.T) {
	fs := &billingFakeStore{}
	svc := &BillingLifecycleService{store: fs}
	before := time.Now()

	if err := svc.Suspend(context.Background(), "u1"); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if len(fs.statusCalls) != 1 {
		t.Fatalf("expected 1 status call, got %d", len(fs.statusCalls))
	}
	call := fs.statusCalls[0]
	if call.status != "suspended" || call.graceUntil != nil {
		t.Fatalf("call = %+v, want status=suspended graceUntil=nil", call)
	}
	if call.suspendedAt == nil || call.suspendedAt.Before(before) {
		t.Fatalf("suspendedAt = %v, want >= %v", call.suspendedAt, before)
	}
}

// TestSuspendNow_SoloMode_SkipsLinkRevocation: with no gateway/provisioner/redis
// wired (solo/hoster mode), SuspendNow must still stop the tenant's servers
// synchronously but must not attempt link revocation. If the nil-guard were
// missing this would panic on the nil GatewayProvider/redis.Client.
func TestSuspendNow_SoloMode_SkipsLinkRevocation(t *testing.T) {
	fs := &billingFakeStore{servers: map[string][]models.Server{}}
	svc := &BillingLifecycleService{store: fs}

	if err := svc.SuspendNow(context.Background(), "u1"); err != nil {
		t.Fatalf("SuspendNow: %v", err)
	}
	if len(fs.statusCalls) != 1 || fs.statusCalls[0].status != "suspended" {
		t.Fatalf("status calls = %+v", fs.statusCalls)
	}
	if len(fs.listServersCalls) != 1 || fs.listServersCalls[0] != "u1" {
		t.Fatalf("expected stopTenantServers to query servers for u1, got %v", fs.listServersCalls)
	}
}

func TestReactivate(t *testing.T) {
	fs := &billingFakeStore{}
	svc := &BillingLifecycleService{store: fs}

	if err := svc.Reactivate("u1"); err != nil {
		t.Fatalf("Reactivate: %v", err)
	}
	if len(fs.statusCalls) != 1 {
		t.Fatalf("expected 1 status call, got %d", len(fs.statusCalls))
	}
	call := fs.statusCalls[0]
	if call.status != "active" || call.graceUntil != nil || call.suspendedAt != nil {
		t.Fatalf("call = %+v, want status=active graceUntil=nil suspendedAt=nil", call)
	}
}

// --- enforceSuspensions: hard-cutoff boundary at suspended_at + suspendGrace ---
//
// This exercises the real time.Now() read inside enforceSuspensions (there is
// no injected clock). The boundary timestamps below are offset by minutes/hours
// from "now" captured at table-construction time, so the test stays
// deterministic: normal test execution never takes long enough to cross a
// minute-wide margin. "Enforced" is observed indirectly via
// stopTenantServers's ListServersByOwner call, since that is the first store
// read on the cutoff path.
func TestEnforceSuspensions_GraceBoundary(t *testing.T) {
	const grace = time.Hour
	cases := []struct {
		name         string
		suspendedAt  *time.Time
		wantEnforced bool
	}{
		{"nil suspended_at is never enforced", nil, false},
		{"well within grace", timePtr(time.Now().Add(-10 * time.Minute)), false},
		{"just before the deadline", timePtr(time.Now().Add(-grace + time.Minute)), false},
		{"just after the deadline", timePtr(time.Now().Add(-grace - time.Minute)), true},
		{"well past the deadline", timePtr(time.Now().Add(-24 * time.Hour)), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &billingFakeStore{
				suspended: []store.UserBilling{{UserID: "u1", SuspendedAt: tc.suspendedAt}},
			}
			svc := &BillingLifecycleService{store: fs, suspendGrace: grace}

			svc.enforceSuspensions(context.Background())

			enforced := len(fs.listServersCalls) == 1
			if enforced != tc.wantEnforced {
				t.Errorf("enforced = %v, want %v (listServersCalls=%v)", enforced, tc.wantEnforced, fs.listServersCalls)
			}
		})
	}
}

// --- cleanupExpiredR2: retention boundary measured from suspended_at ---
//
// Same real-clock-boundary technique as TestEnforceSuspensions_GraceBoundary.
// Uses a 1-day per-tenant override so the boundary math stays simple (the
// built-in default is 3 months).
func TestCleanupExpiredR2_RetentionBoundary(t *testing.T) {
	cases := []struct {
		name        string
		suspendedAt *time.Time
		wantCleaned bool
	}{
		{"nil suspended_at is never cleaned", nil, false},
		{"well within the 1d retention", timePtr(time.Now().Add(-1 * time.Hour)), false},
		{"just before the 1d deadline", timePtr(time.Now().Add(-24*time.Hour + time.Minute)), false},
		{"just after the 1d deadline", timePtr(time.Now().Add(-24*time.Hour - time.Minute)), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &billingFakeStore{
				suspended: []store.UserBilling{{UserID: "u1", SuspendedAt: tc.suspendedAt, R2Retention: "1d"}},
			}
			svc := &BillingLifecycleService{store: fs}

			svc.cleanupExpiredR2(context.Background())

			cleaned := len(fs.listBackupsCalls) == 1
			if cleaned != tc.wantCleaned {
				t.Errorf("cleaned = %v, want %v (listBackupsCalls=%v)", cleaned, tc.wantCleaned, fs.listBackupsCalls)
			}
		})
	}
}

// --- runOnce: past_due -> suspended promotion at the grace deadline ---

func TestRunOnce_PastDueGraceBoundary(t *testing.T) {
	cases := []struct {
		name          string
		graceUntil    *time.Time
		wantSuspended bool
	}{
		{"nil grace_until is never promoted", nil, false},
		{"grace not yet elapsed", timePtr(time.Now().Add(time.Hour)), false},
		{"grace just elapsed", timePtr(time.Now().Add(-time.Minute)), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &billingFakeStore{
				pastDue: []store.UserBilling{{UserID: "u1", GraceUntil: tc.graceUntil}},
			}
			svc := &BillingLifecycleService{store: fs}

			svc.runOnce(context.Background())

			gotSuspended := false
			for _, c := range fs.statusCalls {
				if c.userID == "u1" && c.status == "suspended" {
					gotSuspended = true
				}
			}
			if gotSuspended != tc.wantSuspended {
				t.Errorf("suspended = %v, want %v (statusCalls=%+v)", gotSuspended, tc.wantSuspended, fs.statusCalls)
			}
		})
	}
}

// --- warp tunnel teardown at the hard cutoff ---

type fakeWarpDisconnector struct {
	calls   []int
	removed int
}

func (f *fakeWarpDisconnector) DisconnectKeyPeers(_ context.Context, keyID int) int {
	f.calls = append(f.calls, keyID)
	return f.removed
}

func (f *billingFakeStore) ListWarpAPIKeysByOwner(owner string) ([]store.WarpAPIKey, error) {
	f.warpKeyOwners = append(f.warpKeyOwners, owner)
	return f.warpKeys, f.warpKeysErr
}

// Taking away what the tunnel carries is not the same as taking away the
// tunnel: before this, a hard-suspended tenant kept a working overlay peer
// indefinitely, because nothing on the enforcement path touched warp.
func TestEnforceSuspensions_DropsWarpPeersPastGrace(t *testing.T) {
	const grace = time.Hour
	cases := []struct {
		name        string
		suspendedAt *time.Time
		wantKeys    []int
	}{
		{"within the grace the tunnel stays up", timePtr(time.Now().Add(-10 * time.Minute)), nil},
		{"past the grace every key of the tenant is disconnected", timePtr(time.Now().Add(-grace - time.Minute)), []int{7, 9}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &billingFakeStore{
				suspended: []store.UserBilling{{UserID: "u1", SuspendedAt: tc.suspendedAt}},
				warpKeys:  []store.WarpAPIKey{{ID: 7}, {ID: 9}},
			}
			warp := &fakeWarpDisconnector{removed: 1}
			svc := &BillingLifecycleService{store: fs, suspendGrace: grace, warpPeers: warp}

			svc.enforceSuspensions(context.Background())

			if len(warp.calls) != len(tc.wantKeys) {
				t.Fatalf("disconnected keys %v, want %v", warp.calls, tc.wantKeys)
			}
			for i, want := range tc.wantKeys {
				if warp.calls[i] != want {
					t.Errorf("call %d = key %d, want %d", i, warp.calls[i], want)
				}
			}
		})
	}
}

// A deployment with no overlay has no tunnel to drop, and must not panic trying.
func TestEnforceSuspensions_WithoutWarpWiring(t *testing.T) {
	fs := &billingFakeStore{
		suspended: []store.UserBilling{{UserID: "u1", SuspendedAt: timePtr(time.Now().Add(-24 * time.Hour))}},
		warpKeys:  []store.WarpAPIKey{{ID: 1}},
	}
	svc := &BillingLifecycleService{store: fs, suspendGrace: time.Hour}

	svc.enforceSuspensions(context.Background())

	if len(fs.warpKeyOwners) != 0 {
		t.Errorf("looked up warp keys with no disconnector wired: %v", fs.warpKeyOwners)
	}
}

// The pass runs hourly and a cut-off tenant stays cut off, so it re-runs against
// the same tenant forever. It has to stay a no-op rather than log or act again.
func TestEnforceSuspensions_WarpTeardownIsIdempotent(t *testing.T) {
	fs := &billingFakeStore{
		suspended: []store.UserBilling{{UserID: "u1", SuspendedAt: timePtr(time.Now().Add(-24 * time.Hour))}},
		warpKeys:  []store.WarpAPIKey{{ID: 3}},
	}
	warp := &fakeWarpDisconnector{removed: 0} // already disconnected: nothing left to remove
	svc := &BillingLifecycleService{store: fs, suspendGrace: time.Hour, warpPeers: warp}

	svc.enforceSuspensions(context.Background())
	svc.enforceSuspensions(context.Background())

	if len(warp.calls) != 2 {
		t.Fatalf("expected one call per pass, got %v", warp.calls)
	}
}
