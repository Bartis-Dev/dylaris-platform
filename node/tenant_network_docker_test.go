package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/docker/docker/api/types/network"
)

// fakeDockerNet is an in-memory dockerNetAPI for daemon-free tests.
type fakeDockerNet struct {
	nets     []network.Summary
	created  []network.CreateOptions
	connects []string // "netID|container"
	removed  []string
	nextID   int
}

func (f *fakeDockerNet) NetworkList(_ context.Context, _ network.ListOptions) ([]network.Summary, error) {
	return f.nets, nil
}
func (f *fakeDockerNet) NetworkInspect(_ context.Context, id string, _ network.InspectOptions) (network.Inspect, error) {
	for _, n := range f.nets {
		if n.ID == id {
			return n, nil
		}
	}
	return network.Inspect{}, fmt.Errorf("no such network: %s", id)
}
func (f *fakeDockerNet) NetworkCreate(_ context.Context, name string, opts network.CreateOptions) (network.CreateResponse, error) {
	f.nextID++
	id := fmt.Sprintf("net%d", f.nextID)
	f.created = append(f.created, opts)
	sum := network.Summary{Name: name, ID: id, Driver: opts.Driver}
	if opts.IPAM != nil {
		sum.IPAM = *opts.IPAM
	}
	f.nets = append(f.nets, sum)
	return network.CreateResponse{ID: id}, nil
}
func (f *fakeDockerNet) NetworkConnect(_ context.Context, id, c string, _ *network.EndpointSettings) error {
	f.connects = append(f.connects, id+"|"+c)
	return nil
}
func (f *fakeDockerNet) NetworkDisconnect(_ context.Context, _, _ string, _ bool) error { return nil }
func (f *fakeDockerNet) NetworkRemove(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return nil
}

func newTestManager(t *testing.T, globalDriver string) (*TenantNetworkManager, *fakeDockerNet) {
	t.Helper()
	f := &fakeDockerNet{nets: []network.Summary{
		{Name: "dylaris_net", ID: "global1", Driver: globalDriver,
			IPAM: network.IPAM{Config: []network.IPAMConfig{{Subnet: "172.18.0.0/16"}}}},
	}}
	alloc := loadTenantAllocator(t.TempDir())
	return newTenantNetworkManager(f, context.Background(), alloc, "node-host"), f
}

func TestEnsureTenantNetworkCreatesAndIsIdempotent(t *testing.T) {
	m, f := newTestManager(t, "bridge")
	m.mu.Lock()
	name, err := m.EnsureTenantNetwork("owner-A")
	m.mu.Unlock()
	if err != nil {
		t.Fatalf("EnsureTenantNetwork: %v", err)
	}
	if name != "dylaris_tenant_owner-a" {
		t.Fatalf("name = %s, want dylaris_tenant_owner-a", name)
	}
	if len(f.created) != 1 {
		t.Fatalf("created %d networks, want 1", len(f.created))
	}
	if got := f.created[0].IPAM.Config[0].Subnet; got != "10.0.0.0/26" {
		t.Fatalf("subnet = %s, want 10.0.0.0/26 (avoids docker 172.18/16)", got)
	}
	if f.created[0].Driver != "bridge" || f.created[0].Attachable {
		t.Fatalf("driver/attachable = %s/%v, want bridge/false", f.created[0].Driver, f.created[0].Attachable)
	}
	// Node connected to the tenant net.
	if len(f.connects) != 1 || f.connects[0] != "net1|node-host" {
		t.Fatalf("connects = %v, want [net1|node-host]", f.connects)
	}
	// Second call: no new network.
	m.mu.Lock()
	_, err = m.EnsureTenantNetwork("owner-A")
	m.mu.Unlock()
	if err != nil {
		t.Fatalf("EnsureTenantNetwork(2): %v", err)
	}
	if len(f.created) != 1 {
		t.Fatalf("idempotency broken: created %d networks", len(f.created))
	}
}

func TestEnsureTenantNetworkMirrorsOverlayDriver(t *testing.T) {
	m, f := newTestManager(t, "overlay")
	m.mu.Lock()
	_, err := m.EnsureTenantNetwork("owner-A")
	m.mu.Unlock()
	if err != nil {
		t.Fatalf("EnsureTenantNetwork: %v", err)
	}
	if f.created[0].Driver != "overlay" || !f.created[0].Attachable {
		t.Fatalf("driver/attachable = %s/%v, want overlay/true", f.created[0].Driver, f.created[0].Attachable)
	}
}

func TestEndpointsForAssignsFixedIP(t *testing.T) {
	m, _ := newTestManager(t, "bridge")
	nc, err := m.endpointsFor("srv-1", "owner-A")
	if err != nil {
		t.Fatalf("endpointsFor: %v", err)
	}
	ep, ok := nc.EndpointsConfig["dylaris_tenant_owner-a"]
	if !ok {
		t.Fatalf("no endpoint for tenant net in %v", nc.EndpointsConfig)
	}
	if ep.IPAMConfig == nil || ep.IPAMConfig.IPv4Address != "10.0.0.3" {
		t.Fatalf("fixed IP = %v, want 10.0.0.3", ep.IPAMConfig)
	}
	// Empty ownerID reverse-resolves via the allocator (restart path).
	nc2, err := m.endpointsFor("srv-1", "")
	if err != nil {
		t.Fatalf("endpointsFor(empty owner): %v", err)
	}
	if _, ok := nc2.EndpointsConfig["dylaris_tenant_owner-a"]; !ok {
		t.Fatalf("empty-owner path did not resolve tenant net: %v", nc2.EndpointsConfig)
	}
}

func TestEndpointsForUnknownServerErrors(t *testing.T) {
	m, _ := newTestManager(t, "bridge")
	if _, err := m.endpointsFor("ghost", ""); err == nil {
		t.Fatal("endpointsFor(unknown, empty owner) err = nil, want error")
	}
}

func TestTenantEndpointsFallbackWhenDisabled(t *testing.T) {
	dm := &DockerManager{} // tenant == nil (isolation off)
	nc := dm.tenantEndpoints("srv-1", "owner-A", "global-net-id")
	ep, ok := nc.EndpointsConfig["dylaris_net"]
	if !ok || ep.NetworkID != "global-net-id" {
		t.Fatalf("fallback endpoints = %v, want dylaris_net -> global-net-id", nc.EndpointsConfig)
	}
}
