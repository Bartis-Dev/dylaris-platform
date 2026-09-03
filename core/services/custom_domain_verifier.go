package services

import (
	"context"
	"log"
	"time"

	"dylaris-core/pkg/leader"
	"dylaris-core/store"
)

// Verification cadence, from the pinned spec: a tenant gets four hours to point
// their domain at us, and we look every thirty minutes.
//
// The poll interval is well inside the grant, so a customer who sets the record
// correctly is verified long before the deadline and never sees it. The deadline
// is what stops an unproven claim from sitting on a domain forever.
const (
	CustomDomainGrant       = 4 * time.Hour
	CustomDomainPollEvery   = 30 * time.Minute
	customDomainProofTimout = 10 * time.Second
)

// RouteRemover deletes the routes a user holds on a domain they failed to prove.
// Implemented by the gateway route service; an interface so the verifier does
// not drag the whole handler package in.
type RouteRemover interface {
	DeleteRoutesForDomain(ctx context.Context, userID, domain string) error
}

// CustomDomainVerifier proves (or fails) pending custom-domain claims.
type CustomDomainVerifier struct {
	store    store.Store
	resolver DomainResolver
	routes   RouteRemover
	// targets returns the CNAME targets and edge addresses a correctly
	// configured domain may point at. A function, not a snapshot: an operator
	// can add an edge while this is running.
	targets func() (cnameTargets, edgeAddrs []string)

	// leader gates each pass, like every other Core singleton. Nil means
	// ungated, which is what the tests want.
	leader leader.Election
}

// SetLeader gates the poll loop so only one Core acts on a claim.
//
// The gate used to be a one-shot `if coreLeader.IsLeader()` around Start in
// main.go, and leadership is not a boot-time fact. Two ways that lost the
// verifier entirely: the lease is acquired by a goroutine started ~200 lines
// earlier, so a follower - or, if Redis was slow, BOTH replicas - simply never
// started it; and after any failover the new leader had not started it at ITS
// boot either. Nothing logged, nothing errored. Claims just stopped being
// looked at, sitting pending until someone noticed their domain never verified.
func (v *CustomDomainVerifier) SetLeader(l leader.Election) { v.leader = l }

func (v *CustomDomainVerifier) shouldRun() bool {
	return v.leader == nil || v.leader.IsLeader()
}

// NewCustomDomainVerifier wires the verifier.
func NewCustomDomainVerifier(st store.Store, res DomainResolver, routes RouteRemover,
	targets func() ([]string, []string)) *CustomDomainVerifier {
	return &CustomDomainVerifier{store: st, resolver: res, routes: routes, targets: targets}
}

// Start runs the poll loop until ctx is cancelled.
func (v *CustomDomainVerifier) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(CustomDomainPollEvery)
		defer t.Stop()
		// One pass immediately: after a Core restart a claim could otherwise sit
		// unproven for a whole interval even though its DNS has been right for
		// hours. Skipped on a follower, and on a leader that has not acquired
		// its lease yet - the next tick picks it up either way.
		if v.shouldRun() {
			v.RunOnce(ctx)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !v.shouldRun() {
					continue
				}
				v.RunOnce(ctx)
			}
		}
	}()
}

// RunOnce proves what can be proven, then fails what has run out of time.
//
// Order matters: a claim whose DNS went live minutes before its deadline must be
// verified rather than punished for the timing of this tick.
func (v *CustomDomainVerifier) RunOnce(ctx context.Context) {
	pending, err := v.store.ListPendingClaims()
	if err != nil {
		log.Printf("custom-domain verifier: list pending: %v", err)
		return
	}
	if len(pending) == 0 {
		return
	}
	cnameTargets, edgeAddrs := v.targets()

	stillPending := make([]store.CustomDomainClaim, 0, len(pending))
	for _, c := range pending {
		pctx, cancel := context.WithTimeout(ctx, customDomainProofTimout)
		ok := CheckDomainPointsAtUs(pctx, v.resolver, c.Domain, cnameTargets, edgeAddrs)
		cancel()
		if ok {
			if err := v.store.MarkCustomDomainVerified(c.ID); err != nil {
				log.Printf("custom-domain verifier: mark %s verified: %v", c.Domain, err)
				continue
			}
			log.Printf("custom-domain verifier: %s proven for user %s", c.Domain, c.UserID)
			continue
		}
		stillPending = append(stillPending, c)
	}

	now := time.Now()
	for _, c := range stillPending {
		if c.DeadlineAt == nil || now.Before(*c.DeadlineAt) {
			continue // still inside the grant
		}
		// The route goes first. Leaving it up while the claim is blocked would
		// keep serving a domain nobody has shown they own.
		if v.routes != nil {
			if derr := v.routes.DeleteRoutesForDomain(ctx, c.UserID, c.Domain); derr != nil {
				log.Printf("custom-domain verifier: remove routes for %s: %v", c.Domain, derr)
			}
		}
		state, ferr := v.store.FailCustomDomainClaim(c.ID)
		if ferr != nil {
			logErrf("custom-domain-verifier", "fail %s: %v", c.Domain, ferr)
			continue
		}
		log.Printf("custom-domain verifier: %s not proven in time for user %s -> %s",
			c.Domain, c.UserID, state)
	}
}
