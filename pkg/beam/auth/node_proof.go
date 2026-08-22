package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Per-node beam ticket proof.
//
// The ticket JWT is signed with the fleet secret, because the beam-relay reads
// node_id out of it to route the connection and only holds that key. A BYON
// node never receives the fleet secret - the deploy snippet withholds it on
// purpose - so it could not verify that signature, and rejected every ticket:
// beam file access simply did not exist on a customer machine.
//
// So the ticket carries a SECOND authenticator alongside the signature. Core
// derives it from the PER-NODE secret, which it and that one node already
// share and which never crosses the wire. The node checks that instead of the
// signature, and needs no fleet secret at all.
//
// This is additive: the relay and the frozen beam stream header are untouched,
// and a fleet-signed ticket still validates the old way wherever the fleet
// secret exists. Nothing got weaker - the proof is a second lock on the same
// door, and only Core can turn it.

// nodeProofDomain separates this HMAC from every other use of the per-node
// secret (Redis credentials, the challenge response, the LAN certificate). The
// same key deriving two things from unseparated inputs is how one of them
// becomes an oracle for the other.
const nodeProofDomain = "dylaris-beam-node-proof:v1:"

// proofPayload is the exact byte string the proof covers.
//
// Every security-relevant claim is in here. That is the whole design: the node
// does NOT verify the JWT signature, so any claim left out of this string would
// be attacker-editable on an otherwise valid ticket - swap the username for an
// admin's, flip is_admin, point server_uuid at a neighbour, or push exp years
// out. Adding a claim to BeamClaims that decides access means adding it here.
func proofPayload(c BeamClaims) string {
	var exp int64
	if c.ExpiresAt != nil {
		exp = c.ExpiresAt.Unix()
	}
	return nodeProofDomain +
		c.NodeID + "|" +
		c.ServerUUID + "|" +
		c.Username + "|" +
		strconv.FormatBool(c.IsAdmin) + "|" +
		strconv.FormatInt(exp, 10)
}

// NodeProof derives the per-node authenticator for a set of claims.
func NodeProof(nodeSecret []byte, c BeamClaims) string {
	m := hmac.New(sha256.New, nodeSecret)
	m.Write([]byte(proofPayload(c)))
	return hex.EncodeToString(m.Sum(nil))
}

// ValidateBeamTicketByNodeProof reads a ticket using ONLY the per-node secret.
//
// The signature is deliberately not checked - the holder of this key cannot
// check it. Authenticity comes from the proof, expiry and issuer are enforced
// here rather than by the JWT library (which will not validate claims on a
// token it did not verify), and the caller must still enforce
// NodeID == its own id, exactly as with the signature path.
func ValidateBeamTicketByNodeProof(nodeSecret []byte, token string) (*BeamClaims, error) {
	if len(nodeSecret) == 0 {
		return nil, errors.New("auth: empty node secret")
	}
	var c BeamClaims
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"HS256"}))
	if _, _, err := parser.ParseUnverified(token, &c); err != nil {
		return nil, err
	}
	if c.Issuer != BeamIssuer {
		return nil, errors.New("auth: not a beam ticket")
	}
	if c.ExpiresAt == nil {
		return nil, errors.New("auth: ticket has no expiry")
	}
	if time.Now().After(c.ExpiresAt.Time) {
		return nil, errors.New("auth: ticket expired")
	}
	if c.NodeProof == "" {
		return nil, errors.New("auth: ticket carries no node proof")
	}
	want := NodeProof(nodeSecret, c)
	if subtle.ConstantTimeCompare([]byte(c.NodeProof), []byte(want)) != 1 {
		return nil, errors.New("auth: node proof does not verify")
	}
	return &c, nil
}
