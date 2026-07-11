package services

import (
	"context"
	nodegrpc "dylaris-core/grpc"
	"dylaris-core/mailer"
	"dylaris-core/pkg/leader"
	"dylaris-core/services/redisacl"
	backupstorage "dylaris-core/storage/backup"
	"dylaris-core/store"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Settings keys + built-in defaults for the BYON non-payment lifecycle. All are
// payment-provider-agnostic: status is set by the admin endpoint today (or a
// webhook later) and this worker progresses it.
const (
	BillingGracePeriodKey   = "billing.grace_period"
	BillingR2RetentionKey   = "billing.r2_retention"
	BillingNodeRetentionKey = "billing.node_retention"
	BillingPaymentURLKey    = "billing.payment_url"
	BillingR2QuotaKey       = "billing.r2_quota_gb" // platform default, 0 = unlimited

	DefaultGracePeriod   = "3d"
	DefaultR2Retention   = "3m"
	DefaultNodeRetention = "2w"
)

// R2QuotaExceeded reports whether a tenant is at or over their R2 backup quota.
// Quota resolution (first that is set wins): per-user override
// (user_billing.r2_quota_gb), else the tenant's plan (assigned, else the default
// plan), else the platform setting (billing.r2_quota_gb), else 0 = unlimited. A
// 0/unset quota never blocks (so solo/hoster and unmetered tenants are unaffected).
func R2QuotaExceeded(st store.Store, ownerID string) (exceeded bool, usedBytes, quotaBytes int64) {
	b, err := st.GetUserBilling(ownerID)
	if err != nil {
		return false, 0, 0
	}
	quotaGB := int64(-1)
	// 1. per-user override.
	if b.R2QuotaGB != nil {
		quotaGB = *b.R2QuotaGB
	}
	// 2. the tenant's plan (assigned, else default). A plan's value — including an
	// explicit 0 (unlimited) — is authoritative, so we do not fall through to the
	// platform setting once a plan is found.
	if quotaGB < 0 {
		if pid, perr := st.GetUserPlanID(ownerID); perr == nil {
			var plan *store.Plan
			if pid != nil {
				plan, _ = st.GetPlan(*pid)
			}
			if plan == nil {
				plan, _ = st.GetDefaultPlan()
			}
			if plan != nil {
				quotaGB = plan.R2QuotaGB
			}
		}
	}
	// 3. the platform setting (pre-plan default).
	if quotaGB < 0 {
		if v, _ := st.GetSetting(BillingR2QuotaKey); v != "" {
			if n, perr := strconv.ParseInt(v, 10, 64); perr == nil {
				quotaGB = n
			}
		}
	}
	if quotaGB <= 0 {
		return false, 0, 0 // unlimited / unset
	}
	quotaBytes = quotaGB * 1024 * 1024 * 1024
	used, err := st.BackupBytesByOwner(ownerID)
	if err != nil {
		return false, 0, quotaBytes
	}
	return used >= quotaBytes, used, quotaBytes
}

// BillingLifecycleService runs the non-payment lifecycle: past_due (grace, all
// running) -> suspended (services stopped, read access kept) -> retention cleanup
// (next chunk removes node connection + R2 backups after their windows). It NEVER
// deletes a user or their DB rows. Leader-gated so only one Core acts.
type BillingLifecycleService struct {
	store       store.Store
	queue       *QueueService
	registry    *nodegrpc.Registry // for backupstorage.Deps (node-local deletes)
	frontendURL string
	leader      leader.Election
	interval    time.Duration

	// Route-only link teardown/restore deps, wired after the ACL provisioner is
	// built (SetLinkACL). Nil in solo/hoster mode, where there are no link kits.
	gateway       GatewayProvider
	redis         *redis.Client
	provisioner   *redisacl.Provisioner
	clusterSecret string

	// suspendGrace defers the hard cutoff until suspended_at + suspendGrace has
	// elapsed (see enforceSuspensions). Threaded from cfg.SuspendGrace at
	// construction, like frontendURL.
	suspendGrace time.Duration
}

func NewBillingLifecycleService(s store.Store, q *QueueService, registry *nodegrpc.Registry, frontendURL string, suspendGrace time.Duration) *BillingLifecycleService {
	return &BillingLifecycleService{store: s, queue: q, registry: registry, frontendURL: frontendURL, interval: time.Hour, suspendGrace: suspendGrace}
}

func (s *BillingLifecycleService) SetLeader(l leader.Election) { s.leader = l }

// SetLinkACL wires the route-only link teardown/restore hooks. Called once at
// startup after the ACL provisioner and gateway exist. When any dependency is nil
// (solo/hoster mode) Suspend/Reactivate skip the link steps.
func (s *BillingLifecycleService) SetLinkACL(gw GatewayProvider, rdb *redis.Client, prov *redisacl.Provisioner, clusterSecret string) {
	s.gateway = gw
	s.redis = rdb
	s.provisioner = prov
	s.clusterSecret = clusterSecret
}

func (s *BillingLifecycleService) Start(ctx context.Context) {
	log.Println("Billing lifecycle service started")
	ticker := time.NewTicker(s.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.leader != nil && !s.leader.IsLeader() {
					continue
				}
				s.runOnce(ctx)
			}
		}
	}()
}

// runOnce progresses past_due tenants whose grace window has elapsed into
// suspended. Retention cleanup of suspended tenants is a separate pass added in
// the next chunk.
func (s *BillingLifecycleService) runOnce(ctx context.Context) {
	pastDue, err := s.store.ListUserBillingByStatus("past_due")
	if err != nil {
		log.Printf("billing lifecycle: list past_due: %v", err)
		return
	}
	now := time.Now()
	for _, b := range pastDue {
		if b.GraceUntil == nil || now.Before(*b.GraceUntil) {
			continue
		}
		if err := s.Suspend(ctx, b.UserID); err != nil {
			log.Printf("billing lifecycle: suspend %s: %v", b.UserID, err)
		}
	}

	// Deferred hard cutoff: enforce suspensions whose grace has elapsed. Separate
	// from the past_due->suspended promotion above so a tenant suspended this pass
	// still gets the full grace before anything is cut.
	s.enforceSuspensions(ctx)

	s.cleanupExpiredR2(ctx)
	// NOTE: node-connection retention teardown (drop the warp tunnel + revoke the
	// tenant's warp peers/keys after node_retention) is handled in the Warp
	// multi-hub track, which adds the tenant-scoped warp queries + remove_peer
	// command needed to disconnect a LIVE tunnel. Deleting enroll tokens alone
	// would not drop an active tunnel, so it is intentionally not done here.
}

// enforceSuspensions applies the hard cutoff to tenants whose suspension has
// persisted past the grace window (suspended_at + suspendGrace <= now): it stops
// their running servers and drops their route-only link ACLs. Suspend() only
// records the suspended state; the cutoff lands here so a transient billing fault
// cannot instantly kick a paying customer. Idempotent - stopping an already
// stopped server and DELUSER/DEL on absent keys are no-ops - so re-running every
// hour is harmless and needs no "enforced" flag. Leader-gated via runOnce.
func (s *BillingLifecycleService) enforceSuspensions(ctx context.Context) {
	suspended, err := s.store.ListUserBillingByStatus("suspended")
	if err != nil {
		log.Printf("billing lifecycle: list suspended for enforcement: %v", err)
		return
	}
	now := time.Now()
	for _, b := range suspended {
		if b.SuspendedAt == nil || now.Before(b.SuspendedAt.Add(s.suspendGrace)) {
			continue
		}
		// One line per enforced tenant per pass. Suspended tenants are few, so the
		// hourly repeat is acceptable; no state is added just to silence it.
		log.Printf("billing lifecycle: enforcing suspension cutoff for %s (suspended_at=%s, grace=%s)",
			b.UserID, b.SuspendedAt.UTC().Format(time.RFC3339), s.suspendGrace)
		s.stopTenantServers(ctx, b.UserID)
		s.suspendTenantLinks(ctx, b.UserID)
	}
}

// cleanupExpiredR2 deletes the R2 backups of suspended tenants whose r2_retention
// window has elapsed (measured from suspended_at). The user account + server
// metadata are never touched — only the backup objects + their rows.
func (s *BillingLifecycleService) cleanupExpiredR2(ctx context.Context) {
	suspended, err := s.store.ListUserBillingByStatus("suspended")
	if err != nil {
		log.Printf("billing lifecycle: list suspended: %v", err)
		return
	}
	now := time.Now()
	for _, b := range suspended {
		if b.SuspendedAt == nil {
			continue
		}
		spec := s.effectiveSpec(b.R2Retention, BillingR2RetentionKey, DefaultR2Retention)
		deadline, ok := AddRetention(*b.SuspendedAt, spec)
		if !ok || now.Before(deadline) {
			continue
		}
		s.deleteTenantBackups(ctx, b.UserID)
	}
}

func (s *BillingLifecycleService) deleteTenantBackups(ctx context.Context, userID string) {
	refs, err := s.store.ListBackupRunsByOwner(userID)
	if err != nil {
		log.Printf("billing lifecycle: list backups for %s: %v", userID, err)
		return
	}
	deps := backupstorage.Deps{Registry: s.registry, NodeStore: s.store}
	for _, ref := range refs {
		if ref.StorageID != nil {
			if storage, err := s.store.GetBackupStorage(*ref.StorageID); err == nil && storage != nil {
				if provider, err := backupstorage.Open(ctx, storage, deps); err == nil {
					if err := provider.Delete(ctx, ref.StorageKey); err != nil {
						log.Printf("billing lifecycle: delete backup object %s: %v", ref.StorageKey, err)
					}
				}
			}
		}
		if err := s.store.DeleteBackupRun(ref.RunID); err != nil {
			log.Printf("billing lifecycle: delete backup run %d: %v", ref.RunID, err)
		}
	}
}

// effectiveSpec resolves a retention spec: per-user override wins, else the
// platform setting (if valid), else the built-in default.
func (s *BillingLifecycleService) effectiveSpec(override, settingKey, def string) string {
	if ValidRetentionSpec(override) {
		return override
	}
	if v, _ := s.store.GetSetting(settingKey); ValidRetentionSpec(v) {
		return v
	}
	return def
}

// EnterPastDue marks a tenant past_due, sets the grace deadline from the
// effective grace period, and sends the dunning email. Everything keeps running
// during grace. Re-calling resets the grace window.
func (s *BillingLifecycleService) EnterPastDue(userID string) error {
	b, err := s.store.GetUserBilling(userID)
	if err != nil {
		return err
	}
	grace := s.effectiveSpec(b.GracePeriod, BillingGracePeriodKey, DefaultGracePeriod)
	until, ok := AddRetention(time.Now(), grace)
	if !ok {
		until, _ = AddRetention(time.Now(), DefaultGracePeriod)
	}
	if err := s.store.SetUserBillingStatus(userID, "past_due", &until, nil); err != nil {
		return err
	}
	s.sendDunningEmail(userID, until)
	return nil
}

// Reactivate clears the lifecycle back to active (e.g. after payment). Stopped
// servers are NOT auto-started; the owner starts them. The tenant's route-only
// links come back on their own once their ACL + tunnel key are restored.
func (s *BillingLifecycleService) Reactivate(userID string) error {
	if err := s.store.SetUserBillingStatus(userID, "active", nil, nil); err != nil {
		return err
	}
	s.reactivateTenantLinks(userID)
	return nil
}

// Suspend marks the tenant suspended and notifies them. Read access (file
// browser, backups) stays; the start path is gated elsewhere. No data is
// deleted. The hard cutoff (stop servers, drop link ACLs) is NOT synchronous
// here - see enforceSuspensions.
func (s *BillingLifecycleService) Suspend(ctx context.Context, userID string) error {
	now := time.Now()
	if err := s.store.SetUserBillingStatus(userID, "suspended", nil, &now); err != nil {
		return err
	}
	// The hard cutoff (stop servers + drop route-only link ACLs) is deferred to the
	// enforcement pass in runOnce, which fires once suspended_at + suspendGrace has
	// elapsed. Recording state + notifying only here means a transient billing/DB
	// fault (or a buggy manual suspend) cannot instantly kick a paying customer, and
	// enforcement is single-sourced in the leader-gated ticker. ctx is retained for
	// the unchanged caller signature.
	s.sendSuspendedEmail(userID)
	return nil
}

func (s *BillingLifecycleService) stopTenantServers(ctx context.Context, userID string) {
	servers, err := s.store.ListServersByOwner(userID)
	if err != nil {
		log.Printf("billing lifecycle: list servers for %s: %v", userID, err)
		return
	}
	for _, srv := range servers {
		if srv.Status != "online" {
			continue
		}
		node, err := s.store.GetNodeByID(srv.NodeID)
		if err != nil {
			continue
		}
		if err := s.queue.SendCommand(ctx, node.Token, "stop", map[string]interface{}{"uuid": srv.UUID}, nil); err != nil {
			log.Printf("billing lifecycle: stop %s: %v", srv.UUID, err)
		}
	}
}

// --- emails (best-effort; SMTP misconfig never blocks the lifecycle) ---

// PaymentURL is the configured payment link the panel banner + emails point at.
func (s *BillingLifecycleService) PaymentURL() string { return s.paymentURL() }

func (s *BillingLifecycleService) paymentURL() string {
	v, _ := s.store.GetSetting(BillingPaymentURLKey)
	if v != "" {
		return v
	}
	return strings.TrimRight(s.frontendURL, "/") + "/account/billing"
}

func (s *BillingLifecycleService) sendDunningEmail(userID string, graceUntil time.Time) {
	u, err := s.store.GetUserByID(userID)
	if err != nil || u == nil || u.Email == "" {
		return
	}
	cfg, err := mailer.LoadConfig(s.store, "auth")
	if err != nil {
		return
	}
	body := fmt.Sprintf(`Hi %s,

We could not process the payment for your Dylaris services.

Everything keeps running for now, but your services will be SUSPENDED on %s if payment is not completed.

Pay here to keep your services active:

%s

Your data is safe either way — nothing is deleted when you miss a payment.

— Dylaris
`, u.Username, graceUntil.UTC().Format("2006-01-02 15:04 UTC"), s.paymentURL())
	if err := mailer.Send(cfg, mailer.Message{To: u.Email, Subject: "Payment required — your Dylaris services", Body: body}); err != nil {
		log.Printf("billing lifecycle: dunning mail to %s failed: %v", u.Email, err)
	}
}

func (s *BillingLifecycleService) sendSuspendedEmail(userID string) {
	u, err := s.store.GetUserByID(userID)
	if err != nil || u == nil || u.Email == "" {
		return
	}
	cfg, err := mailer.LoadConfig(s.store, "auth")
	if err != nil {
		return
	}
	body := fmt.Sprintf(`Hi %s,

Your Dylaris services have been suspended for non-payment. Your servers are stopped, but your data and backups are kept and remain viewable.

Pay here to reactivate:

%s

— Dylaris
`, u.Username, s.paymentURL())
	if err := mailer.Send(cfg, mailer.Message{To: u.Email, Subject: "Your Dylaris services are suspended", Body: body}); err != nil {
		log.Printf("billing lifecycle: suspended mail to %s failed: %v", u.Email, err)
	}
}

// linkKitsForUser returns the tenant's non-revoked route-only link identities.
func (s *BillingLifecycleService) linkKitsForUser(userID string) []string {
	keys, err := s.store.ListWarpAPIKeysByOwner(userID)
	if err != nil {
		log.Printf("billing lifecycle: list link kits for %s: %v", userID, err)
		return nil
	}
	var ids []string
	for _, k := range keys {
		if strings.HasPrefix(k.NodeID, "link-") {
			ids = append(ids, k.NodeID)
		}
	}
	return ids
}

// suspendTenantLinks drops each of the tenant's route-only links (ACL user +
// tunnel key), killing their live tunnels. It does NOT revoke the warp key or
// delete routes, so Reactivate can bring the same links back.
func (s *BillingLifecycleService) suspendTenantLinks(ctx context.Context, userID string) {
	if s.provisioner == nil || s.gateway == nil || s.redis == nil {
		return
	}
	// The hourly ticker calls Suspend with context.Background(); a hung Redis call
	// would stall that goroutine indefinitely. Bound it, as reactivate already does.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, linkID := range s.linkKitsForUser(userID) {
		tunnelToken := s.gateway.LinkToken(linkID)
		s.provisioner.RemoveRouteOnlyLinkACL(ctx, linkID)
		if err := s.redis.Del(ctx, "link:"+tunnelToken).Err(); err != nil {
			log.Printf("billing lifecycle: suspend link %s: delete tunnel key: %v", linkID, err)
		}
	}
}

// reactivateTenantLinks restores each link's scoped Redis ACL (identical derived
// password, so the link's pool re-authenticates on its next reconnect) and re-adds
// its tunnel key, so the edge accepts the tunnel again within seconds. No restart
// needed: the link's ConnectionManager retries on its own.
func (s *BillingLifecycleService) reactivateTenantLinks(userID string) {
	if s.provisioner == nil || s.gateway == nil || s.redis == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, linkID := range s.linkKitsForUser(userID) {
		tunnelToken := s.gateway.LinkToken(linkID)
		if _, _, err := s.provisioner.EnsureRouteOnlyLinkACL(ctx, s.clusterSecret, linkID, tunnelToken); err != nil {
			log.Printf("billing lifecycle: reactivate link %s: ensure ACL: %v", linkID, err)
			continue
		}
		if err := s.redis.Set(ctx, "link:"+tunnelToken, "valid", 24*time.Hour).Err(); err != nil {
			log.Printf("billing lifecycle: reactivate link %s: set tunnel key: %v", linkID, err)
		}
	}
}
