package services

import (
	"context"
	"reflect"
	"testing"
)

func TestCNAMETargetsExpandsTheLabel(t *testing.T) {
	for _, c := range []struct {
		name  string
		label string
		bases []string
		want  []string
	}{
		{"one base", "route", []string{"eu.dylaris.com"}, []string{"route.eu.dylaris.com"}},
		{"one per region", "route", []string{"eu.dylaris.com", "us.dylaris.com"},
			[]string{"route.eu.dylaris.com", "route.us.dylaris.com"}},
		{"case and space", "  Route ", []string{" EU.Dylaris.com "}, []string{"route.eu.dylaris.com"}},
		{"duplicate bases collapse", "route", []string{"eu.dylaris.com", "eu.dylaris.com"},
			[]string{"route.eu.dylaris.com"}},
		{"no label, no targets", "", []string{"eu.dylaris.com"}, nil},
		{"no bases, no targets", "route", nil, []string{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := CNAMETargets(c.label, c.bases)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("CNAMETargets(%q, %v) = %v, want %v", c.label, c.bases, got, c.want)
			}
		})
	}
}

// stubResolver answers exactly one CNAME and nothing else.
type stubCNAMEResolver struct{ cname string }

func (s stubCNAMEResolver) LookupCNAME(context.Context, string) (string, error) {
	return s.cname, nil
}
func (s stubCNAMEResolver) LookupHost(context.Context, string) ([]string, error) { return nil, nil }
func (s stubCNAMEResolver) LookupTXT(context.Context, string) ([]string, error)  { return nil, nil }

// The bug this pins is silent in the direction that matters: a customer who
// creates exactly the record they were told to create is judged NOT to have
// created it, so their claim expires and their route is removed on the deadline.
func TestCheckDomainPointsAtUsNeedsTheExpandedTarget(t *testing.T) {
	res := stubCNAMEResolver{cname: "route.eu.dylaris.com."}
	label, bases := "route", []string{"eu.dylaris.com"}

	if CheckDomainPointsAtUs(context.Background(), res, "mc.customer.test", []string{label}, nil) {
		t.Error("the bare label matched a real CNAME answer; that cannot happen and the test is wrong")
	}
	if !CheckDomainPointsAtUs(context.Background(), res, "mc.customer.test", CNAMETargets(label, bases), nil) {
		t.Error("a domain pointed at the published CNAME target was not accepted, " +
			"so the customer's correctly-configured domain fails and its route is deleted at the deadline")
	}
}
