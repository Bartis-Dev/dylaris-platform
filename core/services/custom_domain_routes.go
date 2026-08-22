package services

import (
	"context"
	"strings"

	"github.com/redis/go-redis/v9"
)

// RouteDeleter is the slice of the gateway provider the remover needs.
// Satisfied structurally by GatewayProvider.
type RouteDeleter interface {
	DeleteRoute(domain string) error
}

// customDomainRouteRemover drops the routes a user holds on a domain they
// failed to prove they own.
type customDomainRouteRemover struct {
	redis *redis.Client
	gw    RouteDeleter
}

// NewCustomDomainRouteRemover wires the verifier's route teardown.
func NewCustomDomainRouteRemover(rdb *redis.Client, gw RouteDeleter) RouteRemover {
	return &customDomainRouteRemover{redis: rdb, gw: gw}
}

// DeleteRoutesForDomain removes the domain itself and anything under it that
// belongs to this user.
//
// Scoped to the OWNER, not to the domain alone: two tenants holding routes under
// one name should not be possible, but if it ever is, one tenant failing a check
// must not tear down the other's routes. Same reasoning as the block being per
// (user, domain) rather than global.
//
// A route with no OwnerID is a server-bound (managed) route, which cannot be on
// a tenant's custom domain, so it is left alone rather than matched by suffix.
func (r *customDomainRouteRemover) DeleteRoutesForDomain(ctx context.Context, userID, domain string) error {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" || userID == "" {
		return nil
	}
	var firstErr error
	for _, rt := range GetRoutesFromRedis(ctx, r.redis) {
		if rt.OwnerID != userID {
			continue
		}
		d := strings.ToLower(rt.Domain)
		if d != domain && !strings.HasSuffix(d, "."+domain) {
			continue
		}
		if derr := r.gw.DeleteRoute(rt.Domain); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}
	return firstErr
}
