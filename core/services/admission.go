package services

import (
	"context"
	"database/sql"
	"errors"
	"net"

	"dylaris-core/store"
)

// Admission setting keys + values. Defaults are inert: open + allow.
const (
	settingNodeJoinMode        = "node_join_mode"
	settingNodeAdmissionIPMode = "node_admission_ip_mode"
)

// AdmissionGate evaluates the network + join gates for a NEW node registration.
// It is consulted ONLY on the ACL-on gRPC enroll path for unknown nodes; known
// nodes never reach it, so admission never blocks a reconnect.
type AdmissionGate struct {
	store store.Store
}

// NewAdmissionGate wires the resolver to the Core store.
func NewAdmissionGate(st store.Store) *AdmissionGate {
	return &AdmissionGate{store: st}
}

// CheckNewRegistration applies the network gate then the join gate. On denial it
// returns a stable, non-leaky reason code. Unset settings fall back to the inert
// defaults (open + allow); real DB errors propagate (fail-closed for the caller).
func (g *AdmissionGate) CheckNewRegistration(ctx context.Context, ip net.IP) (bool, string, error) {
	// Network gate.
	ipAllowed, err := g.ipAllowed(ip)
	if err != nil {
		return false, "", err
	}
	if !ipAllowed {
		return false, "admission_ip_denied", nil
	}

	// Join gate.
	mode, err := g.store.GetSetting(settingNodeJoinMode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			mode = "open"
		} else {
			return false, "", err
		}
	}
	switch mode {
	case "disabled":
		return false, "join_disabled", nil
	case "one-shot":
		won, cerr := g.store.ConsumeOneShotJoin()
		if cerr != nil {
			return false, "", cerr
		}
		if !won {
			return false, "join_disabled", nil
		}
		return true, "", nil
	default: // "open" or any unknown value -> open (inert default)
		return true, "", nil
	}
}

// ipAllowed implements the allow/deny rule: allow-mode always admits (the CIDR
// list is advisory); deny-mode admits only when ip is inside a configured CIDR.
func (g *AdmissionGate) ipAllowed(ip net.IP) (bool, error) {
	mode, err := g.store.GetSetting(settingNodeAdmissionIPMode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			mode = "allow"
		} else {
			return false, err
		}
	}
	if mode != "deny" {
		return true, nil
	}
	if ip == nil {
		return false, nil
	}
	cidrs, lerr := g.store.ListAdmissionCIDRs()
	if lerr != nil {
		return false, lerr
	}
	for _, c := range cidrs {
		_, netw, perr := net.ParseCIDR(c.CIDR)
		if perr != nil {
			continue
		}
		if netw.Contains(ip) {
			return true, nil
		}
	}
	return false, nil
}
