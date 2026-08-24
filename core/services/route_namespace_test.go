package services

import "testing"

// What this decides is who pays for an address. A domain classified as ours
// spends the tenant's allowance; one classified as theirs is free and unlimited.
// Both mistakes are real: calling their domain ours charges them for something
// we do not supply, and calling ours theirs hands out our namespace for nothing.
func TestDomainIsOurs(t *testing.T) {
	bases := HosterBaseDomains(`[{"domain":"dylaris.com","validation":"dns"},{"domain":"eu.dylaris.com","validation":"letters"}]`)

	cases := []struct {
		name   string
		domain string
		want   bool
	}{
		{"a subdomain of ours", "survival.dylaris.com", true},
		{"a deeper subdomain of ours", "eu.dylaris.com", true},
		{"the base domain itself", "dylaris.com", true},
		{"case is irrelevant", "SURVIVAL.Dylaris.COM", true},
		{"a trailing dot is still ours", "survival.dylaris.com.", true},
		// The one that matters: a suffix match without the dot anchor would hand
		// "notdylaris.com" a free pass on somebody else's brand.
		{"a domain merely ENDING in ours is not ours", "notdylaris.com", false},
		{"another suffix collision", "mydylaris.com", false},
		{"the customer's own domain", "mc.theirown.net", false},
		{"empty is not ours", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DomainIsOurs(c.domain, bases); got != c.want {
				t.Errorf("DomainIsOurs(%q) = %v, want %v", c.domain, got, c.want)
			}
		})
	}
}

// Unreadable settings must not make every customer domain start counting against
// an allowance. Failing open here is the safe direction: the cap exists to ration
// our namespace, and a namespace we cannot enumerate is not one we are using up.
func TestHosterBaseDomainsFailsOpen(t *testing.T) {
	for _, raw := range []string{"", "   ", "not json at all", `{"domain":"x.com"}`} {
		bases := HosterBaseDomains(raw)
		if len(bases) != 0 {
			t.Errorf("HosterBaseDomains(%q) = %v, want none", raw, bases)
		}
		if DomainIsOurs("survival.dylaris.com", bases) {
			t.Errorf("with settings %q nothing should classify as ours", raw)
		}
	}
}

func TestHosterBaseDomainsNormalizes(t *testing.T) {
	bases := HosterBaseDomains(`[{"domain":"  Dylaris.COM  "},{"domain":""},{"domain":"eu.dylaris.com"}]`)
	if len(bases) != 2 || bases[0] != "dylaris.com" || bases[1] != "eu.dylaris.com" {
		t.Fatalf("bases = %v, want [dylaris.com eu.dylaris.com]", bases)
	}
}
