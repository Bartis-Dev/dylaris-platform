package auth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"time"
)

// Beam LAN fast-path TLS. The node serves the LAN listener over TLS using a
// self-signed certificate derived DETERMINISTICALLY from the shared beam secret
// and the node ID. Because Core holds the same secret and knows the node ID, it
// derives the identical certificate and can hand the Beam app the expected
// SHA-256 fingerprint to pin — encrypting the LAN hop AND defeating MITM without
// any extra heartbeat/registry plumbing. Pinning makes the certificate's
// validity window irrelevant; the dates are fixed only so the DER (and thus the
// fingerprint) is byte-for-byte reproducible on both sides.

// Fixed template fields for a reproducible certificate.
var (
	lanCertNotBefore = time.Unix(1700000000, 0).UTC() // 2023-11-14
	lanCertNotAfter  = time.Unix(4102444800, 0).UTC() // 2100-01-01
)

// zeroReader is a deterministic io.Reader (all zero bytes). ed25519 signing is
// deterministic and ignores the rand source, but x509.CreateCertificate still
// wants a non-nil reader; this keeps the output reproducible regardless.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// DeriveLANCert returns the node's deterministic self-signed TLS certificate for
// the Beam LAN fast-path plus its lowercase-hex SHA-256 fingerprint. Node and
// Core call this with the same (secret, nodeID) and get identical results.
func DeriveLANCert(secret, nodeID string) (tls.Certificate, string, error) {
	seed := sha256.Sum256([]byte("dylaris-beam-lan-tls\x00" + secret + "\x00" + nodeID))
	priv := ed25519.NewKeyFromSeed(seed[:])

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "dylaris-beam-node"},
		NotBefore:             lanCertNotBefore,
		NotAfter:              lanCertNotAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(zeroReader{}, tmpl, tmpl, priv.Public(), priv)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	fp := sha256.Sum256(der)
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
	return cert, hex.EncodeToString(fp[:]), nil
}

// LANCertFingerprint returns just the fingerprint (Core's use — it never needs
// the private key, only the value to hand the app for pinning).
func LANCertFingerprint(secret, nodeID string) (string, error) {
	_, fp, err := DeriveLANCert(secret, nodeID)
	return fp, err
}
