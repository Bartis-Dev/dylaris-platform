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

// CheckNewRegistration applies the network gate then the join gate. This is a
// PEEK, not a consume: for "one-shot" mode it only reports whether a new
// registration is CURRENTLY allowed to attempt enrollment, without flipping the
// mode to "disabled" here. Consuming the one-shot slot during this pre-check
// let a garbage TCP connection (bad/empty enroll token, rejected before auth)
// burn the slot and lock out the real device - see ConsumeJoinSlot, which the
// gRPC layer calls AFTER a successful enroll instead. On denial this returns a
// stable, non-leaky reason code. Unset settings fall back to the inert defaults
// (open + allow); real DB errors propagate (fail-closed for the caller).
func (g *AdmissionGate) CheckNewRegistration(ctx context.Context, ip net.IP) (bool, string, error) {
	// The NETWORK gate is deliberately NOT evaluated here any more; it lives on
	// the warp enrol (handlers/warp.go Enroll), and CheckNetwork is what runs it.
	//
	// A BYON node reaches this gRPC endpoint through the warp tunnel, so `ip` is
	// the warp LEADER's overlay address, identical for every customer - measured
	// at 10.20.0.11 for every denial. Matching an operator's CIDR list against it
	// could only ever be all-or-nothing, which made the setting look like a
	// per-customer control while being none. The warp enrol sees the customer's
	// real address because it happens over HTTPS before the tunnel exists.
	//
	// `ip` is still accepted (and still the gRPC peer) so callers and tests keep
	// their shape, and so a future check that genuinely wants the tunnel-side
	// address has it.
	_ = ip

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
		// Peek only - admit the attempt so it can try to enroll. The slot itself
		// is consumed by ConsumeJoinSlot after a successful enroll.
		return true, "", nil
	default: // "open" or any unknown value -> open (inert default)
		return true, "", nil
	}
}

// ConsumeJoinSlot consumes the one-shot join slot after a NEW registration has
// successfully enrolled (called from grpc/server.go only once s.acl.Enroll
// succeeds - never from the CheckNewRegistration pre-check peek above).
// Re-reads the join mode: a no-op (nil error) when it is not "one-shot", so an
// "open" registration never touches the setting and a "disabled" registration
// never reaches this call in the first place (rejected by the pre-check gate
// before auth). A concurrent second enrollment that also passed the peek
// before this call lands could still slip through in a narrow window -
// accepted: one-shot mode is meant to be paired with a single admin-controlled
// enroll token, not used as a distributed-lock primitive.
func (g *AdmissionGate) ConsumeJoinSlot(ctx context.Context) error {
	mode, err := g.store.GetSetting(settingNodeJoinMode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if mode != "one-shot" {
		return nil
	}
	_, err = g.store.ConsumeOneShotJoin()
	return err
}

// CheckNetwork evaluates ONLY the network gate, against an address the caller
// vouches for. It is called from the warp enrol, which is reached over HTTPS
// before any tunnel exists and therefore sees the customer's real IP.
//
// Returns the same stable reason code the gRPC path used, so an operator reading
// logs across the two sees one vocabulary.
func (g *AdmissionGate) CheckNetwork(ctx context.Context, ip net.IP) (bool, string, error) {
	allowed, err := g.ipAllowed(ip)
	if err != nil {
		return false, "", err
	}
	if !allowed {
		return false, "admission_ip_denied", nil
	}
	return true, "", nil
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
