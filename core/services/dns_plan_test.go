package services

import (
	"reflect"
	"testing"
	"time"
)

func onlineEdge(id, region, ip, wildcard string) GatewayEdgeInfo {
	return GatewayEdgeInfo{EdgeID: id, Status: "online", Region: region, IP: ip, Wildcard: wildcard}
}

// The panel carries its own copy of this rule (panel/src/components/settings/
// dnsZones.ts resolveZone) because it cannot call Go. The cases below are
// mirrored there under "parity with Core ResolveZone" - keep the two tables in
// step, or the screen can offer a zone Core will not actually use.
func TestResolveZone(t *testing.T) {
	zones := []string{"dylaris.com", "eu.dylaris.com", "example.org"}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"wildcard under the parent zone", "*.us.dylaris.com", "dylaris.com"},
		{"longest match wins over the parent", "*.eu.dylaris.com", "eu.dylaris.com"},
		{"the zone apex itself", "dylaris.com", "dylaris.com"},
		{"second managed zone", "*.eu.example.org", "example.org"},
		{"trailing dot is normalised", "*.us.dylaris.com.", "dylaris.com"},
		{"case is normalised", "*.US.Dylaris.COM", "dylaris.com"},
		{"unmanaged zone yields nothing", "*.eu.other.net", ""},
		{"empty name yields nothing", "", ""},
		// The label-boundary check. Without it this matches "dylaris.com" and the
		// reconciler writes into a zone on behalf of a name that is not in it.
		{"lookalike domain does not match", "evil-dylaris.com", ""},
		{"lookalike subdomain does not match", "*.eu.notdylaris.com", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveZone(tt.in, zones); got != tt.want {
				t.Errorf("ResolveZone(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolveZone_NoZonesConfigured(t *testing.T) {
	if got := ResolveZone("*.eu.dylaris.com", nil); got != "" {
		t.Errorf("ResolveZone with no zones = %q, want empty", got)
	}
}

// The base case: edges advertise their own wildcard and every online edge in the
// region answers for it.
func TestBuildDNSPlan_EdgeWildcards(t *testing.T) {
	edges := []GatewayEdgeInfo{
		onlineEdge("e1", "eu", "1.1.1.1", "*.eu.dylaris.com"),
		onlineEdge("e2", "eu", "2.2.2.2", "*.eu.dylaris.com"),
		onlineEdge("e3", "us", "3.3.3.3", "*.us.dylaris.com"),
	}
	plan := BuildDNSPlan(edges, nil, nil, []string{"dylaris.com"})

	if len(plan.Names) != 2 {
		t.Fatalf("Names = %d, want 2: %+v", len(plan.Names), plan.Names)
	}
	eu := plan.Names[0]
	if eu.Name != "*.eu.dylaris.com" || eu.Origin != DNSOriginEdge || eu.Zone != "dylaris.com" {
		t.Errorf("eu entry = %+v", eu)
	}
	if !reflect.DeepEqual(eu.IPs, []string{"1.1.1.1", "2.2.2.2"}) {
		t.Errorf("eu IPs = %v, want both online edges", eu.IPs)
	}
}

// The point of the feature: one region serving several domains, so a hoster can
// offer more than one brand from the same edges.
func TestBuildDNSPlan_PanelNamesGiveMultipleDomains(t *testing.T) {
	edges := []GatewayEdgeInfo{
		onlineEdge("e1", "eu", "1.1.1.1", "*.eu.dylaris.com"),
		onlineEdge("e2", "eu", "2.2.2.2", "*.eu.dylaris.com"),
	}
	regionNames := map[string][]string{"eu": {"*.eu.brand-a.com", "*.eu.brand-b.net"}}
	plan := BuildDNSPlan(edges, nil, regionNames, []string{"brand-a.com", "brand-b.net", "dylaris.com"})

	if len(plan.Names) != 2 {
		t.Fatalf("Names = %d, want both panel names: %+v", len(plan.Names), plan.Names)
	}
	for _, n := range plan.Names {
		if n.Origin != DNSOriginPanel {
			t.Errorf("%s origin = %q, want %q", n.Name, n.Origin, DNSOriginPanel)
		}
		if !reflect.DeepEqual(n.IPs, []string{"1.1.1.1", "2.2.2.2"}) {
			t.Errorf("%s IPs = %v, want every online edge in the region", n.Name, n.IPs)
		}
	}
	// The panel selection is authoritative: the edge's own wildcard is no longer
	// advertised, so it becomes an orphan and leaves under the grace period.
	for _, n := range plan.Names {
		if n.Name == "*.eu.dylaris.com" {
			t.Error("edge wildcard still planned despite a panel selection")
		}
	}
}

func TestBuildDNSPlan_PanelSelectionIsPerRegion(t *testing.T) {
	edges := []GatewayEdgeInfo{
		onlineEdge("e1", "eu", "1.1.1.1", "*.eu.dylaris.com"),
		onlineEdge("e2", "us", "3.3.3.3", "*.us.dylaris.com"),
	}
	// Only EU is configured in the panel; US must keep falling back to its edge.
	plan := BuildDNSPlan(edges, nil, map[string][]string{"eu": {"*.eu.brand.com"}}, []string{"brand.com", "dylaris.com"})

	byName := map[string]PlannedName{}
	for _, n := range plan.Names {
		byName[n.Name] = n
	}
	if got, ok := byName["*.eu.brand.com"]; !ok || got.Origin != DNSOriginPanel {
		t.Errorf("eu = %+v, want the panel name", got)
	}
	if got, ok := byName["*.us.dylaris.com"]; !ok || got.Origin != DNSOriginEdge {
		t.Errorf("us = %+v, want the edge fallback", got)
	}
}

// Offline edges must not appear in any record, but their region keeps working
// through whatever is still online.
func TestBuildDNSPlan_SkipsOfflineEdges(t *testing.T) {
	edges := []GatewayEdgeInfo{
		onlineEdge("e1", "eu", "1.1.1.1", "*.eu.dylaris.com"),
		{EdgeID: "e2", Status: "offline", Region: "eu", IP: "2.2.2.2", Wildcard: "*.eu.dylaris.com"},
		{EdgeID: "e3", Status: "online", Region: "eu", IP: "", Wildcard: "*.eu.dylaris.com"},
	}
	plan := BuildDNSPlan(edges, nil, nil, []string{"dylaris.com"})

	if len(plan.Names) != 1 {
		t.Fatalf("Names = %+v, want one", plan.Names)
	}
	if !reflect.DeepEqual(plan.Names[0].IPs, []string{"1.1.1.1"}) {
		t.Errorf("IPs = %v, want only the online edge with an address", plan.Names[0].IPs)
	}
}

// A region whose edges are ALL offline disappears from the plan entirely rather
// than planning an empty record set. Its records are then left untouched -
// better a stale address than a blackholed region.
func TestBuildDNSPlan_RegionFullyOfflineIsAbsent(t *testing.T) {
	edges := []GatewayEdgeInfo{
		{EdgeID: "e1", Status: "offline", Region: "eu", IP: "1.1.1.1", Wildcard: "*.eu.dylaris.com"},
		onlineEdge("e2", "us", "3.3.3.3", "*.us.dylaris.com"),
	}
	plan := BuildDNSPlan(edges, nil, nil, []string{"dylaris.com"})

	for _, n := range plan.Names {
		if n.Region == "eu" {
			t.Fatalf("fully offline region still planned: %+v", n)
		}
	}
	if len(plan.Names) != 1 {
		t.Errorf("Names = %+v, want only the live region", plan.Names)
	}
}

// A name outside every managed zone is reported, never written. Guessing a zone
// would mean touching one the operator never released.
func TestBuildDNSPlan_UnroutableNames(t *testing.T) {
	edges := []GatewayEdgeInfo{onlineEdge("e1", "eu", "1.1.1.1", "*.eu.unmanaged.net")}
	plan := BuildDNSPlan(edges, nil, nil, []string{"dylaris.com"})

	if len(plan.Names) != 0 {
		t.Errorf("Names = %+v, want none", plan.Names)
	}
	if len(plan.Unroutable) != 1 || plan.Unroutable[0].Name != "*.eu.unmanaged.net" {
		t.Fatalf("Unroutable = %+v, want the unmanaged name", plan.Unroutable)
	}
}

func TestBuildDNSPlan_DedupesAndNormalises(t *testing.T) {
	edges := []GatewayEdgeInfo{
		onlineEdge("e1", "eu", "1.1.1.1", "*.EU.Dylaris.com"),
		onlineEdge("e2", "EU", "1.1.1.1", "*.eu.dylaris.com."),
	}
	plan := BuildDNSPlan(edges, nil, nil, []string{"dylaris.com"})

	if len(plan.Names) != 1 {
		t.Fatalf("Names = %+v, want one after normalisation", plan.Names)
	}
	if got := plan.Names[0].IPs; !reflect.DeepEqual(got, []string{"1.1.1.1"}) {
		t.Errorf("IPs = %v, want the duplicate collapsed", got)
	}
}

func TestBuildDNSPlan_NoEdges(t *testing.T) {
	plan := BuildDNSPlan(nil, nil, map[string][]string{"eu": {"*.eu.dylaris.com"}}, []string{"dylaris.com"})
	if len(plan.Names) != 0 || len(plan.Unroutable) != 0 {
		t.Errorf("plan = %+v, want empty with no edges", plan)
	}
}

// Several relays in one region deliberately share one public host - that is what
// makes them round-robin and rolling-update capable - so every address answers
// for the same name.
func TestBuildDNSPlan_RelaysShareOneName(t *testing.T) {
	relays := []RelayAdvert{
		{Name: "beam-eu.dylaris.com", IP: "9.9.9.9"},
		{Name: "beam-eu.dylaris.com", IP: "8.8.8.8"},
		{Name: "beam-us.dylaris.com", IP: "7.7.7.7"},
	}
	plan := BuildDNSPlan(nil, relays, nil, []string{"dylaris.com"})

	if len(plan.Names) != 2 {
		t.Fatalf("Names = %+v, want two relay names", plan.Names)
	}
	eu := plan.Names[0]
	if eu.Name != "beam-eu.dylaris.com" || eu.Origin != DNSOriginRelay {
		t.Errorf("eu entry = %+v", eu)
	}
	if !reflect.DeepEqual(eu.IPs, []string{"8.8.8.8", "9.9.9.9"}) {
		t.Errorf("eu IPs = %v, want both relays", eu.IPs)
	}
}

// A relay is only actionable with both halves: the host is the record name and
// the IP is its value.
func TestBuildDNSPlan_RelaysNeedNameAndIP(t *testing.T) {
	relays := []RelayAdvert{
		{Name: "", IP: "9.9.9.9"},
		{Name: "beam-eu.dylaris.com", IP: ""},
		{Name: "beam-us.dylaris.com", IP: "7.7.7.7"},
	}
	plan := BuildDNSPlan(nil, relays, nil, []string{"dylaris.com"})

	if len(plan.Names) != 1 || plan.Names[0].Name != "beam-us.dylaris.com" {
		t.Fatalf("Names = %+v, want only the complete relay", plan.Names)
	}
}

func TestBuildDNSPlan_RelayOutsideManagedZones(t *testing.T) {
	relays := []RelayAdvert{{Name: "beam.unmanaged.net", IP: "9.9.9.9"}}
	plan := BuildDNSPlan(nil, relays, nil, []string{"dylaris.com"})

	if len(plan.Names) != 0 {
		t.Errorf("Names = %+v, want none", plan.Names)
	}
	if len(plan.Unroutable) != 1 || plan.Unroutable[0].Origin != DNSOriginRelay {
		t.Fatalf("Unroutable = %+v, want the relay name", plan.Unroutable)
	}
}

// Planning one name twice would make each pass delete the other's addresses as
// stale, flapping the record on every tick. The edge wins and the collision is
// reported instead.
func TestBuildDNSPlan_RelayNameCollidingWithEdgeIsRefused(t *testing.T) {
	edges := []GatewayEdgeInfo{onlineEdge("e1", "eu", "1.1.1.1", "beam-eu.dylaris.com")}
	relays := []RelayAdvert{{Name: "beam-eu.dylaris.com", IP: "9.9.9.9"}}
	plan := BuildDNSPlan(edges, relays, nil, []string{"dylaris.com"})

	if len(plan.Names) != 1 {
		t.Fatalf("Names = %+v, want the name planned exactly once", plan.Names)
	}
	if plan.Names[0].Origin != DNSOriginEdge {
		t.Errorf("origin = %q, want the edge to win", plan.Names[0].Origin)
	}
	if !reflect.DeepEqual(plan.Names[0].IPs, []string{"1.1.1.1"}) {
		t.Errorf("IPs = %v, want the edge address only", plan.Names[0].IPs)
	}
	if len(plan.Unroutable) != 1 || plan.Unroutable[0].Origin != DNSOriginRelay {
		t.Fatalf("Unroutable = %+v, want the relay collision reported", plan.Unroutable)
	}
}

// Edges and relays must land in ONE plan: the orphan sweep works off the plan,
// so a relay name missing from it would be deleted as abandoned.
func TestBuildDNSPlan_EdgesAndRelaysTogether(t *testing.T) {
	edges := []GatewayEdgeInfo{onlineEdge("e1", "eu", "1.1.1.1", "*.eu.dylaris.com")}
	relays := []RelayAdvert{{Name: "beam-eu.dylaris.com", IP: "9.9.9.9"}}
	plan := BuildDNSPlan(edges, relays, nil, []string{"dylaris.com"})

	advertised := plan.AdvertisedNames()
	if !advertised["*.eu.dylaris.com"] || !advertised["beam-eu.dylaris.com"] {
		t.Fatalf("AdvertisedNames() = %v, want both the edge and the relay name", advertised)
	}
}

func TestBuildDNSPlan_RelayNamesAreNormalised(t *testing.T) {
	relays := []RelayAdvert{
		{Name: "  Beam-EU.Dylaris.com.  ", IP: "9.9.9.9"},
		{Name: "beam-eu.dylaris.com", IP: "9.9.9.9"},
	}
	plan := BuildDNSPlan(nil, relays, nil, []string{"dylaris.com"})

	if len(plan.Names) != 1 || plan.Names[0].Name != "beam-eu.dylaris.com" {
		t.Fatalf("Names = %+v, want one normalised entry", plan.Names)
	}
	if !reflect.DeepEqual(plan.Names[0].IPs, []string{"9.9.9.9"}) {
		t.Errorf("IPs = %v, want the duplicate collapsed", plan.Names[0].IPs)
	}
}

// The safety property of the whole feature: only names the reconciler recorded
// creating can ever come back as deletable.
func TestPlanOrphans_OnlyOwnedNames(t *testing.T) {
	now := time.Now()
	owned := map[string]OwnedName{
		"*.eu.dylaris.com": {Zone: "dylaris.com", LastAdvertised: now.Add(-time.Hour)},
	}
	// "www.dylaris.com" is the hoster's website. It is not owned, so no input
	// can put it on the deletion list.
	orphans := PlanOrphans(owned, map[string]bool{}, now, 15*time.Minute)

	if len(orphans) != 1 || orphans[0].Name != "*.eu.dylaris.com" {
		t.Fatalf("orphans = %+v, want only the owned name", orphans)
	}
	if orphans[0].Zone != "dylaris.com" {
		t.Errorf("zone = %q, want the recorded zone", orphans[0].Zone)
	}
}

func TestPlanOrphans_GracePeriod(t *testing.T) {
	now := time.Now()
	grace := 15 * time.Minute
	tests := []struct {
		name     string
		lastSeen time.Time
		want     int
	}{
		{"just missed a heartbeat", now.Add(-time.Minute), 0},
		{"still inside the grace period", now.Add(-14 * time.Minute), 0},
		{"exactly at the boundary expires", now.Add(-15 * time.Minute), 1},
		{"long gone", now.Add(-24 * time.Hour), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owned := map[string]OwnedName{"*.eu.dylaris.com": {Zone: "dylaris.com", LastAdvertised: tt.lastSeen}}
			if got := len(PlanOrphans(owned, map[string]bool{}, now, grace)); got != tt.want {
				t.Errorf("orphans = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPlanOrphans_AdvertisedNamesAreNeverOrphans(t *testing.T) {
	now := time.Now()
	owned := map[string]OwnedName{"*.eu.dylaris.com": {Zone: "dylaris.com", LastAdvertised: now.Add(-24 * time.Hour)}}
	advertised := map[string]bool{"*.eu.dylaris.com": true}

	if got := PlanOrphans(owned, advertised, now, time.Minute); len(got) != 0 {
		t.Errorf("orphans = %+v, want none for a name still advertised", got)
	}
}

// An entry written before the timestamp field existed must not read as expired,
// or one upgrade would orphan every managed record at once.
func TestPlanOrphans_ZeroTimestampIsNotExpired(t *testing.T) {
	owned := map[string]OwnedName{"*.eu.dylaris.com": {Zone: "dylaris.com"}}
	if got := PlanOrphans(owned, map[string]bool{}, time.Now(), time.Minute); len(got) != 0 {
		t.Errorf("orphans = %+v, want none for a zero timestamp", got)
	}
}

// An entry with no zone cannot be acted on - there is nowhere to send the
// delete - so it must not be reported as deletable.
func TestPlanOrphans_EntryWithoutZoneIsSkipped(t *testing.T) {
	owned := map[string]OwnedName{"*.eu.dylaris.com": {LastAdvertised: time.Now().Add(-time.Hour)}}
	if got := PlanOrphans(owned, map[string]bool{}, time.Now(), time.Minute); len(got) != 0 {
		t.Errorf("orphans = %+v, want none without a zone", got)
	}
}

func TestRefreshOwnership(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	owned := map[string]OwnedName{
		"*.eu.dylaris.com": {Zone: "dylaris.com", LastAdvertised: old},
		"*.us.dylaris.com": {Zone: "dylaris.com", LastAdvertised: old},
	}
	plan := DNSPlan{Names: []PlannedName{
		{Name: "*.eu.dylaris.com", Zone: "dylaris.com"},
		{Name: "*.ap.dylaris.com", Zone: "dylaris.com"},
	}}

	next := RefreshOwnership(owned, plan, now)

	if !next["*.eu.dylaris.com"].LastAdvertised.Equal(now) {
		t.Error("an advertised name did not get its timestamp bumped")
	}
	if !next["*.ap.dylaris.com"].LastAdvertised.Equal(now) {
		t.Error("a newly planned name was not recorded")
	}
	// The one that vanished keeps its old timestamp - that is exactly what the
	// grace period measures against.
	if !next["*.us.dylaris.com"].LastAdvertised.Equal(old) {
		t.Error("a name that stopped being advertised had its timestamp reset")
	}
}

func TestDNSPlanAdvertisedNames(t *testing.T) {
	plan := DNSPlan{Names: []PlannedName{
		{Name: "*.eu.dylaris.com"},
		{Name: "*.us.dylaris.com"},
	}}
	got := plan.AdvertisedNames()
	if len(got) != 2 || !got["*.eu.dylaris.com"] || !got["*.us.dylaris.com"] {
		t.Errorf("AdvertisedNames() = %v", got)
	}
}

// A self-detected container address must never reach public DNS. Inside Swarm a
// relay's outbound interface is the docker_gwbridge (172.18.0.0/16), and before
// this guard the updater published exactly that for beam.<zone>. Filtering the
// advert (rather than erroring) also means an already-written bad record goes
// unadvertised and is deleted by the reconciler on its own.
func TestBuildDNSPlan_PrivateAddressesAreNotPublishable(t *testing.T) {
	edges := []GatewayEdgeInfo{
		onlineEdge("e1", "eu", "172.18.0.6", "*.eu.dylaris.com"), // gwbridge
		onlineEdge("e2", "eu", "94.130.98.3", "*.eu.dylaris.com"),
	}
	relays := []RelayAdvert{
		{Name: "beam.dylaris.com", IP: "172.18.0.6"},  // gwbridge
		{Name: "beam.dylaris.com", IP: "10.20.0.14"},  // overlay
		{Name: "beam.dylaris.com", IP: "100.72.1.9"},  // CGNAT
		{Name: "beam.dylaris.com", IP: "not-an-ip"},
		{Name: "beam.dylaris.com", IP: "94.130.98.3"},
	}
	plan := BuildDNSPlan(edges, relays, nil, []string{"dylaris.com"})

	if len(plan.Names) != 2 {
		t.Fatalf("Names = %+v, want wildcard + relay name", plan.Names)
	}
	for _, n := range plan.Names {
		if !reflect.DeepEqual(n.IPs, []string{"94.130.98.3"}) {
			t.Errorf("%s IPs = %v, want only the public address", n.Name, n.IPs)
		}
	}
}

// A relay whose ONLY addresses are private disappears from the plan entirely -
// that is the deletion path for a record that was already written wrong.
func TestBuildDNSPlan_RelayWithOnlyPrivateAddressesIsDropped(t *testing.T) {
	relays := []RelayAdvert{{Name: "beam.dylaris.com", IP: "172.18.0.6"}}
	plan := BuildDNSPlan(nil, relays, nil, []string{"dylaris.com"})
	if len(plan.Names) != 0 {
		t.Fatalf("Names = %+v, want none", plan.Names)
	}
}
