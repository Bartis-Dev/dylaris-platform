package services

import (
	"encoding/json"
	"strings"
	"testing"
)

const leakedToken = "1f0a9c3b7e5d4a2f8c6b0e9d3a7f5c1b"

func aRoute() GatewayRoute {
	return GatewayRoute{
		Domain:     "survival.eu.example.com",
		TargetIP:   "mc_1a2b3c",
		TargetPort: 25565,
		TunnelID:   leakedToken,
		ServerUUID: "1a2b3c",
		CoreOwned:  true,
		OwnerID:    "user-1",
	}
}

// The tunnel token is a credential: the edge admits a tunnel that presents it
// and then round-robins player streams across every session registered under
// it, so a second holder receives about half of the connections meant for the
// first. For a managed server it is derived from the NODE, which makes it the
// same credential for every server that node hosts.
func TestPublicRoute_DropsTheTunnelToken(t *testing.T) {
	b, err := json.Marshal(aRoute().Public())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), leakedToken) {
		t.Errorf("the token survived into %s", b)
	}
	if strings.Contains(string(b), "tunnel_id") {
		t.Errorf("tunnel_id key present in %s", b)
	}
}

// Everything the panel actually renders has to survive, or the redaction is a
// regression rather than a fix.
func TestPublicRoute_KeepsWhatTheUIRenders(t *testing.T) {
	var got map[string]any
	b, _ := json.Marshal(aRoute().Public())
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]any{
		"domain":      "survival.eu.example.com",
		"target_ip":   "mc_1a2b3c",
		"target_port": float64(25565),
		"server_uuid": "1a2b3c",
		"core_owned":  true,
		"owner_id":    "user-1",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

// The stored shape must keep reading tunnel_id: that is how a route arrives
// from Redis, and the edge routing depends on it. Only the OUTBOUND shape drops
// it.
func TestGatewayRoute_StillParsesTheTokenFromRedis(t *testing.T) {
	var rt GatewayRoute
	raw := `{"domain":"a.example.com","tunnel_id":"` + leakedToken + `"}`
	if err := json.Unmarshal([]byte(raw), &rt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rt.TunnelID != leakedToken {
		t.Errorf("TunnelID = %q, want it read from storage unchanged", rt.TunnelID)
	}
}

// A handler encodes the result directly, so an empty list has to be [] and not
// null - the panel maps over it.
func TestPublicRoutes_EmptyEncodesAsList(t *testing.T) {
	b, _ := json.Marshal(PublicRoutes(nil))
	if string(b) != "[]" {
		t.Errorf("encoded %s, want []", b)
	}
}

func TestPublicRoutes_PreservesOrderAndRedactsEvery(t *testing.T) {
	in := []GatewayRoute{
		{Domain: "a.example.com", TunnelID: leakedToken},
		{Domain: "b.example.com", TunnelID: "another-token"},
	}
	b, _ := json.Marshal(PublicRoutes(in))
	s := string(b)
	if strings.Contains(s, leakedToken) || strings.Contains(s, "another-token") {
		t.Errorf("a token survived into %s", s)
	}
	if strings.Index(s, "a.example.com") > strings.Index(s, "b.example.com") {
		t.Errorf("order changed: %s", s)
	}
}

// A link's identity IS its token - the Redis key is link:<token> - so a listing
// has to name it somehow. The digest is that name: stable across polls, distinct
// per link, and useless as a tunnel claim.
func TestLinkFingerprint_NamesWithoutRevealing(t *testing.T) {
	fp := LinkFingerprint(leakedToken)
	if strings.Contains(fp, leakedToken) || strings.Contains(leakedToken, fp) {
		t.Errorf("fingerprint %q overlaps the token it stands for", fp)
	}
	if fp == LinkFingerprint("another-token") {
		t.Error("two different links got the same name")
	}
	if fp != LinkFingerprint(leakedToken) {
		t.Error("the same link got two different names, so a listing cannot be diffed across polls")
	}
	if len(fp) != 16 {
		t.Errorf("length = %d, want 16 hex characters", len(fp))
	}
}
