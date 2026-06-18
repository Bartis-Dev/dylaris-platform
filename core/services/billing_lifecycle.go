package services

import (
	"context"
	nodegrpc "dylaris-core/grpc"
	"dylaris-core/mailer"
	"dylaris-core/pkg/leader"
	backupstorage "dylaris-core/storage/backup"
	"dylaris-core/store"
	"fmt"
	"log"
	"strings"
	"time"
)

// Settings keys + built-in defaults for the BYON non-payment lifecycle. All are
// payment-provider-agnostic: status is set by the admin endpoint today (or a
// webhook later) and this worker progresses it.
const (
	BillingGracePeriodKey   = "billing.grace_period"
	BillingR2RetentionKey   = "billing.r2_retention"
	BillingNodeRetentionKey = "billing.node_retention"
	BillingPaymentURLKey    = "billing.payment_url"

	DefaultGracePeriod   = "3d"
	DefaultR2Retention   = "3m"
	DefaultNodeRetention = "2w"
)

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
}

func NewBillingLifecycleService(s store.Store, q *QueueService, registry *nodegrpc.Registry, frontendURL string) *BillingLifecycleService {
	return &BillingLifecycleService{store: s, queue: q, registry: registry, frontendURL: frontendURL, interval: time.Hour}
}

func (s *BillingLifecycleService) SetLeader(l leader.Election) { s.leader = l }

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

	s.cleanupExpiredR2(ctx)
	// NOTE: node-connection retention teardown (drop the warp tunnel + revoke the
	// tenant's warp peers/keys after node_retention) is handled in the Warp
	// multi-hub track, which adds the tenant-scoped warp queries + remove_peer
	// command needed to disconnect a LIVE tunnel. Deleting enroll tokens alone
	// would not drop an active tunnel, so it is intentionally not done here.
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
// servers are NOT auto-started; the owner starts them.
func (s *BillingLifecycleService) Reactivate(userID string) error {
	return s.store.SetUserBillingStatus(userID, "active", nil, nil)
}

// Suspend stops the tenant's running servers and marks them suspended. Read
// access (file browser, backups) stays; the start path is gated elsewhere. No
// data is deleted.
func (s *BillingLifecycleService) Suspend(ctx context.Context, userID string) error {
	now := time.Now()
	if err := s.store.SetUserBillingStatus(userID, "suspended", nil, &now); err != nil {
		return err
	}
	s.stopTenantServers(ctx, userID)
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
