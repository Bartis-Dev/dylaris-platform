package services

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/libdns/libdns"
)

// fakeLibDNS is an in-memory libdns provider. The whole point of moving to
// libdns is that the reconciler's DNS behaviour can now be tested without a
// network or a vendor account, which the hand-written HTTP client made
// impossible.
type fakeLibDNS struct {
	zone    string
	records []libdns.Record
	getErr  error
	lister  bool
}

func (f *fakeLibDNS) GetRecords(_ context.Context, zone string) ([]libdns.Record, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if zone != f.zone {
		return nil, nil
	}
	return append([]libdns.Record(nil), f.records...), nil
}

func (f *fakeLibDNS) AppendRecords(_ context.Context, _ string, recs []libdns.Record) ([]libdns.Record, error) {
	f.records = append(f.records, recs...)
	return recs, nil
}

func (f *fakeLibDNS) DeleteRecords(_ context.Context, _ string, recs []libdns.Record) ([]libdns.Record, error) {
	for _, want := range recs {
		w := want.RR()
		for i, have := range f.records {
			h := have.RR()
			if h.Name == w.Name && h.Type == w.Type && h.Data == w.Data {
				f.records = append(f.records[:i], f.records[i+1:]...)
				break
			}
		}
	}
	return recs, nil
}

// zoneListingFake adds the OPTIONAL ZoneLister, so the two shapes can be tested
// apart - a provider without it is not a misconfiguration.
type zoneListingFake struct {
	*fakeLibDNS
	zones   []string
	listErr error
}

func (z *zoneListingFake) ListZones(context.Context) ([]libdns.Zone, error) {
	if z.listErr != nil {
		return nil, z.listErr
	}
	out := make([]libdns.Zone, 0, len(z.zones))
	for _, name := range z.zones {
		out = append(out, libdns.Zone{Name: name})
	}
	return out, nil
}

func addr(t *testing.T, name, ip string) libdns.Address {
	t.Helper()
	return libdns.Address{Name: name, TTL: time.Minute, IP: netip.MustParseAddr(ip)}
}

func newTestProvider(t *testing.T, f any) DNSProvider {
	t.Helper()
	p, err := wrapLibDNS("fake", f)
	if err != nil {
		t.Fatalf("wrapLibDNS: %v", err)
	}
	return p
}

// The reconciler speaks FQDNs; libdns speaks names relative to the zone. Getting
// that translation wrong silently manages the wrong name.
func TestListAMatchesOnTheAbsoluteName(t *testing.T) {
	f := &fakeLibDNS{
		zone: "example.com",
		records: []libdns.Record{
			addr(t, "*.eu", "1.1.1.1"),
			addr(t, "*.eu", "2.2.2.2"),
			addr(t, "*.us", "3.3.3.3"), // different name
			libdns.CNAME{Name: "www", Target: "example.com."},
		},
	}
	p := newTestProvider(t, f)

	got, err := p.ListA(context.Background(), "example.com", "*.eu.example.com")
	if err != nil {
		t.Fatalf("ListA: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListA returned %d records, want 2: %+v", len(got), got)
	}
	if got[0].IP != "1.1.1.1" || got[1].IP != "2.2.2.2" {
		t.Errorf("ListA = %+v, want the two *.eu addresses in stable order", got)
	}
}

func TestListAIgnoresOtherRecordTypes(t *testing.T) {
	f := &fakeLibDNS{
		zone: "example.com",
		records: []libdns.Record{
			libdns.CNAME{Name: "*.eu", Target: "somewhere.example.com."},
			libdns.TXT{Name: "*.eu", Text: "not an address"},
		},
	}
	p := newTestProvider(t, f)

	got, err := p.ListA(context.Background(), "example.com", "*.eu.example.com")
	if err != nil {
		t.Fatalf("ListA: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListA = %+v, want nothing - only A records are managed", got)
	}
}

// Several edges answer for one wildcard, so creating must ADD rather than
// replace. AppendRecords is the difference between round-robin and one edge.
func TestCreateAKeepsSiblings(t *testing.T) {
	f := &fakeLibDNS{zone: "example.com", records: []libdns.Record{addr(t, "*.eu", "1.1.1.1")}}
	p := newTestProvider(t, f)
	ctx := context.Background()

	if err := p.CreateA(ctx, "example.com", "*.eu.example.com", "2.2.2.2"); err != nil {
		t.Fatalf("CreateA: %v", err)
	}
	got, _ := p.ListA(ctx, "example.com", "*.eu.example.com")
	if len(got) != 2 {
		t.Fatalf("after CreateA there are %d records, want 2 - the sibling was replaced", len(got))
	}
}

func TestDeleteARemovesOnlyTheMatchingValue(t *testing.T) {
	f := &fakeLibDNS{zone: "example.com", records: []libdns.Record{
		addr(t, "*.eu", "1.1.1.1"),
		addr(t, "*.eu", "2.2.2.2"),
	}}
	p := newTestProvider(t, f)
	ctx := context.Background()

	if err := p.DeleteA(ctx, "example.com", "*.eu.example.com", "1.1.1.1"); err != nil {
		t.Fatalf("DeleteA: %v", err)
	}
	got, _ := p.ListA(ctx, "example.com", "*.eu.example.com")
	if len(got) != 1 || got[0].IP != "2.2.2.2" {
		t.Errorf("after DeleteA: %+v, want only 2.2.2.2 left", got)
	}
}

func TestCreateARejectsAnInvalidIP(t *testing.T) {
	p := newTestProvider(t, &fakeLibDNS{zone: "example.com"})
	if err := p.CreateA(context.Background(), "example.com", "*.eu.example.com", "not-an-ip"); err == nil {
		t.Error("CreateA accepted a non-address")
	}
}

// A read failure must surface, because the reconciler's rail is "never delete on
// a read failure" - swallowing the error here would silently delete everything.
func TestListAPropagatesReadErrors(t *testing.T) {
	f := &fakeLibDNS{zone: "example.com", getErr: errors.New("upstream down")}
	p := newTestProvider(t, f)
	if _, err := p.ListA(context.Background(), "example.com", "*.eu.example.com"); err == nil {
		t.Error("ListA swallowed a read error")
	}
}

// Zone discovery has three distinguishable outcomes, and merging them sends an
// operator to the wrong remedy.
func TestZonesDistinguishesUnsupportedFromFailedFromEmpty(t *testing.T) {
	ctx := context.Background()

	t.Run("provider cannot list zones", func(t *testing.T) {
		p := newTestProvider(t, &fakeLibDNS{zone: "example.com"})
		_, err := p.Zones(ctx)
		if !errors.Is(err, ErrZoneListingUnsupported) {
			t.Errorf("err = %v, want ErrZoneListingUnsupported", err)
		}
	})

	t.Run("the call failed", func(t *testing.T) {
		p := newTestProvider(t, &zoneListingFake{
			fakeLibDNS: &fakeLibDNS{zone: "example.com"},
			listErr:    errors.New("403 forbidden"),
		})
		_, err := p.Zones(ctx)
		if err == nil || errors.Is(err, ErrZoneListingUnsupported) {
			t.Errorf("err = %v, want the raw provider error, not 'unsupported'", err)
		}
	})

	t.Run("no zones visible", func(t *testing.T) {
		p := newTestProvider(t, &zoneListingFake{fakeLibDNS: &fakeLibDNS{zone: "example.com"}})
		zones, err := p.Zones(ctx)
		if err != nil {
			t.Fatalf("Zones: %v", err)
		}
		if len(zones) != 0 {
			t.Errorf("zones = %v, want empty", zones)
		}
	})

	t.Run("zones returned sorted", func(t *testing.T) {
		p := newTestProvider(t, &zoneListingFake{
			fakeLibDNS: &fakeLibDNS{zone: "example.com"},
			zones:      []string{"b.example", "a.example"},
		})
		zones, err := p.Zones(ctx)
		if err != nil || len(zones) != 2 || zones[0] != "a.example" {
			t.Errorf("zones = %v (err %v), want [a.example b.example]", zones, err)
		}
	})
}

func TestNewDNSProvider(t *testing.T) {
	t.Run("no token means feature off, not an error", func(t *testing.T) {
		p, err := NewDNSProvider("cloudflare", "  ")
		if err != nil || p != nil {
			t.Errorf("NewDNSProvider = (%v, %v), want (nil, nil)", p, err)
		}
	})
	t.Run("empty provider name defaults to cloudflare", func(t *testing.T) {
		p, err := NewDNSProvider("", "token")
		if err != nil || p == nil {
			t.Errorf("NewDNSProvider = (%v, %v), want a provider", p, err)
		}
	})
	t.Run("an unknown provider is rejected loudly", func(t *testing.T) {
		if _, err := NewDNSProvider("not-a-real-dns-vendor", "token"); err == nil {
			t.Error("NewDNSProvider accepted an unimplemented provider")
		}
	})
	t.Run("a bare token still builds a single-field provider", func(t *testing.T) {
		// The pre-catalogue storage format. Nothing was migrated, so this has to
		// keep working for every one-token provider, not just cloudflare.
		for _, name := range []string{"cloudflare", "hetzner", "desec"} {
			p, err := NewDNSProvider(name, "some-token")
			if err != nil || p == nil {
				t.Errorf("NewDNSProvider(%q, bare token) = (%v, %v), want a provider", name, p, err)
			}
		}
	})
	t.Run("a multi-field provider reads its JSON credential", func(t *testing.T) {
		p, err := NewDNSProvider("porkbun", `{"api_key":"pk","api_secret_key":"sk"}`)
		if err != nil || p == nil {
			t.Errorf("NewDNSProvider(porkbun, json) = (%v, %v), want a provider", p, err)
		}
	})
	t.Run("a multi-field provider names the field it is missing", func(t *testing.T) {
		// The whole point of validating here: the vendor's own error for a blank
		// secret says nothing about WHICH of four values was left out.
		_, err := NewDNSProvider("netcup", `{"customer_number":"12345"}`)
		if err == nil {
			t.Fatal("NewDNSProvider accepted netcup without its API key or password")
		}
		for _, want := range []string{"API key", "API password"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name the missing field %q", err, want)
			}
		}
	})
	t.Run("every catalogued provider has a constructor", func(t *testing.T) {
		// Guards the one way this catalogue can rot: a spec added without a case
		// in newLibDNSProvider, which the panel would offer and then fail on.
		for _, spec := range SupportedDNSProviders() {
			values := map[string]string{}
			for _, f := range spec.Fields {
				values[f.Key] = "x"
			}
			if _, err := newLibDNSProvider(spec.Name, values); err != nil {
				t.Errorf("provider %q is offered but does not construct: %v", spec.Name, err)
			}
		}
	})
}
