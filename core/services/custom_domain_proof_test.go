package services

import (
	"context"
	"errors"
	"testing"
)

// fakeResolver drives the proof without touching DNS.
type fakeResolver struct {
	cname    map[string]string
	hosts    map[string][]string
	txt      map[string][]string
	cnameErr error
	hostErr  error
	txtErr   error
}

func (f *fakeResolver) LookupCNAME(_ context.Context, host string) (string, error) {
	if f.cnameErr != nil {
		return "", f.cnameErr
	}
	// A real resolver returns the queried name when there is no CNAME.
	if v, ok := f.cname[host]; ok {
		return v, nil
	}
	return host + ".", nil
}
func (f *fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if f.hostErr != nil {
		return nil, f.hostErr
	}
	return f.hosts[host], nil
}
func (f *fakeResolver) LookupTXT(_ context.Context, host string) ([]string, error) {
	if f.txtErr != nil {
		return nil, f.txtErr
	}
	return f.txt[host], nil
}

func TestCheckDomainPointsAtUs(t *testing.T) {
	const domain = "mc.example.com"
	targets := []string{"route.eu.dylaris.com"}
	edges := []string{"94.130.98.3", "178.104.241.73"}

	t.Run("CNAME at a published target passes", func(t *testing.T) {
		r := &fakeResolver{cname: map[string]string{domain: "route.eu.dylaris.com."}}
		if !CheckDomainPointsAtUs(context.Background(), r, domain, targets, edges) {
			t.Error("a correct CNAME was rejected")
		}
	})

	// An apex domain cannot carry a CNAME, and a Cloudflare-proxied record hides
	// the real target behind Cloudflare's own. A CNAME-only check would reject
	// both, and both are customers who did exactly what they were told.
	t.Run("A record at an edge passes when the CNAME does not match", func(t *testing.T) {
		r := &fakeResolver{hosts: map[string][]string{domain: {"178.104.241.73"}}}
		if !CheckDomainPointsAtUs(context.Background(), r, domain, targets, edges) {
			t.Error("a correct A record was rejected")
		}
	})

	t.Run("pointing somewhere else fails", func(t *testing.T) {
		r := &fakeResolver{
			cname: map[string]string{domain: "someone-else.example.net."},
			hosts: map[string][]string{domain: {"203.0.113.9"}},
		}
		if CheckDomainPointsAtUs(context.Background(), r, domain, targets, edges) {
			t.Error("a domain pointing elsewhere passed")
		}
	})

	// The trap this design has to survive: a resolver that answers a name that
	// does not exist with some public address (a search-domain wildcard). Since
	// the check compares against OUR addresses, a bogus answer cannot pass.
	t.Run("a wildcard NXDOMAIN answer does not pass", func(t *testing.T) {
		r := &fakeResolver{hosts: map[string][]string{domain: {"46.225.53.182"}}}
		if CheckDomainPointsAtUs(context.Background(), r, domain, targets, edges) {
			t.Error("an unrelated wildcard answer was accepted as proof")
		}
	})

	t.Run("resolution failure is not proof", func(t *testing.T) {
		r := &fakeResolver{cnameErr: errors.New("nxdomain"), hostErr: errors.New("nxdomain")}
		if CheckDomainPointsAtUs(context.Background(), r, domain, targets, edges) {
			t.Error("a failed lookup was treated as proof")
		}
	})

	t.Run("no configured targets or edges cannot pass", func(t *testing.T) {
		r := &fakeResolver{hosts: map[string][]string{domain: {"94.130.98.3"}}}
		if CheckDomainPointsAtUs(context.Background(), r, domain, nil, nil) {
			t.Error("passed with nothing configured to compare against")
		}
	})

	t.Run("trailing dots and case do not matter", func(t *testing.T) {
		r := &fakeResolver{cname: map[string]string{domain: "ROUTE.eu.Dylaris.com."}}
		if !CheckDomainPointsAtUs(context.Background(), r, domain, targets, edges) {
			t.Error("case/trailing-dot normalisation failed")
		}
	})
}

func TestCheckTXTToken(t *testing.T) {
	const domain = "mc.example.com"
	token := "dylaris-verify=0123456789abcdef"

	t.Run("the published token passes", func(t *testing.T) {
		r := &fakeResolver{txt: map[string][]string{TXTVerifyPrefix + domain: {token}}}
		if !CheckTXTToken(context.Background(), r, domain, token) {
			t.Error("the correct TXT record was rejected")
		}
	})

	t.Run("a different token fails", func(t *testing.T) {
		r := &fakeResolver{txt: map[string][]string{TXTVerifyPrefix + domain: {"dylaris-verify=deadbeefdeadbeef"}}}
		if CheckTXTToken(context.Background(), r, domain, token) {
			t.Error("a foreign token was accepted")
		}
	})

	// An empty token must never match, or a claim with no token issued would be
	// verifiable by publishing an empty record.
	t.Run("an empty token never passes", func(t *testing.T) {
		r := &fakeResolver{txt: map[string][]string{TXTVerifyPrefix + domain: {""}}}
		if CheckTXTToken(context.Background(), r, domain, "") {
			t.Error("an empty token was accepted")
		}
	})

	t.Run("a record on the bare domain does not count", func(t *testing.T) {
		r := &fakeResolver{txt: map[string][]string{domain: {token}}}
		if CheckTXTToken(context.Background(), r, domain, token) {
			t.Error("a TXT record outside the _dylaris-verify label was accepted")
		}
	})

	t.Run("lookup failure is not proof", func(t *testing.T) {
		r := &fakeResolver{txtErr: errors.New("nxdomain")}
		if CheckTXTToken(context.Background(), r, domain, token) {
			t.Error("a failed lookup was treated as proof")
		}
	})
}

func TestNewTXTTokenIsUniqueAndPrefixed(t *testing.T) {
	a, err := NewTXTToken()
	if err != nil {
		t.Fatalf("NewTXTToken: %v", err)
	}
	b, _ := NewTXTToken()
	if a == b {
		t.Error("two tokens collided")
	}
	if len(a) < 20 {
		t.Errorf("token %q is too short to be unguessable", a)
	}
}
