package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"dylaris-core/services"
	"dylaris-core/store"
)

// customDomainGate decides whether this caller may point `domain` at us.
//
// Returns a non-nil error to refuse, with a message meant for the customer.
//
// ADMINS ARE EXEMPT ENTIRELY - not "checked and usually passing", but never
// checked. The proof exists to stop a tenant from claiming a domain they do not
// control; an operator configuring their own platform is the party the check
// protects, and making them wait four hours to point their own domain at their
// own edge would be ceremony with no threat behind it.
//
// It is also a no-op for anything that is not a custom domain: a subdomain of a
// hoster domain is already ours, so there is nothing to prove.
func (h *GatewayHandler) customDomainGate(r *http.Request, userID, domain string, isCustom bool) error {
	if !isCustom || IsAdmin(r) {
		return nil
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return nil
	}

	claim, err := h.state.Store.GetCustomDomainClaim(userID, domain)
	if err != nil && err != store.ErrNoClaim {
		// Fail CLOSED. Not knowing whether this user is blocked is not a reason
		// to assume they are not.
		return fmt.Errorf("could not check the ownership status of %s", domain)
	}

	if claim != nil {
		switch claim.State {
		case store.ClaimVerified:
			return nil
		case store.ClaimPermablocked:
			return fmt.Errorf("%s is blocked for your account after %d failed ownership checks. "+
				"To unblock it, add the TXT record shown under Custom domains in your panel",
				domain, claim.Attempts)
		}
	}

	// pending, blocked, or brand new: (re-)arm the grant. StartCustomDomainClaim
	// refuses to re-arm a permanent block, so a retry cannot launder one.
	if _, err := h.state.Store.StartCustomDomainClaim(userID, domain,
		time.Now().Add(services.CustomDomainGrant)); err != nil {
		return fmt.Errorf("could not record the ownership check for %s", domain)
	}
	return nil
}

// customDomainDeadlineHint is the one-line instruction the panel shows after a
// route on an unproven domain is created. Written here so the two route
// handlers cannot drift apart on what they promise the customer.
func customDomainDeadlineHint(cnameTarget string) string {
	if cnameTarget == "" {
		return fmt.Sprintf("Point this domain at us within %s or the route is removed.",
			formatGrant(services.CustomDomainGrant))
	}
	return fmt.Sprintf("Add a CNAME to %s (or an A record to one of our edge addresses) within %s, "+
		"or the route is removed.", cnameTarget, formatGrant(services.CustomDomainGrant))
}

func formatGrant(d time.Duration) string {
	h := int(d.Hours())
	if h == 1 {
		return "1 hour"
	}
	return fmt.Sprintf("%d hours", h)
}
