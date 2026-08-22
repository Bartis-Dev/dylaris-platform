package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// A BYON node holds no fleet secret, so it cannot verify the JWT signature.
// The proof is what makes the ticket trustworthy to it, which means the proof
// has to be as strong a statement as the signature would have been.
func TestNodeProofRoundTrip(t *testing.T) {
	nodeSecret := []byte("per-node-secret-bytes")
	base := BeamClaims{ServerUUID: "srv-1", NodeID: "node-a", Username: "alice"}

	tok, err := SignBeamTicketWithNodeProof("fleet", base, time.Hour, nodeSecret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	claims, err := ValidateBeamTicketByNodeProof(nodeSecret, tok)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if claims.ServerUUID != "srv-1" || claims.Username != "alice" || claims.NodeID != "node-a" {
		t.Errorf("claims came back wrong: %+v", claims)
	}
	// The fleet signature must still be intact, or the relay stops routing.
	if _, err := ValidateBeamTicket("fleet", tok); err != nil {
		t.Errorf("fleet signature no longer validates: %v", err)
	}
}

func TestNodeProofRejects(t *testing.T) {
	nodeSecret := []byte("per-node-secret-bytes")
	other := []byte("some-other-node-secret")
	base := BeamClaims{ServerUUID: "srv-1", NodeID: "node-a", Username: "alice"}
	tok, _ := SignBeamTicketWithNodeProof("fleet", base, time.Hour, nodeSecret)

	t.Run("another node's secret does not verify it", func(t *testing.T) {
		if _, err := ValidateBeamTicketByNodeProof(other, tok); err == nil {
			t.Error("a ticket for one node verified with another node's secret")
		}
	})

	t.Run("a ticket with no proof is refused, not waved through", func(t *testing.T) {
		plain, _ := SignBeamTicketWithTTL("fleet", base, time.Hour)
		if _, err := ValidateBeamTicketByNodeProof(nodeSecret, plain); err == nil {
			t.Error("a ticket carrying no proof was accepted")
		}
	})

	t.Run("an empty node secret is refused", func(t *testing.T) {
		if _, err := ValidateBeamTicketByNodeProof(nil, tok); err == nil {
			t.Error("an empty secret was accepted")
		}
	})

	t.Run("an expired ticket is refused", func(t *testing.T) {
		old, _ := SignBeamTicketWithNodeProof("fleet", base, -time.Minute, nodeSecret)
		if _, err := ValidateBeamTicketByNodeProof(nodeSecret, old); err == nil {
			t.Error("an expired ticket was accepted")
		}
	})

	t.Run("a token from another issuer is refused", func(t *testing.T) {
		c := base
		c.RegisteredClaims = jwt.RegisteredClaims{
			Issuer:    "someone-else",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		}
		c.NodeProof = NodeProof(nodeSecret, c)
		raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte("fleet"))
		if _, err := ValidateBeamTicketByNodeProof(nodeSecret, raw); err == nil {
			t.Error("a token from another issuer was accepted")
		}
	})
}

// The node does NOT check the signature, so anything the proof does not cover
// is attacker-editable on an otherwise valid ticket. This is the test that
// fails if someone adds an access-deciding claim and forgets proofPayload.
func TestNodeProofCoversEveryAccessClaim(t *testing.T) {
	nodeSecret := []byte("per-node-secret-bytes")
	base := BeamClaims{ServerUUID: "srv-1", NodeID: "node-a", Username: "alice"}
	base.RegisteredClaims = jwt.RegisteredClaims{
		Issuer:    BeamIssuer,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	good := NodeProof(nodeSecret, base)

	tamper := map[string]func(*BeamClaims){
		"server_uuid": func(c *BeamClaims) { c.ServerUUID = "srv-victim" },
		"node_id":     func(c *BeamClaims) { c.NodeID = "node-victim" },
		"username":    func(c *BeamClaims) { c.Username = "admin" },
		"is_admin":    func(c *BeamClaims) { c.IsAdmin = true },
		"expiry":      func(c *BeamClaims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(9999 * time.Hour)) },
	}
	for name, edit := range tamper {
		t.Run("editing "+name+" breaks the proof", func(t *testing.T) {
			c := base
			edit(&c)
			if NodeProof(nodeSecret, c) == good {
				t.Errorf("%s is not covered by the proof - it can be changed on a valid ticket", name)
			}
		})
	}
}

// The per-node secret keys several unrelated things. Domain separation is what
// keeps one of them from becoming an oracle for another.
func TestNodeProofIsDomainSeparated(t *testing.T) {
	c := BeamClaims{ServerUUID: "srv-1", NodeID: "node-a"}
	if !strings.HasPrefix(proofPayload(c), nodeProofDomain) {
		t.Errorf("proof payload %q is not domain-separated", proofPayload(c))
	}
}
