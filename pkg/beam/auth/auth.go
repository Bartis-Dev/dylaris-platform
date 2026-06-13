// Package auth mirrors gateway/pkg/beam/auth so platform/core (signer) and
// platform/node (validator) can speak the same Beam-ticket JWT contract
// without a cross-repo Go-module dependency.
//
// Keep this in sync with: gateway/pkg/beam/auth/auth.go (byte-for-byte
// equivalent claim structure + signing parameters). An integration smoke
// test verifies a ticket signed here validates in the gateway relay.
package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const BeamIssuer = "dylaris-beam"
const BeamTicketTTL = 30 * time.Minute

type BeamClaims struct {
	ServerUUID string `json:"server_uuid"`
	NodeID     string `json:"node_id"`
	Username   string `json:"username"`
	IsAdmin    bool   `json:"is_admin"`
	jwt.RegisteredClaims
}

// SignBeamTicket signs a Beam ticket compatible with gateway/beam-relay.
func SignBeamTicket(secret string, c BeamClaims) (string, error) {
	return SignBeamTicketWithTTL(secret, c, BeamTicketTTL)
}

func SignBeamTicketWithTTL(secret string, c BeamClaims, ttl time.Duration) (string, error) {
	if secret == "" {
		return "", errors.New("auth: empty secret")
	}
	c.RegisteredClaims = jwt.RegisteredClaims{
		Issuer:    BeamIssuer,
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return t.SignedString([]byte(secret))
}

// ValidateBeamTicket is the read side — used by beam-relay (gateway) and
// node-side BeamNodeService.Authenticate. Callers must additionally enforce
// NodeID == localNodeID.
func ValidateBeamTicket(secret, token string) (*BeamClaims, error) {
	if secret == "" {
		return nil, errors.New("auth: empty secret")
	}
	var c BeamClaims
	tk, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	if !tk.Valid {
		return nil, errors.New("auth: invalid token")
	}
	return &c, nil
}
