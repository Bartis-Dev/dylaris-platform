package services

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"testing"

	"dylaris-core/store"
)

// admissionFakeStore embeds store.Store (nil) so it satisfies the full interface
// at compile time; only the methods AdmissionGate touches are overridden. Any
// other call would panic - these tests never make one.
//
// GetSetting models the real store contract the gate relies on: a missing key
// returns sql.ErrNoRows (the gate then falls back to its inert default), while a
// real error propagates (fail-closed). settingErr allows per-key error injection
// so a join-mode DB error can be tested while the IP gate still passes.
type admissionFakeStore struct {
	store.Store
	settings      map[string]string
	settingErr    map[string]error
	cidrs         []store.AdmissionCIDR
	cidrsErr      error
	consumeCalled int
	consumeWon    bool
	consumeErr    error
}

func (f *admissionFakeStore) GetSetting(key string) (string, error) {
	if f.settingErr != nil {
		if e, ok := f.settingErr[key]; ok {
			return "", e
		}
	}
	if v, ok := f.settings[key]; ok {
		return v, nil
	}
	return "", sql.ErrNoRows
}

func (f *admissionFakeStore) ListAdmissionCIDRs() ([]store.AdmissionCIDR, error) {
	return f.cidrs, f.cidrsErr
}

func (f *admissionFakeStore) ConsumeOneShotJoin() (bool, error) {
	f.consumeCalled++
	return f.consumeWon, f.consumeErr
}

func cidrList(cidrs ...string) []store.AdmissionCIDR {
	out := make([]store.AdmissionCIDR, len(cidrs))
	for i, c := range cidrs {
		out[i] = store.AdmissionCIDR{CIDR: c}
	}
	return out
}

func TestIPAllowed(t *testing.T) {
	cases := []struct {
		name        string
		ipMode      string // "" = setting unset (store returns sql.ErrNoRows)
		ipModeErr   error
		cidrs       []store.AdmissionCIDR
		cidrsErr    error
		ip          net.IP
		wantAllowed bool
		wantErr     bool
	}{
		{
			name:        "unset mode defaults to allow",
			ip:          net.ParseIP("203.0.113.7"),
			wantAllowed: true,
		},
		{
			name:        "explicit allow admits any ip (cidrs advisory)",
			ipMode:      "allow",
			ip:          net.ParseIP("203.0.113.7"),
			wantAllowed: true,
		},
		{
			name:        "deny mode: ip inside a configured cidr is allowed",
			ipMode:      "deny",
			cidrs:       cidrList("10.0.0.0/8"),
			ip:          net.ParseIP("10.1.2.3"),
			wantAllowed: true,
		},
		{
			name:        "deny mode: ip outside all cidrs is denied",
			ipMode:      "deny",
			cidrs:       cidrList("10.0.0.0/8"),
			ip:          net.ParseIP("192.168.1.1"),
			wantAllowed: false,
		},
		{
			name:        "deny mode: empty cidr list denies",
			ipMode:      "deny",
			cidrs:       nil,
			ip:          net.ParseIP("10.1.2.3"),
			wantAllowed: false,
		},
		{
			name:        "deny mode: nil ip is denied",
			ipMode:      "deny",
			cidrs:       cidrList("10.0.0.0/8"),
			ip:          nil,
			wantAllowed: false,
		},
		{
			name:        "deny mode: malformed cidr skipped, valid one still matches",
			ipMode:      "deny",
			cidrs:       cidrList("not-a-cidr", "10.0.0.0/8"),
			ip:          net.ParseIP("10.1.2.3"),
			wantAllowed: true,
		},
		{
			name:        "deny mode: only a malformed cidr denies",
			ipMode:      "deny",
			cidrs:       cidrList("not-a-cidr"),
			ip:          net.ParseIP("10.1.2.3"),
			wantAllowed: false,
		},
		{
			name:      "real GetSetting error propagates (fail-closed)",
			ipModeErr: errors.New("db down"),
			ip:        net.ParseIP("10.1.2.3"),
			wantErr:   true,
		},
		{
			name:     "deny mode: ListAdmissionCIDRs error propagates",
			ipMode:   "deny",
			cidrsErr: errors.New("db down"),
			ip:       net.ParseIP("10.1.2.3"),
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &admissionFakeStore{
				settings:   map[string]string{},
				settingErr: map[string]error{},
				cidrs:      tc.cidrs,
				cidrsErr:   tc.cidrsErr,
			}
			if tc.ipMode != "" {
				fs.settings[settingNodeAdmissionIPMode] = tc.ipMode
			}
			if tc.ipModeErr != nil {
				fs.settingErr[settingNodeAdmissionIPMode] = tc.ipModeErr
			}
			g := NewAdmissionGate(fs)

			allowed, err := g.ipAllowed(tc.ip)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if allowed != tc.wantAllowed {
				t.Fatalf("allowed = %v, want %v", allowed, tc.wantAllowed)
			}
		})
	}
}

func TestCheckNewRegistration(t *testing.T) {
	cases := []struct {
		name        string
		ipMode      string
		joinMode    string
		ipModeErr   error
		joinModeErr error
		cidrs       []store.AdmissionCIDR
		ip          net.IP
		wantOK      bool
		wantReason  string
		wantErr     bool
	}{
		{
			// The NETWORK gate no longer runs here - it moved to the warp enrol,
			// which is the only place a BYON customer's real IP is visible. Over
			// gRPC the peer is the warp leader, so an allowlist evaluated here
			// could only ever be all-or-nothing. Denying the IP must therefore
			// NOT block the join gate any more; TestCheckNetwork covers the gate
			// itself.
			name:   "ip denied no longer blocks here (network gate moved to warp enrol)",
			ipMode: "deny",
			cidrs:  nil,
			ip:     net.ParseIP("203.0.113.7"),
			wantOK: true,
		},
		{
			name:   "ip allowed + join unset defaults open",
			ip:     net.ParseIP("203.0.113.7"),
			wantOK: true,
		},
		{
			name:     "ip allowed + join open",
			joinMode: "open",
			ip:       net.ParseIP("203.0.113.7"),
			wantOK:   true,
		},
		{
			name:       "ip allowed + join disabled",
			joinMode:   "disabled",
			ip:         net.ParseIP("203.0.113.7"),
			wantOK:     false,
			wantReason: "join_disabled",
		},
		{
			name:     "ip allowed + join one-shot is a peek (admits, no consume)",
			joinMode: "one-shot",
			ip:       net.ParseIP("203.0.113.7"),
			wantOK:   true,
		},
		{
			name:     "ip allowed + unknown join mode defaults open",
			joinMode: "banana",
			ip:       net.ParseIP("203.0.113.7"),
			wantOK:   true,
		},
		{
			// Same move: this path no longer reads the ip-mode setting at all, so
			// a fault reading it cannot fail the join check. TestCheckNetwork
			// pins that CheckNetwork still fails closed on that error.
			name:      "ip-mode DB error is irrelevant here",
			ipModeErr: errors.New("db down"),
			ip:        net.ParseIP("203.0.113.7"),
			wantOK:    true,
		},
		{
			name:        "join-mode DB error propagates (ip gate passed)",
			ipMode:      "allow",
			joinModeErr: errors.New("db down"),
			ip:          net.ParseIP("203.0.113.7"),
			wantErr:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &admissionFakeStore{
				settings:   map[string]string{},
				settingErr: map[string]error{},
				cidrs:      tc.cidrs,
			}
			if tc.ipMode != "" {
				fs.settings[settingNodeAdmissionIPMode] = tc.ipMode
			}
			if tc.joinMode != "" {
				fs.settings[settingNodeJoinMode] = tc.joinMode
			}
			if tc.ipModeErr != nil {
				fs.settingErr[settingNodeAdmissionIPMode] = tc.ipModeErr
			}
			if tc.joinModeErr != nil {
				fs.settingErr[settingNodeJoinMode] = tc.joinModeErr
			}
			g := NewAdmissionGate(fs)

			ok, reason, err := g.CheckNewRegistration(context.Background(), tc.ip)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (reason=%q)", ok, tc.wantOK, reason)
			}
			if reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", reason, tc.wantReason)
			}
			// CheckNewRegistration is a peek: it must NEVER consume the one-shot
			// slot (that is ConsumeJoinSlot's job, called only after enroll).
			if fs.consumeCalled != 0 {
				t.Fatalf("CheckNewRegistration consumed the join slot %d times, want 0", fs.consumeCalled)
			}
		})
	}
}

// TestConsumeJoinSlot verifies the slot is only touched in one-shot mode. The
// actual decrement/exhaustion accounting lives in the store's atomic
// ConsumeOneShotJoin; the gate only gates the call on the join mode and
// propagates the store error.
func TestConsumeJoinSlot(t *testing.T) {
	cases := []struct {
		name         string
		joinMode     string
		joinModeErr  error
		consumeWon   bool
		consumeErr   error
		wantConsumed int
		wantErr      bool
	}{
		{
			name:         "one-shot mode consumes the slot",
			joinMode:     "one-shot",
			consumeWon:   true,
			wantConsumed: 1,
		},
		{
			name:         "one-shot slot already exhausted still returns nil (won=false)",
			joinMode:     "one-shot",
			consumeWon:   false,
			wantConsumed: 1,
		},
		{
			name:         "one-shot store error propagates",
			joinMode:     "one-shot",
			consumeErr:   errors.New("db down"),
			wantConsumed: 1,
			wantErr:      true,
		},
		{
			name:         "open mode is a no-op",
			joinMode:     "open",
			wantConsumed: 0,
		},
		{
			name:         "disabled mode is a no-op",
			joinMode:     "disabled",
			wantConsumed: 0,
		},
		{
			name:         "unset mode is a no-op",
			joinMode:     "", // store returns sql.ErrNoRows -> treated as not-one-shot
			wantConsumed: 0,
		},
		{
			name:         "GetSetting DB error propagates without consuming",
			joinMode:     "",
			joinModeErr:  errors.New("db down"),
			wantConsumed: 0,
			wantErr:      true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &admissionFakeStore{
				settings:   map[string]string{},
				settingErr: map[string]error{},
				consumeWon: tc.consumeWon,
				consumeErr: tc.consumeErr,
			}
			if tc.joinMode != "" {
				fs.settings[settingNodeJoinMode] = tc.joinMode
			}
			if tc.joinModeErr != nil {
				fs.settingErr[settingNodeJoinMode] = tc.joinModeErr
			}
			g := NewAdmissionGate(fs)

			err := g.ConsumeJoinSlot(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if fs.consumeCalled != tc.wantConsumed {
				t.Fatalf("ConsumeOneShotJoin called %d times, want %d", fs.consumeCalled, tc.wantConsumed)
			}
		})
	}
}

// TestCheckNetwork pins the network gate at its new home. It is the half the
// warp enrol calls, where clientIP(r) is the customer's real address rather than
// the warp leader every BYON node shares.
func TestCheckNetwork(t *testing.T) {
	cases := []struct {
		name       string
		ipMode     string
		ipModeErr  error
		cidrs      []store.AdmissionCIDR
		ip         net.IP
		wantOK     bool
		wantReason string
		wantErr    bool
	}{
		{
			name:   "allow mode admits anything, cidrs advisory",
			ipMode: "allow",
			cidrs:  []store.AdmissionCIDR{{CIDR: "10.0.0.0/8"}},
			ip:     net.ParseIP("203.0.113.7"),
			wantOK: true,
		},
		{
			name:   "unset defaults to allow (inert)",
			ip:     net.ParseIP("203.0.113.7"),
			wantOK: true,
		},
		{
			name:       "deny mode with no cidrs admits nobody",
			ipMode:     "deny",
			ip:         net.ParseIP("203.0.113.7"),
			wantOK:     false,
			wantReason: "admission_ip_denied",
		},
		{
			name:   "deny mode admits an ip inside a listed cidr",
			ipMode: "deny",
			cidrs:  []store.AdmissionCIDR{{CIDR: "203.0.113.0/24"}},
			ip:     net.ParseIP("203.0.113.7"),
			wantOK: true,
		},
		{
			name:       "deny mode refuses an ip outside every listed cidr",
			ipMode:     "deny",
			cidrs:      []store.AdmissionCIDR{{CIDR: "198.51.100.0/24"}},
			ip:         net.ParseIP("203.0.113.7"),
			wantOK:     false,
			wantReason: "admission_ip_denied",
		},
		{
			// An unresolvable client address must not pass a deny-mode gate.
			name:       "deny mode refuses a nil ip",
			ipMode:     "deny",
			cidrs:      []store.AdmissionCIDR{{CIDR: "0.0.0.0/0"}},
			ip:         nil,
			wantOK:     false,
			wantReason: "admission_ip_denied",
		},
		{
			name:      "db error fails closed",
			ipModeErr: errors.New("db down"),
			ip:        net.ParseIP("203.0.113.7"),
			wantErr:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &admissionFakeStore{
				settings:   map[string]string{},
				settingErr: map[string]error{},
				cidrs:      tc.cidrs,
			}
			if tc.ipMode != "" {
				fs.settings[settingNodeAdmissionIPMode] = tc.ipMode
			}
			if tc.ipModeErr != nil {
				fs.settingErr[settingNodeAdmissionIPMode] = tc.ipModeErr
			}
			g := NewAdmissionGate(fs)

			ok, reason, err := g.CheckNetwork(context.Background(), tc.ip)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (reason=%q)", ok, tc.wantOK, reason)
			}
			if reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}
