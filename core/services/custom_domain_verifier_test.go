package services

import (
	"context"
	"testing"
	"time"

	"dylaris-core/store"
)

// verifierFakeStore embeds store.Store (nil) so it satisfies the interface at
// compile time; only what the verifier touches is implemented.
type verifierFakeStore struct {
	store.Store
	pending  []store.CustomDomainClaim
	verified []int
	failed   []int
	nextFail string
}

func (f *verifierFakeStore) ListPendingClaims() ([]store.CustomDomainClaim, error) {
	return f.pending, nil
}
func (f *verifierFakeStore) MarkCustomDomainVerified(id int) error {
	f.verified = append(f.verified, id)
	return nil
}
func (f *verifierFakeStore) FailCustomDomainClaim(id int) (string, error) {
	f.failed = append(f.failed, id)
	if f.nextFail == "" {
		return store.ClaimBlocked, nil
	}
	return f.nextFail, nil
}

type fakeRemover struct{ removed []string }

func (r *fakeRemover) DeleteRoutesForDomain(_ context.Context, _, domain string) error {
	r.removed = append(r.removed, domain)
	return nil
}

func claim(id int, domain string, deadline time.Time) store.CustomDomainClaim {
	return store.CustomDomainClaim{ID: id, UserID: "u1", Domain: domain,
		State: store.ClaimPending, DeadlineAt: &deadline}
}

func TestVerifierProvesBeforeItPunishes(t *testing.T) {
	past := time.Now().Add(-time.Minute)

	// The case that matters: DNS went live shortly before the deadline. Failing
	// it because this tick happened to run after the deadline would punish a
	// customer who did everything right, seconds late.
	t.Run("a proven claim past its deadline is verified, not blocked", func(t *testing.T) {
		fs := &verifierFakeStore{pending: []store.CustomDomainClaim{claim(1, "mc.example.com", past)}}
		rm := &fakeRemover{}
		res := &fakeResolver{cname: map[string]string{"mc.example.com": "route.eu.dylaris.com."}}
		v := NewCustomDomainVerifier(fs, res, rm, func() ([]string, []string) {
			return []string{"route.eu.dylaris.com"}, nil
		})
		v.RunOnce(context.Background())

		if len(fs.verified) != 1 || fs.verified[0] != 1 {
			t.Errorf("claim was not verified: %v", fs.verified)
		}
		if len(fs.failed) != 0 {
			t.Errorf("a proven claim was also failed: %v", fs.failed)
		}
		if len(rm.removed) != 0 {
			t.Errorf("routes were removed for a proven claim: %v", rm.removed)
		}
	})

	t.Run("an unproven claim past its deadline loses its routes and is blocked", func(t *testing.T) {
		fs := &verifierFakeStore{pending: []store.CustomDomainClaim{claim(2, "mc.example.com", past)}}
		rm := &fakeRemover{}
		v := NewCustomDomainVerifier(fs, &fakeResolver{}, rm, func() ([]string, []string) {
			return []string{"route.eu.dylaris.com"}, nil
		})
		v.RunOnce(context.Background())

		if len(fs.failed) != 1 || fs.failed[0] != 2 {
			t.Errorf("claim was not failed: %v", fs.failed)
		}
		// The route must go BEFORE the block is recorded, or an unproven domain
		// keeps being served.
		if len(rm.removed) != 1 || rm.removed[0] != "mc.example.com" {
			t.Errorf("routes were not removed: %v", rm.removed)
		}
	})

	// Inside the grant nothing happens: no proof yet is the normal state for the
	// first few hours, not a failure.
	t.Run("an unproven claim inside its grant is left alone", func(t *testing.T) {
		future := time.Now().Add(2 * time.Hour)
		fs := &verifierFakeStore{pending: []store.CustomDomainClaim{claim(3, "mc.example.com", future)}}
		rm := &fakeRemover{}
		v := NewCustomDomainVerifier(fs, &fakeResolver{}, rm, func() ([]string, []string) {
			return []string{"route.eu.dylaris.com"}, nil
		})
		v.RunOnce(context.Background())

		if len(fs.failed) != 0 || len(rm.removed) != 0 || len(fs.verified) != 0 {
			t.Errorf("a claim inside its grant was acted on: failed=%v removed=%v verified=%v",
				fs.failed, rm.removed, fs.verified)
		}
	})

	t.Run("a claim with no deadline is never failed", func(t *testing.T) {
		fs := &verifierFakeStore{pending: []store.CustomDomainClaim{
			{ID: 4, UserID: "u1", Domain: "mc.example.com", State: store.ClaimPending},
		}}
		rm := &fakeRemover{}
		v := NewCustomDomainVerifier(fs, &fakeResolver{}, rm, func() ([]string, []string) {
			return []string{"route.eu.dylaris.com"}, nil
		})
		v.RunOnce(context.Background())
		if len(fs.failed) != 0 {
			t.Errorf("a claim without a deadline was failed: %v", fs.failed)
		}
	})
}

// The grant must be comfortably longer than the poll interval, or a customer
// who configures DNS correctly could still miss the window between two ticks.
func TestPollIntervalFitsInsideTheGrant(t *testing.T) {
	if CustomDomainPollEvery >= CustomDomainGrant {
		t.Fatalf("poll interval %v does not fit inside the grant %v",
			CustomDomainPollEvery, CustomDomainGrant)
	}
	if CustomDomainGrant/CustomDomainPollEvery < 4 {
		t.Errorf("only %d polls fit inside the grant; a customer gets too few chances",
			CustomDomainGrant/CustomDomainPollEvery)
	}
}
